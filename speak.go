package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var voices = map[string]string{
	"en": filepath.Join(os.Getenv("HOME"), ".local/share/piper/en_US-lessac-medium.onnx"),
	"sw": filepath.Join(os.Getenv("HOME"), ".local/share/piper/sw_CD-lanfrica-medium.onnx"),
}

// speakNeeds reports what is missing to speak in lang, or "".
func speakNeeds(lang string) string {
	if m := missing("piper", "aplay"); m != "" {
		return "Needs " + m
	}
	if _, err := os.Stat(voices[lang]); err != nil {
		return "Needs " + filepath.Base(voices[lang])
	}
	return ""
}

// piper: text in, raw 16-bit PCM out. aplay: PCM in, speaker out.
func speak(ctx context.Context, text, lang string) error {
	piper := exec.CommandContext(ctx, "piper", "-m", voices[lang], "--output-raw", "--sentence-silence", "0.4")
	piper.Stdin = strings.NewReader(text)
	piper.Stderr = os.Stderr

	play := exec.CommandContext(ctx, "aplay", "-q", "-r", "22050", "-f", "S16_LE", "-c", "1")
	play.Stdin, _ = piper.StdoutPipe()
	play.Stderr = os.Stderr

	if err := play.Start(); err != nil {
		return err
	}
	return errors.Join(piper.Run(), play.Wait())
}
