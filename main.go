package main

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	_ "golang.org/x/image/webp"
)

type fixedTheme struct {
	fyne.Theme
	variant fyne.ThemeVariant
}

func (t *fixedTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return t.Theme.Color(n, t.variant)
}

func themeFor(dark bool) fyne.Theme {
	if dark {
		return &fixedTheme{theme.DefaultTheme(), theme.VariantDark}
	}
	return &fixedTheme{theme.DefaultTheme(), theme.VariantLight}
}

func main() {
	loadEnv(".env")
	a := app.New()
	dark := false
	a.Settings().SetTheme(themeFor(dark))

	w := a.NewWindow("Trends")
	w.Resize(fyne.NewSize(900, 650))
	settings, err := openSettings()
	if err != nil {
		log.Printf("settings FAIL err=%v", err)
		w.SetContent(container.NewCenter(widget.NewLabel("Could not load settings: " + err.Error())))
		w.ShowAndRun()
		return
	}
	kokoro := newKokoroRuntime()
	defer func() {
		if err := kokoro.close(); err != nil {
			log.Printf("Kokoro shutdown FAIL err=%v", err)
		}
	}()

	var items []Item
	var home fyne.CanvasObject
	status := widget.NewLabel("")
	speechStatus := widget.NewLabel("Preparing speech engine…")
	go func() {
		err := kokoro.prepare(kokoro.ctx, settings.snapshot().KokoroVoice)
		fyne.Do(func() {
			if err != nil {
				speechStatus.SetText("Speech engine unavailable: " + err.Error())
			} else {
				speechStatus.SetText("Speech engine ready")
			}
		})
	}()

	list := widget.NewList(
		func() int { return len(items) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.TextStyle.Bold = true
			title.Wrapping = fyne.TextWrapWord
			return container.NewVBox(title, widget.NewLabel(""))
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			it := items[id]
			box := o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(it.Title)
			box.Objects[1].(*widget.Label).SetText(meta(it))
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		w.SetContent(reader(a, items[id], settings, kokoro, func() { w.SetContent(home) }))
		list.UnselectAll()
	}

	topic := widget.NewSelect(Topics, nil)
	search := widget.NewEntry()
	search.SetPlaceHolder("Search anything…")

	load := func() {
		status.SetText("Loading…")
		go func() {
			res, err := Fetch(topic.Selected, search.Text)
			fyne.Do(func() {
				if err != nil {
					status.SetText("Error: " + err.Error())
					return
				}
				items = res
				list.Refresh()
				list.ScrollToTop()
				status.SetText(fmt.Sprintf("%d stories", len(items)))
			})
		}()
	}

	topic.OnChanged = func(string) { search.SetText(""); load() }
	search.OnSubmitted = func(string) { load() }

	toggle := widget.NewButtonWithIcon("", theme.ColorPaletteIcon(), func() {
		dark = !dark
		a.Settings().SetTheme(themeFor(dark))
	})
	refresh := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), load)
	settingsButton := widget.NewButton("Settings", func() {
		w.SetContent(settingsView(settings, kokoro, func() { w.SetContent(home) }))
	})

	bar := container.NewBorder(nil, nil, topic, container.NewHBox(settingsButton, refresh, toggle), search)
	home = container.NewBorder(bar, container.NewVBox(status, speechStatus), nil, nil, list)
	w.SetContent(home)

	topic.SetSelected(Topics[0])
	w.ShowAndRun()
}

