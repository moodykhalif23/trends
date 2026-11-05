package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	kokoroModelName  = "kokoro-v1.0.int8.onnx"
	kokoroVoicesName = "voices-v1.0.bin"
	kokoroReleaseURL = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.1/"
	kokoroPythonCode = `import sys
import json
import struct
import numpy as np
from kokoro_onnx import Kokoro
kokoro = Kokoro(sys.argv[1], sys.argv[2])
for request in sys.stdin:
    text = json.loads(request)
    audio, sample_rate = kokoro.create(text, voice=sys.argv[3], speed=1.0, lang="en-us")
    if sample_rate != 24000:
        raise RuntimeError(f"unexpected Kokoro sample rate: {sample_rate}")
    pcm = np.asarray(audio, dtype="<f4").tobytes()
    sys.stdout.buffer.write(struct.pack("<Q", len(pcm)))
    sys.stdout.buffer.write(pcm)
    sys.stdout.buffer.flush()
`
)

const kokoroInstallRequirements = "kokoro-onnx==0.6.1"
const maxKokoroAudioBytes = 256 << 20

var kokoroVoices = []string{
	"af_heart", "af_bella", "af_nicole", "af_sarah", "af_sky",
	"af_alloy", "af_aoede", "af_jessica", "af_kore", "af_nova", "af_river",
	"am_adam", "am_echo", "am_eric", "am_fenrir", "am_liam", "am_michael", "am_onyx", "am_puck", "am_santa",
}

var kokoroAssets = []voiceAsset{
	{
		name:   kokoroModelName,
		size:   114119327,
		sha256: "ae315a79b623f244700e4afb9246c46a26066782e049ba174bf3ba433970ee9c",
	},
	{
		name:   kokoroVoicesName,
		size:   28214398,
		sha256: "bca610b8308e8d99f32e6fe4197e7ec01679264efed0cac9140fe9c29f1fbf7d",
	},
}

var kokoroSetupMu sync.Mutex
var kokoroReady bool
var kokoroPythonPath string
var kokoroModelPath string
var kokoroVoicesPath string

func isKokoroVoice(voice string) bool {
	for _, known := range kokoroVoices {
		if voice == known {
			return true
		}
	}
	return false
}

func kokoroCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}
	return filepath.Join(dir, "trends", "kokoro"), nil
}

func ensureKokoro(ctx context.Context) (python, model, voices string, err error) {
	kokoroSetupMu.Lock()
	defer kokoroSetupMu.Unlock()
	if kokoroReady {
		return kokoroPythonPath, kokoroModelPath, kokoroVoicesPath, nil
	}

	dir, err := kokoroCacheDir()
	if err != nil {
		return "", "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", "", fmt.Errorf("create Kokoro cache: %w", err)
	}

	pythonName := strings.TrimSpace(os.Getenv("PYTHON"))
	if pythonName == "" {
		pythonName = "python3"
	}
	python, err = exec.LookPath(pythonName)
	if err != nil {
		return "", "", "", fmt.Errorf("Kokoro requires Python 3.10–3.13: %w", err)
	}
	versionCheck := exec.CommandContext(ctx, python, "-c", "import sys; assert (3, 10) <= sys.version_info[:2] < (3, 14), f'Python 3.10–3.13 required, found {sys.version.split()[0]}'")
	if output, err := versionCheck.CombinedOutput(); err != nil {
		return "", "", "", fmt.Errorf("Kokoro requires Python 3.10–3.13: %s", strings.TrimSpace(string(output)))
	}

	for _, asset := range kokoroAssets {
		path := filepath.Join(dir, asset.name)
		if err := verifyVoiceAsset(path, asset); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return "", "", "", fmt.Errorf("replace invalid Kokoro asset %s: %w", path, removeErr)
			}
		}
		if err := downloadVoiceAsset(ctx, voiceDownloadClient, kokoroReleaseURL+asset.name, path, asset.size, asset.sha256); err != nil {
			return "", "", "", fmt.Errorf("download Kokoro asset %s: %w", asset.name, err)
		}
	}

	envDir := filepath.Join(dir, "venv")
	python = filepath.Join(envDir, "bin", "python")
	if _, err := os.Stat(python); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, pythonName, "-m", "venv", envDir)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", "", "", fmt.Errorf("create Kokoro Python environment: %w", err)
		}
	} else if err != nil {
		return "", "", "", fmt.Errorf("check Kokoro Python environment: %w", err)
	}

	importCheck := exec.CommandContext(ctx, python, "-c", "import kokoro_onnx")
	if err := importCheck.Run(); err != nil {
		install := exec.CommandContext(ctx, python, "-m", "pip", "install", "--disable-pip-version-check", kokoroInstallRequirements)
		install.Stdout = os.Stderr
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			return "", "", "", fmt.Errorf("install Kokoro runtime: %w", err)
		}
		if err := exec.CommandContext(ctx, python, "-c", "import kokoro_onnx").Run(); err != nil {
			return "", "", "", fmt.Errorf("verify Kokoro runtime: %w", err)
		}
	}
	kokoroPythonPath = python
	kokoroModelPath = filepath.Join(dir, kokoroModelName)
	kokoroVoicesPath = filepath.Join(dir, kokoroVoicesName)
	kokoroReady = true
	return kokoroPythonPath, kokoroModelPath, kokoroVoicesPath, nil
}

