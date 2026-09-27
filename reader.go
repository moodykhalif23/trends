package main

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const previewParagraphs = 6

type Block struct {
	Text  string
	Image string
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
	for _, key := range []string{"data-src", "src"} {
		if v := attr(n, key); v != "" && !strings.HasPrefix(v, "data:") {
			return resolve(base, v)
		}
	}
	return ""
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
