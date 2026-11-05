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
| Listen   | `piper`, `aplay`                                   |

Voices go in `~/.local/share/piper/`:

```
sw_CD-lanfrica-medium.onnx (+ .onnx.json)
```

Download from https://huggingface.co/rhasspy/piper-voices.
The first time you use Listen, Trends downloads the 114 MB English Lessac high-quality voice and its config to `~/.local/share/piper/`. It verifies each file before installing it and reuses them on later runs. This requires an internet connection once. See the [Lessac high model card](https://huggingface.co/rhasspy/piper-voices/tree/v1.0.0/en/en_US/lessac/high) and [Piper voice samples](https://rhasspy.github.io/piper-samples/).
Set `PIPER_EN_VOICE` in `.env` to use an already-installed English Piper model instead.

## Run

```
go run .
```
