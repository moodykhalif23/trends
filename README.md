# Trends

A desktop news reader written in Go. Pulls stories from Hacker News and major RSS feeds, shows a short preview of each article, plays embedded video, and reads articles aloud in English or Swahili.

## Features

- Topics: Tech, Finance, Science, Medicine, Education, Kenya, Africa, World, plus free-text search
- Article preview with images and subheadings, extracted from the publisher's page
- Inline video playback, including YouTube embeds
- Listen: text-to-speech of the article
- Sikiliza: translate an article to Swahili using an OpenAI-compatible API
- Settings page for the translation API key, endpoint, model, and English voice

Stories are sorted newest-first by their published timestamps when sources provide them. Items without a readable timestamp are shown after dated stories with **Date unavailable**. This orders the stories returned by the selected feeds; feed coverage and freshness still depend on each publisher and Hacker News.

## Requirements

- Go 1.26+
- PipeWire or PulseAudio
- `aplay`
- Python 3.10–3.13 for English speech (Kokoro)

Optional. Buttons stay disabled until the tool they need is installed.

| Feature  | Needs                                                  |
| -------- | ------------------------------------------------------ |
| Video    | `ffmpeg`, `ffprobe`                                |
| YouTube  | `yt-dlp` (recent; Deno recommended)                  |
| Listen   | `aplay`, Python 3.10–3.13                            |
| Sikiliza | `aplay`, API settings                             |

English and Swahili speech use Kokoro with `af_heart` (American female) by default; choose another supported Kokoro voice in Settings. Swahili translations are spoken by the selected English voice because Kokoro does not include a Swahili voice. Trends applies pronunciation hints to common Kenyan names, including Kisii. On first use, Trends creates an isolated Python environment and downloads the Kokoro runtime plus approximately 142 MB of model files. This requires Python 3.10–3.13 and an internet connection.

The app prepares and warms the speech engine in the background at startup, then keeps it running while the app is open. Pausing and resuming does not reload the model.

## Settings

Open **Settings** in the app to enter an OpenAI-compatible API key, base URL, model, and Kokoro voice. Settings are saved in the operating system's per-user configuration directory, separate from the application executable; the key is stored locally in a user-only settings file and is not encrypted by the app.

Existing `.env` values for `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`, and `KOKORO_VOICE` seed the settings only until a settings file is saved. After that, the saved settings take precedence.

## Run

```
go run .
```
