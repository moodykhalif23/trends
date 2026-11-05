package main

import (
	"context"
	"errors"
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

var billionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*bn\b`)
var millionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*m\b`)
var dollarAmount = regexp.MustCompile(`(?i)(US)?\$\s*(\d[\d,]*(?:\.\d+)?)\s*(billion|bn|b|million|mn|m|thousand|k|trillion|T)?\b`)
var trillionAbbreviation = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)\s*T\b`)

var voices = map[string]string{
	"en": filepath.Join(os.Getenv("HOME"), ".local/share/piper/en_US-lessac-medium.onnx"),
	"sw": filepath.Join(os.Getenv("HOME"), ".local/share/piper/sw_CD-lanfrica-medium.onnx"),
}

var preferredEnglishVoice = filepath.Join(os.Getenv("HOME"), ".local/share/piper/en_US-amy-medium.onnx")

func voicePath(lang string) string {
	if lang == "en" {
		if path := os.Getenv("PIPER_EN_VOICE"); path != "" {
			return path
		}
		if _, err := os.Stat(preferredEnglishVoice); err == nil {
			return preferredEnglishVoice
		}
	}
	return voices[lang]
}

// speakNeeds reports what is missing to speak in lang, or "".
func speakNeeds(lang string) string {
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
	cmd := exec.CommandContext(ctx, "piper", "-m", voicePath(lang), "--output-raw", "--sentence-silence", "0.4")
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