func reader(a fyne.App, it Item, settings *settingsStore, kokoro *kokoroRuntime, back func()) fyne.CanvasObject {
	ctx, cancel := context.WithCancel(context.Background())
	content := container.NewVBox(richText(heading(it.Title), para("Loading…")))

	visit := widget.NewButtonWithIcon("Read the full story on the publisher's site", theme.MailForwardIcon(), func() { open(a, it.Link) })
	visit.Importance = widget.HighImportance
	var art Article
	voice := settings.snapshot().KokoroVoice
	en := &speaker{lang: "en", voice: voice, engine: kokoro}
	speechStatus := widget.NewLabel("")
	speechStatus.Wrapping = fyne.TextWrapWord
	listen := toggleButton(ctx, "Listen", theme.VolumeUpIcon(), "Pause", theme.MediaPauseIcon(), resumable(func(c context.Context, from int) (int, error) {
		if en.lines == nil {
			en.lines = art.Lines()
		}
		return en.play(c, from)
	}), func(err error) {
		if err == nil {
			speechStatus.SetText("")
		} else {
			speechStatus.SetText("Listen failed: " + err.Error())
		}
	})
	sw := &speaker{lang: "sw", voice: voice, engine: kokoro}
	sikiliza := toggleButton(ctx, "Sikiliza", theme.VolumeUpIcon(), "Pause", theme.MediaPauseIcon(), resumable(func(c context.Context, from int) (int, error) {
		if sw.lines == nil {
			text, err := translate(c, strings.Join(art.Lines(), "\n"), "Swahili", settings.snapshot())
			if err != nil {
				return 0, err
			}
			sw.lines = strings.Split(strings.TrimSpace(text), "\n")
		}
		return sw.play(c, from)
	}), func(err error) {
		if err == nil {
			speechStatus.SetText("")
		} else {
			speechStatus.SetText("Sikiliza failed: " + err.Error())
		}
	})
	listen.Disable()
	sikiliza.Disable()
	articleReady, engineReady := false, false
	updateSpeechButtons := func() {
		if !articleReady {
			return
		}
		if !engineReady {
			listen.SetText("Preparing voice…")
			sikiliza.SetText("Preparing voice…")
			return
		}
		enableIf(listen, speakNeeds())
		enableIf(sikiliza, cmp.Or(speakNeeds(), translateNeeds(settings.snapshot())))
	}
	actions := container.NewVBox(container.NewHBox(visit, listen, sikiliza), speechStatus)
	if it.Discuss != "" {
		actions.Add(widget.NewButton("Discuss on Hacker News", func() { open(a, it.Discuss) }))
	}

	go func() {
		res, err := Read(ctx, it)
		fyne.Do(func() {
			visit.SetText("Continue reading at " + res.Site)
			if err != nil {
				content.Objects = []fyne.CanvasObject{richText(heading(it.Title), para("Preview unavailable: "+err.Error()))}
			} else {
				art = res
				articleReady = true
				updateSpeechButtons()
				content.Objects = articleView(ctx, art, content.Refresh)
			}
			content.Refresh()
		})
	}()
	go func() {
		err := kokoro.prepare(ctx, voice)
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				speechStatus.SetText("Speech engine setup failed: " + err.Error())
				return
			}
			engineReady = true
			updateSpeechButtons()
		})
	}()

	top := container.NewHBox(widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), func() {
		cancel()
		back()
	}))
	return container.NewBorder(top, container.NewPadded(actions), nil, nil, container.NewVScroll(container.NewPadded(content)))
}

func settingsView(settings *settingsStore, kokoro *kokoroRuntime, back func()) fyne.CanvasObject {
	current := settings.snapshot()
	key := widget.NewPasswordEntry()
	key.SetPlaceHolder("Paste an OpenAI-compatible API key")
	key.SetText(current.OpenAIAPIKey)
	baseURL := widget.NewEntry()
	baseURL.SetText(current.OpenAIBaseURL)
	baseURL.SetPlaceHolder(defaultOpenAIBaseURL)
	model := widget.NewEntry()
	model.SetText(current.OpenAIModel)
	model.SetPlaceHolder("Provider model name")
	voice := widget.NewSelect(kokoroVoices, nil)
	voice.SetSelected(current.KokoroVoice)
	status := widget.NewLabel("")

	save := widget.NewButton("Save settings", func() {
		value := userSettings{
			OpenAIAPIKey:  key.Text,
			OpenAIBaseURL: baseURL.Text,
			OpenAIModel:   model.Text,
			KokoroVoice:   voice.Selected,
		}
		err := settings.save(value)
		if err != nil {
			status.SetText("Could not save settings: " + err.Error())
			return
		}
		status.SetText("Settings saved. Preparing selected voice…")
		go func() {
			err := kokoro.prepare(context.Background(), value.KokoroVoice)
			fyne.Do(func() {
				if err != nil {
					status.SetText("Settings saved, but voice preparation failed: " + err.Error())
					return
				}
				status.SetText("Settings saved. Voice is ready.")
			})
		}()
	})
	save.Importance = widget.HighImportance

	form := widget.NewForm(
		widget.NewFormItem("API key", key),
		widget.NewFormItem("API base URL", baseURL),
		widget.NewFormItem("Model", model),
		widget.NewFormItem("English voice", voice),
	)
	description := widget.NewLabel("English and Swahili speech use Kokoro. The default is af_heart (American female); Swahili text is spoken with the selected English voice. First use downloads about 142 MB of model files and installs the Kokoro runtime; Python 3.10–3.13 and an internet connection are required.")
	description.Wrapping = fyne.TextWrapWord
	privacy := widget.NewLabel("The API key is stored locally in your user settings file with restricted permissions. Leave the key blank to remove it.")
	privacy.Wrapping = fyne.TextWrapWord
	top := container.NewHBox(widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), back))
	body := container.NewVBox(description, form, privacy, save, status)
	return container.NewBorder(top, nil, nil, nil, container.NewVScroll(container.NewPadded(body)))
}

