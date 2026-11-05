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

const piperBytesPerSec = 22050 * 2

var billionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*bn\b`)
var millionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*m\b`)
var dollarAmount = regexp.MustCompile(`(?i)(US)?\$\s*(\d[\d,]*(?:\.\d+)?)\s*(billion|bn|b|million|mn|m|thousand|k|trillion|T)?\b`)
var trillionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*T\b`)
var monthAbbreviation = regexp.MustCompile(`(?i)\b(Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sept|Sep|Oct|Nov|Dec)\b\.?`)

var months = map[string][2]string{
	"jan":  {"January", "Januari"},
	"feb":  {"February", "Februari"},
	"mar":  {"March", "Machi"},
	"apr":  {"April", "Aprili"},
	"jun":  {"June", "Juni"},
	"jul":  {"July", "Julai"},
	"aug":  {"August", "Agosti"},
	"sep":  {"September", "Septemba"},
	"sept": {"September", "Septemba"},
	"oct":  {"October", "Oktoba"},
	"nov":  {"November", "Novemba"},
	"dec":  {"December", "Desemba"},
}

var voices = map[string]string{
	"sw": filepath.Join(os.Getenv("HOME"), ".local/share/piper/sw_CD-lanfrica-medium.onnx"),
}

var voiceDownloadClient = &http.Client{Timeout: 10 * time.Minute}

func voicePath(lang string) string {
	return voices[lang]
}

type voiceAsset struct {
	name   string
	size   int64
	sha256 string
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

func verifyVoiceAsset(path string, asset voiceAsset) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != asset.size {
		return fmt.Errorf("unexpected size: got %d bytes, want %d", info.Size(), asset.size)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if got := fmt.Sprintf("%x", hash.Sum(nil)); got != asset.sha256 {
		return fmt.Errorf("SHA-256 mismatch: got %s", got)
	}
	return nil
}

// speakNeeds reports what is missing to speak in lang, or "".
func speakNeeds(lang string) string {
	if lang == "en" {
		python := strings.TrimSpace(os.Getenv("PYTHON"))
		if python == "" {
			python = "python3"
		}
		if _, err := exec.LookPath(python); err != nil {
			return "Needs Python 3.10–3.13"
		}
		if m := missing("aplay"); m != "" {
			return "Needs " + m
		}
		return ""
	}
	if m := missing("piper", "aplay"); m != "" {
		return "Needs " + m
	}
	path := voicePath(lang)
	if _, err := os.Stat(path); err != nil {
		return "Needs " + filepath.Base(path)
	}
	return ""
}

type speaker struct {
	lang  string
	voice string
	lines []string
	pcm   [][]byte
}

func (s *speaker) play(ctx context.Context, from int) (int, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	bytesPerSec := piperBytesPerSec
	sampleRate := 22050
	if s.lang == "en" {
		bytesPerSec = 24000 * 2
		sampleRate = 24000
	}
	var kokoro *kokoroStream
	var err error
	if s.lang == "en" && len(s.pcm) < len(s.lines) {
		kokoro, err = startKokoroStream(ctx, s.voice)
		if err != nil {
			return 0, err
		}
	}
	play := exec.CommandContext(ctx, "aplay", "-q", "-r", strconv.Itoa(sampleRate), "-f", "S16_LE", "-c", "1")
	in, err := play.StdinPipe()
	if err != nil {
		if kokoro != nil {
			return 0, errors.Join(err, kokoro.close())
		}
		return 0, err
	}
	if err := play.Start(); err != nil {
		if kokoro != nil {
			return 0, errors.Join(err, kokoro.close())
		}
		return 0, err
	}

	// Synthesize one line ahead of playback, skipping lines an earlier run already made.
	audio := make(chan []byte, 1)
	var synthErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		defer func() {
			if kokoro != nil {
				if err := kokoro.close(); err != nil {
					synthErr = errors.Join(synthErr, err)
				}
			}
			close(audio)
		}()
		for i := range s.lines {
			if i >= len(s.pcm) {
				pcm, err := synth(ctx, s.lines[i], s.lang, kokoro)
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
	heard := min(written, int(time.Since(start).Seconds()*float64(bytesPerSec)))
	return max(heard-bytesPerSec/2, 0) &^ 1, err
}

func synth(ctx context.Context, line, lang string, kokoro *kokoroStream) ([]byte, error) {
	text, heading := strings.CutPrefix(line, "## ")
	text = normalizeSpeechText(text, lang)
	var pcm []byte
	var err error
	if lang == "en" {
		if kokoro == nil {
			return nil, fmt.Errorf("Kokoro runtime is not started")
		}
		pcm, err = kokoro.synthesize(text)
	} else {
		model := voicePath(lang)
		if _, err := os.Stat(model); err != nil {
			return nil, fmt.Errorf("voice model %s: %w", model, err)
		}
		cmd := exec.CommandContext(ctx, "piper", "-m", model, "--output-raw", "--sentence-silence", "0.4")
		cmd.Stdin = strings.NewReader(text + "\n")
		cmd.Stderr = os.Stderr
		pcm, err = cmd.Output()
	}
	if heading {
		bytesPerSec := piperBytesPerSec
		if lang == "en" {
			bytesPerSec = 24000 * 2
		}
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
	text = millionAbbreviation.ReplaceAllString(text, "${1} "+million)
	return normalizeMonthAbbreviations(text, lang)
}

func normalizeMonthAbbreviations(text, lang string) string {
	matches := monthAbbreviation.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}

	var result strings.Builder
	last := 0
	for _, match := range matches {
		abbreviation := text[match[0]:match[1]]
		abbreviatedMonth := strings.TrimSuffix(abbreviation, ".")
		after := strings.TrimLeft(text[match[1]:], " \t\r\n")
		dateFollows := len(after) > 0 && after[0] >= '0' && after[0] <= '9'
		if abbreviatedMonth == strings.ToUpper(abbreviatedMonth) && !strings.HasSuffix(abbreviation, ".") && !dateFollows {
			result.WriteString(text[last:match[1]])
			last = match[1]
			continue
		}

		key := strings.ToLower(abbreviatedMonth)
		name := months[key][0]
		if lang == "sw" {
			name = months[key][1]
		}
		result.WriteString(text[last:match[0]])
		result.WriteString(name)
		if strings.HasSuffix(abbreviation, ".") && !dateFollows {
			result.WriteByte('.')
		}
		last = match[1]
	}
	result.WriteString(text[last:])
	return result.String()
}
