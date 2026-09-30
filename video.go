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

	// Pause kills ffmpeg and remembers where we were; Play restarts from there with -ss.
	var inputs []string
	var pos time.Duration
	play := toggleButton(ctx, "Play video", theme.MediaPlayIcon(), "Pause", theme.MediaPauseIcon(), func(c context.Context) error {
		if inputs == nil {
			var err error
			if inputs, err = mediaURLs(c, link); err != nil {
				return err
			}
		}
		played, err := playVideo(c, inputs, pos, img)
		if c.Err() != nil {
			pos += played
		} else {
			pos = 0
		}
		return err
	})
	need := []string{"ffmpeg", "ffprobe"}
	if strings.Contains(link, "youtube.com/embed/") {
		need = append(need, "yt-dlp")
	}
	if m := missing(need...); m != "" {
		play.SetText("Needs " + m)
		play.Disable()
	}
	return container.NewBorder(nil, play, nil, nil, img)
}

// mediaURLs turns a YouTube embed into direct stream URLs (video, audio); anything else is used as is.
func mediaURLs(ctx context.Context, link string) ([]string, error) {
	if !strings.Contains(link, "youtube.com/embed/") {
		return []string{link}, nil
	}
	out, err := exec.CommandContext(ctx, "yt-dlp", "-g", "-f", "bv*[height<=480]+ba/b", link).Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}
	urls := strings.Fields(string(out))
	if len(urls) == 0 {
		return nil, fmt.Errorf("yt-dlp: no URL for %s", link)
	}
	return urls, nil
}

// playVideo plays from `from` until the end, pause or Back, and reports how long it played.
func playVideo(ctx context.Context, inputs []string, from time.Duration, img *canvas.Image) (time.Duration, error) {
	args := []string{"-loglevel", "error"}
	for _, in := range inputs {
		args = append(args, "-ss", fmt.Sprintf("%.2f", from.Seconds()), "-re", "-user_agent", userAgent, "-i", in)
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
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	front := image.NewRGBA(image.Rect(0, 0, videoW, videoH))
	back := image.NewRGBA(image.Rect(0, 0, videoW, videoH))
	var frames, slow int
	var first time.Time
	for {
		if _, err := io.ReadFull(out, back.Pix); err != nil {
			break
		}
		if frames == 0 {
			first = time.Now()
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
	var played time.Duration
	if frames > 0 {
		played = time.Since(first)
	}
	return played, cmd.Wait()
}

func hasAudio(ctx context.Context, link string) bool {
	out, _ := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-user_agent", userAgent,
		"-select_streams", "a:0", "-show_entries", "stream=index", "-of", "csv=p=0", link).Output()
	return len(bytes.TrimSpace(out)) > 0
}