func articleView(ctx context.Context, art Article, refresh func()) []fyne.CanvasObject {
	objs := []fyne.CanvasObject{richText(heading(art.Title)), richText(emphasis("Published by " + art.Site))}
	if art.Lead != "" {
		objs = append(objs, remoteImage(ctx, art.Lead, refresh))
	}
	for _, b := range art.Blocks {
		switch {
		case b.Image != "":
			objs = append(objs, remoteImage(ctx, b.Image, refresh))
		case b.Video != "":
			objs = append(objs, remoteVideo(ctx, b.Video, b.Poster))
		case b.Heading:
			objs = append(objs, richText(heading(b.Text)))
		default:
			objs = append(objs, richText(para(b.Text)))
		}
	}
	if art.Truncated {
		objs = append(objs, richText(emphasis("This is a short preview. The full article belongs to "+art.Site+" — please continue reading on their site.")))
	}
	return objs
}

func fetchImage(ctx context.Context, link string) (image.Image, error) {
	start := time.Now()
	body, err := get(ctx, link)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	m, _, err := image.Decode(body)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	b := m.Bounds()
	log.Printf("image %v  %dx%d  %s", time.Since(start), b.Dx(), b.Dy(), link)
	return m, nil
}

func remoteImage(ctx context.Context, link string, refresh func()) *canvas.Image {
	img := &canvas.Image{FillMode: canvas.ImageFillContain}
	img.Hide()
	go func() {
		m, err := fetchImage(ctx, link)
		if err != nil {
			log.Printf("image FAIL %s  err=%v", link, err)
			return
		}
		b := m.Bounds()
		if b.Dx() < 200 || ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			img.Image = m
			img.SetMinSize(fyne.NewSize(0, min(400, 600*float32(b.Dy())/float32(b.Dx()))))
			img.Show()
			refresh()
		})
	}()

	return img
}

func loadEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fromFile := map[string]bool{}
	for line := range strings.Lines(string(data)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		k = strings.TrimSpace(k)
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if _, set := os.LookupEnv(k); set && !fromFile[k] {
			continue
		}
		fromFile[k] = true // within the file, the last line wins
		os.Setenv(k, strings.Trim(strings.TrimSpace(v), `"'`))
	}
}

// resumable wraps play so that after a pause the next click continues where it stopped.
func resumable[T int | time.Duration](play func(context.Context, T) (T, error)) func(context.Context) error {
	var pos T
	return func(c context.Context) error {
		n, err := play(c, pos)
		if c.Err() != nil {
			pos += n
		} else {
			pos = 0
		}
		return err
	}
}

// missing returns the first tool not found on PATH, or "".
func missing(tools ...string) string {
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			return t
		}
	}
	return ""
}

func enableIf(b *widget.Button, reason string) {
	if reason == "" {
		b.Enable()
	} else {
		b.SetText(reason)
	}
}

// toggleButton runs `run` in the background on click and shows the busy label until it returns.
func toggleButton(ctx context.Context, label string, icon fyne.Resource, busy string, busyIcon fyne.Resource, run func(context.Context) error, onResult func(error)) *widget.Button {
	var btn *widget.Button
	var stop context.CancelFunc
	btn = widget.NewButtonWithIcon(label, icon, func() {
		if stop != nil {
			stop()
			return
		}
		cctx, cancel := context.WithCancel(ctx)
		stop = cancel
		btn.SetText(busy)
		btn.SetIcon(busyIcon)
		go func() {
			err := run(cctx)
			if err != nil && cctx.Err() == nil {
				log.Printf("%s FAIL err=%v", label, err)
			}
			cancelled := cctx.Err() != nil
			cancel()
			fyne.Do(func() {
				stop = nil
				btn.SetText(label)
				btn.SetIcon(icon)
				if cancelled {
					onResult(nil)
				} else {
					onResult(err)
				}
			})
		}()
	})
	return btn
}

func richText(segs ...widget.RichTextSegment) *widget.RichText {
	rt := widget.NewRichText(segs...)
	rt.Wrapping = fyne.TextWrapWord
	return rt
}

func emphasis(s string) widget.RichTextSegment {
	return &widget.TextSegment{Text: s, Style: widget.RichTextStyleEmphasis}
}

func heading(s string) widget.RichTextSegment {
	return &widget.TextSegment{Text: s, Style: widget.RichTextStyleHeading}
}

func para(s string) widget.RichTextSegment {
	return &widget.TextSegment{Text: s, Style: widget.RichTextStyleParagraph}
}

func open(a fyne.App, link string) {
	if u, err := url.Parse(link); err == nil {
		a.OpenURL(u)
	}
}

func meta(it Item) string {
	source := it.Source
	if it.Discuss != "" {
		source = fmt.Sprintf("Hacker News · %d points · %d comments", it.Points, it.Comments)
	}
	if it.PublishedAt.IsZero() {
		return source + " · Date unavailable"
	}
	return source + " · " + it.PublishedAt.Local().Format("Jan 2, 2006 3:04 PM")
}
