package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Every frame is scaled and padded to this size, so every frame is exactly
// videoW*videoH*4 bytes (R,G,B,A = 1 byte each).
const videoW, videoH = 640, 360

func remoteVideo(ctx context.Context, link, poster string) fyne.CanvasObject {
	img := &canvas.Image{FillMode: canvas.ImageFillContain, ScaleMode: canvas.ImageScaleFastest}
	img.SetMinSize(fyne.NewSize(videoW, videoH))

	if poster != "" {
		go func() {
			if m, err := fetchImage(ctx, poster); err == nil && ctx.Err() == nil {
				fyne.Do(func() { img.Image = m; img.Refresh() })
			}
		}()
	}

	var play *widget.Button
	var stop context.CancelFunc
	play = widget.NewButtonWithIcon("Play video", theme.MediaPlayIcon(), func() {
		if stop != nil {
			stop() // kills ffmpeg (sound too); playVideo returns and the goroutine resets the button
			return
		}
		pctx, cancel := context.WithCancel(ctx)
		stop = cancel
		play.SetText("Stop")
		play.SetIcon(theme.MediaStopIcon())
		go func() {
			if err := playVideo(pctx, link, img); err != nil && pctx.Err() == nil {
				log.Printf("video FAIL %s  err=%v", link, err)
			}
			cancel() // also on a normal finish, or the context leaks (go vet checks this)
			fyne.Do(func() {
				stop = nil
				play.SetText("Play video")
				play.SetIcon(theme.MediaPlayIcon())
			})
		}()
	})
	return container.NewBorder(nil, play, nil, nil, img)
}

func playVideo(ctx context.Context, link string, img *canvas.Image) error {
	inputs := []string{link}
	if strings.Contains(link, "youtube.com/embed/") {
		out, err := exec.CommandContext(ctx, "yt-dlp", "-g", "-f", "bv*[height<=480]+ba/b", link).Output()
		if err != nil {
			return fmt.Errorf("yt-dlp: %w", err)
		}
		inputs = strings.Fields(string(out))
		if len(inputs) == 0 {
			return fmt.Errorf("yt-dlp: no URL for %s", link) // else inputs[0] below panics
		}
	}

	args := []string{"-loglevel", "error"}
	for _, in := range inputs {
		args = append(args, "-re", "-user_agent", userAgent, "-i", in)
	}
	args = append(args, "-map", "0:v:0",
		"-vf", fmt.Sprintf("scale=%[1]d:%[2]d:force_original_aspect_ratio=decrease,pad=%[1]d:%[2]d:(ow-iw)/2:(oh-ih)/2", videoW, videoH),
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	switch {
	case len(inputs) == 2:
		args = append(args, "-map", "1:a:0", "-f", "pulse", "default")
	case hasAudio(ctx, inputs[0]):
		args = append(args, "-map", "0:a:0", "-f", "pulse", "default")
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = os.Stderr

	// StdoutPipe gives an io.ReadCloser — the same interface as an HTTP body.
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	front := image.NewRGBA(image.Rect(0, 0, videoW, videoH))
	back := image.NewRGBA(image.Rect(0, 0, videoW, videoH))
	var frames, slow int
	for {
		if _, err := io.ReadFull(out, back.Pix); err != nil {
			break
		}

		t := time.Now()
		fyne.DoAndWait(func() {
			img.Image = back
			img.Refresh()
		})
		frames++
		if time.Since(t) > 33*time.Millisecond {
			slow++
		}
		front, back = back, front
	}
	log.Printf("video: %d frames, %d slow", frames, slow)
	return cmd.Wait()
}

func hasAudio(ctx context.Context, link string) bool {
	out, _ := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-user_agent", userAgent,
		"-select_streams", "a:0", "-show_entries", "stream=index", "-of", "csv=p=0", link).Output()
	return len(bytes.TrimSpace(out)) > 0
}
