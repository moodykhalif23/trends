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
	"runtime"
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

	var items []Item
	var home fyne.CanvasObject
	status := widget.NewLabel("")

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
		w.SetContent(reader(a, items[id], func() { w.SetContent(home) }))
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

	bar := container.NewBorder(nil, nil, topic, container.NewHBox(refresh, toggle), search)
	home = container.NewBorder(bar, status, nil, nil, list)
	w.SetContent(home)

	topic.SetSelected(Topics[0])
	w.ShowAndRun()
}

func reader(a fyne.App, it Item, back func()) fyne.CanvasObject {
	ctx, cancel := context.WithCancel(context.Background())
	content := container.NewVBox(richText(heading(it.Title), para("Loading…")))

	visit := widget.NewButtonWithIcon("Read the full story on the publisher's site", theme.MailForwardIcon(), func() { open(a, it.Link) })
	visit.Importance = widget.HighImportance
	var art Article
	en := &speaker{lang: "en"}
	listen := toggleButton(ctx, "Listen", theme.VolumeUpIcon(), "Pause", theme.MediaPauseIcon(), resumable(func(c context.Context, from int) (int, error) {
		if en.lines == nil {
			en.lines = art.Lines()
		}
		return en.play(c, from)
	}))
	sw := &speaker{lang: "sw"}
	sikiliza := toggleButton(ctx, "Sikiliza", theme.VolumeUpIcon(), "Pause", theme.MediaPauseIcon(), resumable(func(c context.Context, from int) (int, error) {
		if sw.lines == nil {
			text, err := translate(c, strings.Join(art.Lines(), "\n"), "Swahili")
			if err != nil {
				return 0, err
			}
			sw.lines = strings.Split(strings.TrimSpace(text), "\n")
		}
		return sw.play(c, from)
	}))
	listen.Disable()
	sikiliza.Disable()
	actions := container.NewHBox(visit, listen, sikiliza)
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
				enableIf(listen, speakNeeds("en"))
				enableIf(sikiliza, cmp.Or(speakNeeds("sw"), translateNeeds()))
				content.Objects = articleView(ctx, art, content.Refresh)
			}
			content.Refresh()
		})
	}()

	top := container.NewHBox(widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), func() {
		cancel()
		log.Println("goroutines:", runtime.NumGoroutine())
		back()
	}))
	return container.NewBorder(top, container.NewPadded(actions), nil, nil, container.NewVScroll(container.NewPadded(content)))
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
func toggleButton(ctx context.Context, label string, icon fyne.Resource, busy string, busyIcon fyne.Resource, run func(context.Context) error) *widget.Button {
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
			if err := run(cctx); err != nil && cctx.Err() == nil {
				log.Printf("%s FAIL err=%v", label, err)
			}
			cancel()
			fyne.Do(func() {
				stop = nil
				btn.SetText(label)
				btn.SetIcon(icon)
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
	if it.Discuss != "" {
		return fmt.Sprintf("Hacker News · %d points · %d comments", it.Points, it.Comments)
	}
	return it.Source
}
