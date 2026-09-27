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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Every frame is scaled and padded to this size, so every frame is exactly
// videoW*videoH*4 bytes (R,G,B,A = 1 byte each).
const videoW, videoH = 640, 360

func remoteVideo(ctx context.Context, link string) fyne.CanvasObject {
	img := &canvas.Image{FillMode: canvas.ImageFillContain}
	img.SetMinSize(fyne.NewSize(videoW, videoH))

	var play *widget.Button
	play = widget.NewButtonWithIcon("Play video", theme.MediaPlayIcon(), func() {
		play.Disable()
		go func() {
			if err := playVideo(ctx, link, img); err != nil && ctx.Err() == nil {
				log.Printf("video FAIL %s  err=%v", link, err)
			}
			fyne.Do(play.Enable)
		}()
	})
	return container.NewBorder(nil, play, nil, nil, img)
}

func playVideo(ctx context.Context, link string, img *canvas.Image) error {
	if strings.Contains(link, "youtube.com/embed/") {
		out, err := exec.CommandContext(ctx, "yt-dlp", "-g", "-f", "b[height<=480]/b", link).Output()
		if err != nil {
			return fmt.Errorf("yt-dlp: %w", err)
		}
		link = strings.TrimSpace(string(out))
	}

	args := []string{"-loglevel", "error", "-re", "-user_agent", userAgent, "-i", link,
		"-map", "0:v:0",
		"-vf", fmt.Sprintf("scale=%[1]d:%[2]d:force_original_aspect_ratio=decrease,pad=%[1]d:%[2]d:(ow-iw)/2:(oh-ih)/2", videoW, videoH),
		"-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1"}
	if hasAudio(ctx, link) {
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
	for {
		if _, err := io.ReadFull(out, back.Pix); err != nil {
			break
		}

		fyne.DoAndWait(func() {
			img.Image = back
			img.Refresh()
		})
		front, back = back, front
	}
	return cmd.Wait()
}

func hasAudio(ctx context.Context, link string) bool {
	out, _ := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-user_agent", userAgent,
		"-select_streams", "a:0", "-show_entries", "stream=index", "-of", "csv=p=0", link).Output()
	return len(bytes.TrimSpace(out)) > 0
}
