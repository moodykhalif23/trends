package main

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const previewParagraphs = 6

type Block struct {
	Text    string
	Image   string
	Video   string
	Poster  string // thumbnail shown before the video plays
	Heading bool
}

type Article struct {
	Title     string
	Site      string
	Lead      string
	Blocks    []Block
	Truncated bool
}

var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "nav": true,
	"header": true, "footer": true, "aside": true, "form": true,
}

type node struct {
	*html.Node
	Block
}

func Read(ctx context.Context, it Item) (Article, error) {
	a := Article{Title: it.Title, Site: publisher(it, "")}

	base, err := url.Parse(it.Link)
	if err != nil {
		return a, err
	}

	body, err := get(ctx, it.Link)
	if err != nil {
		return a, err
	}
	defer body.Close()

	doc, err := html.Parse(body)
	if err != nil {
		return a, err
	}

	var siteName string
	var blocks []node
	score := map[*html.Node]int{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if skipTags[n.Data] {
				return
			}
			switch n.Data {
			case "meta":
				switch attr(n, "property") {
				case "og:site_name":
					siteName = attr(n, "content")
				case "og:image":
					a.Lead = resolve(base, attr(n, "content"))
				}
			case "img":
				if src := imageSource(n, base); src != "" {
					blocks = append(blocks, node{n, Block{Image: src}})
				}
				return
			case "video":
				if src := videoSource(n, base); src != "" {
					b := Block{Video: src}
					// POSTER: guard the empty case — resolve(base, "") would
					// return the page URL itself, not "".
					if p := attr(n, "poster"); p != "" {
						b.Poster = resolve(base, p)
					}
					blocks = append(blocks, node{n, b})
				}
				return
			// YOUTUBE: news sites embed video as a YouTube player iframe.
			case "iframe":
				if src := attr(n, "src"); strings.Contains(src, "youtube.com/embed/") {
					blocks = append(blocks, node{n, Block{Video: resolve(base, src), Poster: youtubePoster(src)}})
				}
				return
			case "h2", "h3":
				if t := text(n); t != "" {
					blocks = append(blocks, node{n, Block{Text: t, Heading: true}})
				}
				return
			case "p":
				if t := text(n); len(t) >= 40 {
					blocks = append(blocks, node{n, Block{Text: t}})
					score[n.Parent] += len(t)
					if gp := n.Parent.Parent; gp != nil {
						score[gp] += len(t) / 2
					}
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	a.Site = publisher(it, siteName)

	var best *html.Node
	for n, s := range score {
		if best == nil || s > score[best] {
			best = n
		}
	}

	paras := 0
	for _, b := range blocks {
		if !within(b.Node, best) {
			continue
		}
		if b.Image != "" {
			if paras > 0 && b.Image != a.Lead {
				a.Blocks = append(a.Blocks, b.Block)
			}
			continue
		}

		if b.Video != "" {
			if paras > 0 {
				a.Blocks = append(a.Blocks, b.Block)
			}
			continue
		}
		if b.Heading {
			if paras > 0 && paras < previewParagraphs {
				a.Blocks = append(a.Blocks, b.Block)
			}
			continue
		}
		if paras == previewParagraphs {
			a.Truncated = true
			break
		}
		paras++
		a.Blocks = append(a.Blocks, b.Block)
	}

	if paras == 0 {
		return a, errors.New("no readable text found on this page")
	}
	return a, nil
}

func publisher(it Item, siteName string) string {
	if siteName != "" {
		return siteName
	}
	if it.Discuss == "" {
		return it.Source
	}
	if u, err := url.Parse(it.Link); err == nil {
		return strings.TrimPrefix(u.Hostname(), "www.")
	}
	return "the publisher"
}

func imageSource(n *html.Node, base *url.URL) string {
	for _, key := range []string{"data-srcset", "srcset"} {
		if v := pickSrcset(attr(n, key), 800); v != "" {
			return resolve(base, v)
		}
	}
	for _, key := range []string{"data-src", "src"} {
		if v := attr(n, key); v != "" && !strings.HasPrefix(v, "data:") {
			return resolve(base, v)
		}
	}
	return ""
}

func pickSrcset(srcset string, want int) string {
	best, bestW := "", 0
	for _, cand := range strings.Split(srcset, ", ") {
		f := strings.Fields(cand)
		if len(f) != 2 || !strings.HasSuffix(f[1], "w") {
			continue
		}

		w, _ := strconv.Atoi(strings.TrimSuffix(f[1], "w"))
		big, bestBig := w >= want, bestW >= want
		switch {
		case w == 0:
		case best == "",
			big && !bestBig,       // first one that's big enough
			big && w < bestW,      // a smaller one that's still big enough
			!bestBig && w > bestW: // nothing big enough yet: take the widest
			best, bestW = f[0], w
		}
	}
	return best
}

// <video src="..."> or <video><source src="..."></video>
func videoSource(n *html.Node, base *url.URL) string {
	if v := attr(n, "src"); v != "" {
		return resolve(base, v)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Data == "source" && attr(c, "src") != "" {
			return resolve(base, attr(c, "src"))
		}
	}
	return ""
}

// POSTER: YouTube serves a thumbnail for every video at a fixed URL, so no
// yt-dlp call is needed. strings.Cut splits once on the separator and returns
// (before, after, found) — cleaner than Split when you only want one piece.
//
//	https://www.youtube.com/embed/-Nvne3LzBls?feature=oembed
//	                              ^^^^^^^^^^^ the id
func youtubePoster(src string) string {
	_, rest, _ := strings.Cut(src, "/embed/")
	id, _, _ := strings.Cut(rest, "?")
	return "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"
}

func resolve(base *url.URL, ref string) string {
	u, err := base.Parse(ref)
	if err != nil {
		return ""
	}
	return u.String()
}

func text(n *html.Node) string {
	var b strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func within(n, ancestor *html.Node) bool {
	for ; n != nil; n = n.Parent {
		if n == ancestor {
			return true
		}
	}
	return false
}
