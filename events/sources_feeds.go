package events

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func feedName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	h := strings.TrimPrefix(u.Hostname(), "www.")
	switch {
	case strings.Contains(h, "coindesk"):
		return "CoinDesk"
	case strings.Contains(h, "theblock"):
		return "The Block"
	case strings.Contains(h, "decrypt"):
		return "Decrypt"
	case strings.Contains(h, "cointelegraph"):
		return "Cointelegraph"
	}
	return h
}

type rssDoc struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
	Entries []struct {
		Title   string `xml:"title"`
		Updated string `xml:"updated"`
		Link    struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Summary string `xml:"summary"`
	} `xml:"entry"`
}

func parseRSSDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 02 Jan 2006 15:04:05 MST", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func (f *Feed) rssEvents(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	var out []Event
	var warns []string
	for _, feed := range f.rssFeeds {
		key := "rss:" + feed
		if e, ok := f.extraGet(key, 2*time.Minute, now); ok {
			out = append(out, e.events...)
			continue
		}
		body, err := f.getText(ctx, feed)
		if err != nil {
			warns = append(warns, "RSS "+feedName(feed)+" 不可用: "+err.Error())
			f.extraPut(key, extraEntry{at: now, warnings: []string{"RSS " + feedName(feed) + " 不可用"}})
			continue
		}
		var doc rssDoc
		if xml.Unmarshal([]byte(body), &doc) != nil {
			continue
		}
		name := feedName(feed)
		var evs []Event
		handle := func(title, link, summary, dateStr string) {
			pub := parseRSSDate(dateStr)
			if pub.IsZero() || pub.Before(now.Add(-7*24*time.Hour)) || pub.After(now.Add(10*time.Minute)) {
				return
			}
			text := title + " " + plainText(summary, 2000)
			matched := []string{}
			traders := []TraderRef{}
			for _, coin := range coins {
				if articleMatches(coin, text, false) {
					matched = append(matched, coin)
					for _, ref := range tradersByCoin[coin] {
						traders = appendRef(traders, ref)
					}
				}
			}
			if len(matched) == 0 {
				return
			}
			evs = append(evs, Event{
				ID: "rss:" + shortID(name, link, title), Kind: "news", Source: name,
				Title: truncateRunes(plainText(title, 300), 200), Summary: plainText(summary, 1200),
				URL: articleURL(link), Publisher: name, PublishedAt: pub, ReceivedAt: now,
				Markets: []Market{}, Coins: matched, Traders: traders,
			})
		}
		for _, it := range doc.Channel.Items {
			handle(it.Title, it.Link, it.Description, it.PubDate)
		}
		for _, e := range doc.Entries {
			handle(e.Title, e.Link.Href, e.Summary, e.Updated)
		}
		f.extraPut(key, extraEntry{at: now, events: evs})
		out = append(out, evs...)
	}
	return out, warns
}

// ---------- Exchange announcements (OKX / Bybit, free public endpoints) ----------

func (f *Feed) okxAnnouncements(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if e, ok := f.extraGet("okx_ann", 5*time.Minute, now); ok {
		return e.events, e.warnings
	}
	var resp struct {
		Data []struct {
			Details []struct {
				AnnType string `json:"annType"`
				Title   string `json:"title"`
				URL     string `json:"url"`
				PTime   string `json:"pTime"`
			} `json:"details"`
		} `json:"data"`
	}
	if err := f.getJSONHeaders(ctx, "https://www.okx.com/api/v5/support/announcements", map[string]string{"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36", "Accept": "application/json"}, &resp); err != nil {
		w := []string{"OKX 公告不可用: " + err.Error()}
		f.extraPut("okx_ann", extraEntry{at: now, warnings: w})
		return nil, w
	}
	var evs []Event
	for _, grp := range resp.Data {
		for _, d := range grp.Details {
			ms := 0
			fmt.Sscanf(d.PTime, "%d", &ms)
			pub := time.UnixMilli(int64(ms)).UTC()
			evs = append(evs, announcementEvent("OKX", d.Title, d.URL, d.AnnType, pub, now, coins, tradersByCoin)...)
		}
	}
	f.extraPut("okx_ann", extraEntry{at: now, events: evs})
	return evs, nil
}

