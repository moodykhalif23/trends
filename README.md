# Trends

A desktop news reader written in Go. Pulls stories from Hacker News and major RSS feeds, shows a short preview of each article, plays embedded video, and reads articles aloud in English or Swahili.

## Features

- Topics: Tech, Finance, Science, Medicine, Education, World, plus free-text search
- Article preview with images and subheadings, extracted from the publisher's page
- Inline video playback, including YouTube embeds
- Listen: text-to-speech of the article
- Sikiliza: the article translated to Swahili by Claude, then spoken
- Light and dark theme

## Requirements

- Go 1.26+
- Linux with PipeWire or PulseAudio

Optional. Buttons stay disabled until the tool they need is installed.

| Feature | Needs |
|---|---|
| Video | `ffmpeg`, `ffprobe` |
| YouTube | `yt-dlp` (recent; Deno recommended) |
| Listen | `piper`, `aplay`, English voice |
| Sikiliza | Swahili voice, `ANTHROPIC_API_KEY` |

Voices go in `~/.local/share/piper/`:

```
en_US-lessac-medium.onnx  (+ .onnx.json)
sw_CD-lanfrica-medium.onnx  (+ .onnx.json)
```

Download from https://huggingface.co/rhasspy/piper-voices.

## Run

```
go run .
```

## Layout

| File | Purpose |
|---|---|
| `main.go` | Window, story list, article view |
| `feed.go` | Hacker News and RSS fetching |
| `reader.go` | Article extraction from HTML |
| `video.go` | ffmpeg frame streaming and playback |
| `speak.go` | Text-to-speech via piper |
| `translate.go` | Translation via the Claude API |
