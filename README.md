# Trends

A desktop news reader written in Go. Pulls stories from Hacker News and major RSS feeds, shows a short preview of each article, plays embedded video, and reads articles aloud in English or Swahili.

## Features

- Topics: Tech, Finance, Science, Medicine, Education, Kenya, Africa, World, plus free-text search
- Article preview with images and subheadings, extracted from the publisher's page
- Inline video playback, including YouTube embeds
- Listen: text-to-speech of the article
- Sikiliza: the article translated to Swahili by an LLM(use your any you prefer bby adding OpenAI-compatible API key in`.env`  )

## Requirements

- Go 1.26+
- PipeWire or PulseAudio

Optional. Buttons stay disabled until the tool they need is installed.

| Feature  | Needs                                                  |
| -------- | ------------------------------------------------------ |
| Video    | `ffmpeg`, `ffprobe`                                |
| YouTube  | `yt-dlp` (recent; Deno recommended)                  |
| Listen   | `piper`, `aplay`, English voice                    |

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