type kokoroStream struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func startKokoroStream(ctx context.Context, voice string) (*kokoroStream, error) {
	if !isKokoroVoice(voice) {
		return nil, fmt.Errorf("unsupported Kokoro voice %q", voice)
	}
	python, model, voices, err := ensureKokoro(ctx)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, python, "-u", "-c", kokoroPythonCode, model, voices, voice)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Kokoro input: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("open Kokoro output: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start Kokoro runtime: %w", err)
	}
	return &kokoroStream{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (k *kokoroStream) synthesize(text string) ([]byte, error) {
	request, err := json.Marshal(text)
	if err != nil {
		return nil, fmt.Errorf("encode Kokoro request: %w", err)
	}
	if _, err := k.stdin.Write(append(request, '\n')); err != nil {
		return nil, fmt.Errorf("send text to Kokoro: %w", err)
	}
	var header [8]byte
	if _, err := io.ReadFull(k.stdout, header[:]); err != nil {
		return nil, fmt.Errorf("read Kokoro audio size: %w", err)
	}
	size := binary.LittleEndian.Uint64(header[:])
	if size > maxKokoroAudioBytes {
		killErr := k.cmd.Process.Kill()
		return nil, errors.Join(fmt.Errorf("Kokoro audio is too large: %d bytes", size), killErr)
	}
	audio := make([]byte, int(size))
	if _, err := io.ReadFull(k.stdout, audio); err != nil {
		return nil, fmt.Errorf("read Kokoro audio: %w", err)
	}
	return kokoroFloat32ToPCM16(audio)
}

func (k *kokoroStream) close() error {
	var closeErr error
	if err := k.stdin.Close(); err != nil {
		closeErr = fmt.Errorf("close Kokoro input: %w", err)
	}
	waitErr := k.cmd.Wait()
	if waitErr != nil {
		waitErr = fmt.Errorf("stop Kokoro runtime: %w", waitErr)
	}
	return errors.Join(closeErr, waitErr)
}

func kokoroFloat32ToPCM16(audio []byte) ([]byte, error) {
	if len(audio)%4 != 0 {
		return nil, fmt.Errorf("invalid Kokoro audio: %d bytes is not float32-aligned", len(audio))
	}
	pcm := make([]byte, len(audio)/2)
	for i := 0; i < len(audio); i += 4 {
		sample := math.Float32frombits(binary.LittleEndian.Uint32(audio[i : i+4]))
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			return nil, fmt.Errorf("invalid Kokoro audio: non-finite sample at index %d", i/4)
		}
		sample = min(max(sample, -1), 1)
		value := int16(math.Round(float64(sample) * math.MaxInt16))
		binary.LittleEndian.PutUint16(pcm[i/2:i/2+2], uint16(value))
	}
	return pcm, nil
}
