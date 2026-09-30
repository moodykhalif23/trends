package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const bytesPerSec = 22050 * 2 // piper output: 22050 Hz, 16-bit mono

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
			break
		}
	}
	stop()
	wg.Wait()
	in.Close()
	err = errors.Join(synthErr, play.Wait())
	if start.IsZero() {
		return 0, err
	}
	heard := min(written, int(time.Since(start).Seconds()*bytesPerSec))
	return max(heard-bytesPerSec/2, 0) &^ 1, err
}

func synth(ctx context.Context, line, lang string) ([]byte, error) {
	text, heading := strings.CutPrefix(line, "## ")
	cmd := exec.CommandContext(ctx, "piper", "-m", voices[lang], "--output-raw", "--sentence-silence", "0.4")
	cmd.Stdin = strings.NewReader(text + "\n")
	cmd.Stderr = os.Stderr
	pcm, err := cmd.Output()
	if heading {
		pause := make([]byte, bytesPerSec*3/5) // 0.6s
		pcm = append(append(pause, pcm...), pause...)
	}
	return pcm, err
}
