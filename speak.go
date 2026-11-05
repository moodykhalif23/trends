package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const bytesPerSec = 22050 * 2 // piper output: 22050 Hz, 16-bit mono

const (
	englishVoiceName = "en_US-lessac-high.onnx"
	voiceAssetsURL   = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/en/en_US/lessac/high/"
	englishVoiceSize = 113895201
)

var billionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*bn\b`)
var millionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*m\b`)
var dollarAmount = regexp.MustCompile(`(?i)(US)?\$\s*(\d[\d,]*(?:\.\d+)?)\s*(billion|bn|b|million|mn|m|thousand|k|trillion|T)?\b`)
var trillionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*T\b`)

var voices = map[string]string{
	"sw": filepath.Join(os.Getenv("HOME"), ".local/share/piper/sw_CD-lanfrica-medium.onnx"),
}

var voiceDownloadClient = &http.Client{Timeout: 10 * time.Minute}
var voiceInstallMu sync.Mutex

func voicePath(lang string) string {
	if lang == "en" {
		if path := os.Getenv("PIPER_EN_VOICE"); path != "" {
			return path
		}
		return filepath.Join(os.Getenv("HOME"), ".local/share/piper", englishVoiceName)
	}
	return voices[lang]
}

type voiceAsset struct {
	name   string
	size   int64
	sha256 string
}

var englishVoiceAssets = []voiceAsset{
	{
		name:   englishVoiceName,
		size:   englishVoiceSize,
		sha256: "4cabf7c3a638017137f34a1516522032d4fe3f38228a843cc9b764ddcbcd9e09",
	},
	{
		name:   englishVoiceName + ".json",
		size:   4883,
		sha256: "db42b97d9859f257bc1561b8ed980e7fb2398402050a74ddd6cbec931a92412f",
	},
}

func ensureVoice(ctx context.Context, lang string) (string, error) {
	path := voicePath(lang)
	if lang != "en" || os.Getenv("PIPER_EN_VOICE") != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("voice model %s: %w", path, err)
		}
		return path, nil
	}

	voiceInstallMu.Lock()
	defer voiceInstallMu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create voice directory: %w", err)
	}
	for _, asset := range englishVoiceAssets {
		target := filepath.Join(dir, asset.name)
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("check voice file %s: %w", target, err)
		}
		if err := downloadVoiceAsset(ctx, voiceDownloadClient, voiceAssetsURL+asset.name, target, asset.size, asset.sha256); err != nil {
			return "", fmt.Errorf("download English voice %s: %w", asset.name, err)
		}
	}
	return path, nil
}

func downloadVoiceAsset(ctx context.Context, client *http.Client, url, path string, size int64, expectedSHA256 string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".download-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, size+1))
	if err != nil {
		tmp.Close()
		return err
	}
	if n != size {
		tmp.Close()
		return fmt.Errorf("unexpected size: got %d bytes, want %d", n, size)
	}
	if got := fmt.Sprintf("%x", hash.Sum(nil)); got != expectedSHA256 {
		tmp.Close()
		return fmt.Errorf("SHA-256 mismatch: got %s", got)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// speakNeeds reports what is missing to speak in lang, or "".
func speakNeeds(lang string) string {
	if m := missing("piper", "aplay"); m != "" {
		return "Needs " + m
	}
	if lang == "en" && os.Getenv("PIPER_EN_VOICE") == "" {
		return ""
	}
	path := voicePath(lang)
	if _, err := os.Stat(path); err != nil {
		return "Needs " + filepath.Base(path)
	}
	return ""
}

type speaker struct {
	lang  string
	lines []string
	pcm   [][]byte
}

func (s *speaker) play(ctx context.Context, from int) (int, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	play := exec.CommandContext(ctx, "aplay", "-q", "-r", "22050", "-f", "S16_LE", "-c", "1")
	in, err := play.StdinPipe()
	if err != nil {
		return 0, err
	}
	if err := play.Start(); err != nil {
		return 0, err
	}

	// Synthesize one line ahead of playback, skipping lines an earlier run already made.
	audio := make(chan []byte, 1)
	var synthErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(audio)
		for i := range s.lines {
			if i >= len(s.pcm) {
				pcm, err := synth(ctx, s.lines[i], s.lang)
				if err != nil {
					synthErr = err
					return
				}
				s.pcm = append(s.pcm, pcm)
			}
			select {
			case audio <- s.pcm[i]:
			case <-ctx.Done():
				return
			}
		}
	})

	var start time.Time
	written := 0
	var writeErr error
	for pcm := range audio {
		if from >= len(pcm) {
			from -= len(pcm)
			continue
		}
		if start.IsZero() {
			start = time.Now()
		}
		n, err := in.Write(pcm[from:])
		from = 0
		written += n
		if err != nil {
			writeErr = err
			stop()
			break
		}
	}
	wg.Wait()
	closeErr := in.Close()
	err = errors.Join(synthErr, writeErr, closeErr, play.Wait())
	if start.IsZero() {
		return 0, err
	}
	heard := min(written, int(time.Since(start).Seconds()*bytesPerSec))
	return max(heard-bytesPerSec/2, 0) &^ 1, err
}

func synth(ctx context.Context, line, lang string) ([]byte, error) {
	text, heading := strings.CutPrefix(line, "## ")
	text = normalizeSpeechText(text, lang)
	model, err := ensureVoice(ctx, lang)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "piper", "-m", model, "--output-raw", "--sentence-silence", "0.4")
	cmd.Stdin = strings.NewReader(text + "\n")
	cmd.Stderr = os.Stderr
	pcm, err := cmd.Output()
	if heading {
		pause := make([]byte, bytesPerSec*3/5) // 0.6s
		pcm = append(append(pause, pcm...), pause...)
	}
	return pcm, err
}

func normalizeSpeechText(text, lang string) string {
	trillion, billion, million := "trillion", "billion", "million"
	thousand, dollars := "thousand", "US dollars"
	if lang == "sw" {
		trillion, billion, million = "trilioni", "bilioni", "milioni"
		thousand, dollars = "elfu", "dola za Marekani"
	}
	text = dollarAmount.ReplaceAllStringFunc(text, func(amount string) string {
		parts := dollarAmount.FindStringSubmatch(amount)
		scale := strings.ToLower(parts[3])
		switch scale {
		case "t", "trillion":
			scale = trillion
		case "b", "bn", "billion":
			scale = billion
		case "m", "mn", "million":
			scale = million
		case "k", "thousand":
			scale = thousand
		default:
			scale = ""
		}
		currency := dollars
		if lang == "en" && scale == "" {
			value, err := strconv.ParseFloat(strings.ReplaceAll(parts[2], ",", ""), 64)
			if err == nil && value == 1 {
				currency = "US dollar"
			}
		}
		return strings.Join(strings.Fields(parts[2]+" "+scale+" "+currency), " ")
	})
	text = trillionAbbreviation.ReplaceAllString(text, "${1} "+trillion)
	text = billionAbbreviation.ReplaceAllString(text, "${1} "+billion)
	return millionAbbreviation.ReplaceAllString(text, "${1} "+million)
}
