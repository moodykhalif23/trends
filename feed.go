package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Item struct {
	Title       string
	Link        string
	Source      string
	PublishedAt time.Time
	Points      int
	Comments    int
	Discuss     string
}

type Feed struct{ Name, URL string }

var Topics = []string{"Tech", "Finance", "Science", "Medicine", "Education", "Kenya", "Africa", "World"}

var feeds = map[string][]Feed{
	"Tech": {
		{"Ars Technica", "https://feeds.arstechnica.com/arstechnica/index"},
		{"TechCrunch", "https://techcrunch.com/feed/"},
		{"The Guardian", "https://www.theguardian.com/technology/rss"},
		{"BBC News", "https://feeds.bbci.co.uk/news/technology/rss.xml"},
	},
	"Finance": {
		{"CNBC", "https://www.cnbc.com/id/100003114/device/rss/rss.html"},
		{"BBC News", "https://feeds.bbci.co.uk/news/business/rss.xml"},
		{"The Guardian", "https://www.theguardian.com/business/rss"},
	},
	"Science": {
		{"ScienceDaily", "https://www.sciencedaily.com/rss/top/science.xml"},
		{"Quanta Magazine", "https://www.quantamagazine.org/feed/"},
		{"BBC News", "https://feeds.bbci.co.uk/news/science_and_environment/rss.xml"},
	},
	"Medicine": {
		{"STAT", "https://www.statnews.com/feed/"},
		{"ScienceDaily", "https://www.sciencedaily.com/rss/health_medicine.xml"},
		{"BBC News", "https://feeds.bbci.co.uk/news/health/rss.xml"},
	},
	"Education": {
		{"The Guardian", "https://www.theguardian.com/education/rss"},
		{"NPR", "https://feeds.npr.org/1013/rss.xml"},
		{"EdSurge", "https://www.edsurge.com/articles_rss"},
	},
	"Kenya": {
		{"The Standard", "https://www.standardmedia.co.ke/rss/kenya.php"},
		{"Capital FM Kenya", "https://www.capitalfm.co.ke/news/feed/"},
		{"KBC", "https://www.kbc.co.ke/feed/"},
	},
	"Africa": {
		{"BBC News Africa", "https://feeds.bbci.co.uk/news/world/africa/rss.xml"},
		{"Africanews", "https://www.africanews.com/feed/rss"},
	},
	"World": {
		{"BBC News", "https://feeds.bbci.co.uk/news/world/rss.xml"},
		{"The Guardian", "https://www.theguardian.com/world/rss"},
		{"NPR", "https://feeds.npr.org/1004/rss.xml"},
		{"Al Jazeera", "https://www.aljazeera.com/xml/rss/all.xml"},
		{"BBC News Africa", "https://feeds.bbci.co.uk/news/world/africa/rss.xml"},
		{"Africanews", "https://www.africanews.com/feed/rss"},
	},
}

var hnQueries = map[string]string{
	"Finance":   "finance markets economy stocks",
	"Science":   "science research physics biology",
	"Medicine":  "medicine health medical drug",
	"Education": "education school university learning",
	"World":     "world government war election",
}

const userAgent = "Trends/0.2 (personal news reader)"

var client = &http.Client{Timeout: 15 * time.Second}

func Fetch(topic, search string) ([]Item, error) {
	search = strings.TrimSpace(search)
	sources, hnQuery := feeds[topic], hnQueries[topic]
	includeHN := topic != "Kenya" && topic != "Africa"
	if search != "" {
		sources, hnQuery = allFeeds(), search
		includeHN = true
	}

	lists := make([][]Item, len(sources)+1)
	errs := make([]error, len(lists))
	var wg sync.WaitGroup
	if includeHN {
		wg.Go(func() { lists[0], errs[0] = hackerNews(hnQuery) })
	}
	for i, f := range sources {
		wg.Go(func() { lists[i+1], errs[i+1] = rss(f) })
	}
	wg.Wait()

	if search != "" {
		for i := 1; i < len(lists); i++ {
			lists[i] = matching(lists[i], search)
		}
	}

	items := interleave(lists...)
	sortByPublishedAt(items)
	if len(items) == 0 {
		return nil, errors.Join(errs...)
	}
	return items, nil
}

