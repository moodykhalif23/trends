package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var voice = filepath.Join(os.Getenv("HOME"), ".local/share/piper/en_US-lessac-medium.onnx")

// piper: text in, raw 16-bit PCM out. aplay: PCM in, speaker out.
func speak(ctx context.Context, text string) error {
	piper := exec.CommandContext(ctx, "piper", "-m", voice, "--output-raw", "--sentence-silence", "0.4")
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
