package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const sampleRate = 22050 // piper output: 16-bit mono

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

func speak(ctx context.Context, lines []string, from int, lang string) (int, error) {
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

	// Synthesize one paragraph ahead of playback.
	audio := make(chan []byte, 1)
	var synthErr error
	go func() {
		defer close(audio)
		for _, line := range lines[from:] {
			pcm, err := synth(ctx, line, lang)
			if err != nil {
				synthErr = err
				return
			}
			select {
			case audio <- pcm:
			case <-ctx.Done():
				return
			}
		}
	}()

	done := 0
	for pcm := range audio {
		if _, err := in.Write(pcm); err != nil {
			break
		}
		done++
	}
	stop()
	in.Close()
	return done, errors.Join(synthErr, play.Wait())
}

func synth(ctx context.Context, line, lang string) ([]byte, error) {
	text, heading := strings.CutPrefix(line, "## ")
	cmd := exec.CommandContext(ctx, "piper", "-m", voices[lang], "--output-raw", "--sentence-silence", "0.4")
	cmd.Stdin = strings.NewReader(text + "\n")
	cmd.Stderr = os.Stderr
	pcm, err := cmd.Output()
	if heading {
		pause := make([]byte, sampleRate*2*3/5) // 0.6s of silence
		pcm = append(append(pause, pcm...), pause...)
	}
	return pcm, err
}