func (f *Feed) bybitAnnouncements(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if e, ok := f.extraGet("bybit_ann", 5*time.Minute, now); ok {
		return e.events, e.warnings
	}
	var resp struct {
		Result struct {
			List []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				URL         string `json:"url"`
				StartDate   string `json:"startDate"`
				Type        struct {
					Key   string `json:"key"`
					Title string `json:"title"`
				} `json:"type"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := f.getJSONHeaders(ctx, "https://api.bybit.com/v5/announcements/index?locale=en-US&limit=20", map[string]string{"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36", "Accept": "application/json"}, &resp); err != nil {
		w := []string{"Bybit 公告不可用: " + err.Error()}
		f.extraPut("bybit_ann", extraEntry{at: now, warnings: w})
		return nil, w
	}
	var evs []Event
	for _, d := range resp.Result.List {
		pub := parseTime(d.StartDate)
		kind := "announcement"
		if strings.Contains(strings.ToLower(d.Type.Key), "listing") || strings.Contains(strings.ToLower(d.Title), "will list") {
			kind = "listing"
		}
		evs = append(evs, announcementEvent("Bybit", d.Title, d.URL, kind, pub, now, coins, tradersByCoin)...)
	}
	f.extraPut("bybit_ann", extraEntry{at: now, events: evs})
	return evs, nil
}

func announcementEvent(source, title, link, typ string, pub, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) []Event {
	text := title
	matched := []string{}
	traders := []TraderRef{}
	for _, coin := range coins {
		if matches(coin, text) {
			matched = append(matched, coin)
			for _, ref := range tradersByCoin[coin] {
				traders = appendRef(traders, ref)
			}
		}
	}
	if len(matched) == 0 {
		return nil
	}
	if pub.IsZero() {
		pub = now
	}
	kind := "announcement"
	if strings.Contains(strings.ToLower(typ), "listing") || strings.Contains(strings.ToLower(title), "will list") || strings.Contains(title, "上线") {
		kind = "listing"
	}
	return []Event{{
		ID: source + "-ann:" + shortID(source, title, link), Kind: kind, Source: source,
		Title: truncateRunes(title, 200), Summary: "", URL: articleURL(link), Publisher: source,
		PublishedAt: pub, ReceivedAt: now, Markets: []Market{}, Coins: matched, Traders: traders,
	}}
}

// ---------- CryptoPanic (free key optional) ----------

func (f *Feed) cryptoPanicEvents(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if f.cryptopanicKey == "" {
		return nil, nil
	}
	if e, ok := f.extraGet("cryptopanic", 5*time.Minute, now); ok {
		return e.events, e.warnings
	}
	u := "https://cryptopanic.com/api/v1/posts/?auth_token=" + url.QueryEscape(f.cryptopanicKey) + "&public=true&kind=news"
	var resp struct {
		Results []struct {
			Title      string `json:"title"`
			PublishedAt string `json:"published_at"`
			URL        string `json:"url"`
			Currencies []struct {
				Code string `json:"code"`
			} `json:"currencies"`
		} `json:"results"`
	}
	if err := f.getJSONHeaders(ctx, u, map[string]string{"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36", "Accept": "application/json"}, &resp); err != nil {
		w := []string{"CryptoPanic 不可用: " + err.Error()}
		f.extraPut("cryptopanic", extraEntry{at: now, warnings: w})
		return nil, w
	}
	scope := map[string]bool{}
	for _, c := range coins {
		scope[strings.ToUpper(c)] = true
	}
	var evs []Event
	for _, r := range resp.Results {
		matched := []string{}
		traders := []TraderRef{}
		for _, cur := range r.Currencies {
			code := strings.ToUpper(strings.TrimSpace(cur.Code))
			if scope[code] {
				matched = append(matched, code)
				for _, ref := range tradersByCoin[code] {
					traders = appendRef(traders, ref)
				}
			}
		}
		if len(matched) == 0 {
			continue
		}
		evs = append(evs, Event{
			ID: "cryptopanic:" + shortID(r.URL, r.Title), Kind: "news", Source: "CryptoPanic",
			Title: truncateRunes(r.Title, 200), URL: articleURL(r.URL), Publisher: "CryptoPanic",
			PublishedAt: parseTime(r.PublishedAt), ReceivedAt: now,
			Markets: []Market{}, Coins: matched, Traders: traders,
		})
	}
	f.extraPut("cryptopanic", extraEntry{at: now, events: evs})
	return evs, nil
}