func allFeeds() []Feed {
	seen := map[string]bool{}
	var all []Feed
	for _, t := range Topics {
		for _, f := range feeds[t] {
			if !seen[f.URL] {
				seen[f.URL] = true
				all = append(all, f)
			}
		}
	}
	return all
}

func matching(items []Item, q string) []Item {
	q = strings.ToLower(q)
	var out []Item
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Title), q) {
			out = append(out, it)
		}
	}
	return out
}

func hackerNews(q string) ([]Item, error) {
	u := "https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage=30"
	if q != "" {
		v := url.Values{
			"tags":           {"story"},
			"hitsPerPage":    {"30"},
			"query":          {q},
			"optionalWords":  {q},
			"numericFilters": {fmt.Sprintf("created_at_i>%d", time.Now().AddDate(0, 0, -7).Unix())},
		}
		u = "https://hn.algolia.com/api/v1/search?" + v.Encode()
	}

	var res struct {
		Hits []struct {
			ID            string `json:"objectID"`
			Title         string `json:"title"`
			URL           string `json:"url"`
			CreatedAt     string `json:"created_at"`
			CreatedAtUnix int64  `json:"created_at_i"`
			Points        int    `json:"points"`
			Comments      int    `json:"num_comments"`
		} `json:"hits"`
	}
	if err := getJSON(u, &res); err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(res.Hits))
	for _, h := range res.Hits {
		discuss := "https://news.ycombinator.com/item?id=" + h.ID
		link := h.URL
		if link == "" {
			link = discuss
		}
		publishedAt := time.Time{}
		if h.CreatedAtUnix > 0 {
			publishedAt = time.Unix(h.CreatedAtUnix, 0)
		} else if parsed, err := time.Parse(time.RFC3339, h.CreatedAt); err == nil {
			publishedAt = parsed
		}
		items = append(items, Item{
			Title:       h.Title,
			Link:        link,
			Source:      "Hacker News",
			PublishedAt: publishedAt,
			Points:      h.Points,
			Comments:    h.Comments,
			Discuss:     discuss,
		})
	}
	return items, nil
}

func rss(f Feed) ([]Item, error) {
	body, err := get(context.Background(), f.URL)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	var doc struct {
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			PubDate   string `xml:"pubDate"`
			Date      string `xml:"http://purl.org/dc/elements/1.1/ date"`
			Published string `xml:"published"`
			Updated   string `xml:"updated"`
		} `xml:"channel>item"`
	}
	if err := xml.NewDecoder(body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}

	items := make([]Item, 0, len(doc.Items))
	for _, r := range doc.Items {
		items = append(items, Item{
			Title:       strings.TrimSpace(r.Title),
			Link:        strings.TrimSpace(r.Link),
			Source:      f.Name,
			PublishedAt: parseRSSDate(r.PubDate, r.Date, r.Published, r.Updated),
		})
	}
	return items, nil
}

func parseRSSDate(values ...string) time.Time {
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 2 Jan 2006 15:04:05 MST",
		"Mon, 02 Jan 2006 15:04 MST",
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		for _, layout := range layouts {
			if publishedAt, err := time.Parse(layout, value); err == nil {
				return publishedAt
			}
		}
	}
	return time.Time{}
}

func sortByPublishedAt(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].PublishedAt, items[j].PublishedAt
		switch {
		case left.IsZero():
			return false
		case right.IsZero():
			return true
		default:
			return left.After(right)
		}
	})
}
func get(ctx context.Context, u string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return resp.Body, nil
}

func getJSON(u string, v any) error {
	body, err := get(context.Background(), u)
	if err != nil {
		return err
	}
	defer body.Close()
	return json.NewDecoder(body).Decode(v)
}

func interleave(lists ...[]Item) []Item {
	var out []Item
	for i := 0; ; i++ {
		added := false
		for _, l := range lists {
			if i < len(l) {
				out = append(out, l[i])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}
