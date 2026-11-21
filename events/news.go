package events

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Article URLs are display-only: never fetch them, resolve them, or follow redirects.
// Canonicalization deduplicates news articles, not unrelated prediction contracts
// or HN discussions (which remain separate signals even for the same article).
func articleURL(raw string) string {
	if len(raw) > 4096 {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !strings.Contains(host, ".") || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()) {
		return ""
	}
	if strings.ContainsAny(host, "\\% ") {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") || strings.EqualFold(k, "fbclid") || strings.EqualFold(k, "gclid") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Tokenization removes markup, comments, scripts and styles; entities are decoded
// by x/net/html. Both provider input and emitted plain text are bounded.
func plainText(raw string, limit int) string {
	z := html.NewTokenizer(strings.NewReader(bounded(raw, 16000)))
	var b strings.Builder
	skip := ""
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		token := z.Token()
		if tt == html.StartTagToken && (token.Data == "script" || token.Data == "style") {
			skip = token.Data
		}
		if tt == html.EndTagToken && token.Data == skip {
			skip = ""
			continue
		}
		if skip != "" {
			continue
		}
		if tt == html.TextToken {
			b.WriteString(token.Data)
			b.WriteByte(' ')
		}
		if b.Len() > 32000 {
			break
		}
	}
	return bounded(strings.Join(strings.Fields(b.String()), " "), limit)
}

var cryptoContext = regexp.MustCompile(`(?i)\b(crypto|cryptocurrency|blockchain|token|tokens|coin|coins|defi|mainnet|staking|airdrop)\b`)
var ambiguousCoins = map[string]bool{"OP": true, "NEAR": true, "LINK": true, "DOT": true, "TON": true, "UNI": true, "APT": true, "HYPE": true, "SUI": true, "PEPE": true, "ADA": true, "SOL": true, "AVAX": true, "TAO": true}

func articleMatches(coin, text string, hn bool) bool {
	text = bounded(text, 4000)
	for _, term := range terms(coin) {
		// Ambiguous ticker words need crypto context; HN additionally requires the
		// longer project name. Unknown short tickers are never inferred from AI news.
		ambiguous := ambiguousCoins[coin] || (len(terms(coin)) == 1 && len(coin) <= 4)
		if ambiguous {
			if strings.EqualFold(term, coin) {
				if hn || !cryptoContext.MatchString(text) {
					continue
				}
			} else if (term == "Optimism" || term == "Avalanche" || term == "Pepe") && !cryptoContext.MatchString(text) {
				continue
			}
		}
		re := regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(term) + `($|[^\p{L}\p{N}])`)
		if re.MatchString(text) {
			return true
		}
	}
	return false
}
func recentArticle(t, now time.Time) bool {
	return !t.IsZero() && !t.Before(now.Add(-7*24*time.Hour)) && !t.After(now.Add(10*time.Minute))
}

type newsArticle struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	Description string `json:"description"`
	PubDate     string `json:"pubDate"`
	Source      string `json:"source"`
	SourceKey   string `json:"sourceKey"`
	Category    string `json:"category"`
	ContentType string `json:"contentType"`
}

func (f *Feed) freeCryptoNews(ctx context.Context, coin string, now time.Time) ([]Event, []string, error) {
	q := url.Values{"q": {terms(coin)[0]}, "limit": {"40"}}
	var response struct {
		Articles   []newsArticle `json:"articles"`
		TotalCount int           `json:"totalCount"`
	}
	if err := f.get(ctx, f.newsURL+"/api/search?"+q.Encode(), &response); err != nil {
		return nil, nil, err
	}
	out := []Event{}
	warnings := []string{}
	if len(response.Articles) > 40 || response.TotalCount > 40 {
		warnings = append(warnings, "Free Crypto News "+coin+": showing first 40 search results")
	}
	seen := map[string]bool{}
	for i, a := range response.Articles {
		if i >= 40 {
			break
		}
		link := articleURL(a.Link)
		title, summary := plainText(a.Title, 1200), plainText(a.Description, 2000)
		published := parseTime(a.PubDate)
		if link == "" || seen[link] || title == "" || !recentArticle(published, now) || !articleMatches(coin, title+" "+summary, false) {
			continue
		}
		seen[link] = true
		articleType := "report" // Aggregated reporting is unverified, never a fact label.
		category := strings.ToLower(a.Category)
		if strings.EqualFold(a.SourceKey, "tradingview_crypto") || strings.Contains(category, "trading") || strings.Contains(category, "analysis") || strings.Contains(category, "opinion") {
			articleType = "opinion"
		}
		hash := sha256.Sum256([]byte(link))
		out = append(out, Event{ID: fmt.Sprintf("free-crypto-news:%x", hash), Kind: "news", Title: title, URL: link, Source: "Free Crypto News", Publisher: plainText(a.Source, 200), Summary: summary, ArticleType: articleType, PublishedAt: published, ReceivedAt: now, Markets: []Market{}, Coins: []string{}, Traders: []TraderRef{}})
	}
	return out, warnings, nil
}

type hnItem struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Text        string `json:"text"`
	Time        int64  `json:"time"`
	Score       *int   `json:"score"`
	Descendants *int   `json:"descendants"`
	Deleted     bool   `json:"deleted"`
	Dead        bool   `json:"dead"`
}

func (f *Feed) hackerNews(ctx context.Context, coin string, now time.Time) ([]Event, []string, error) {
	q := url.Values{"query": {terms(coin)[0]}, "tags": {"story"}, "hitsPerPage": {"20"}, "numericFilters": {"created_at_i>" + strconv.FormatInt(now.Add(-7*24*time.Hour).Unix(), 10)}, "restrictSearchableAttributes": {"title,url,story_text"}}
	var response struct {
		Hits []struct {
			ID    string `json:"objectID"`
			Title string `json:"title"`
			Text  string `json:"story_text"`
		} `json:"hits"`
		NBHits int `json:"nbHits"`
	}
	if err := f.get(ctx, f.hnSearchURL+"/api/v1/search_by_date?"+q.Encode(), &response); err != nil {
		return nil, nil, err
	}
	out, warnings := []Event{}, []string{}
	seen := map[int]bool{}
	verified, failures := 0, 0
	for i, hit := range response.Hits {
		if i >= 20 {
			break
		}
		id, err := strconv.Atoi(hit.ID)
		if err != nil || id <= 0 || seen[id] || !articleMatches(coin, plainText(hit.Title, 1200)+" "+plainText(hit.Text, 2000), true) {
			continue
		}
		seen[id] = true
		if verified >= 10 {
			warnings = append(warnings, "Hacker News "+coin+": official verification capped at 10 candidates; partial results")
			break
		}
		verified++
		var item *hnItem
		if err := f.get(ctx, f.hnURL+"/v0/item/"+strconv.Itoa(id)+".json", &item); err != nil {
			failures++
			continue
		}
		if item == nil || item.ID != id || item.Type != "story" || item.Deleted || item.Dead {
			continue
		}
		title, summary := plainText(item.Title, 1200), plainText(item.Text, 2000)
		published := time.Unix(item.Time, 0).UTC()
		if title == "" || !recentArticle(published, now) || !articleMatches(coin, title+" "+summary, true) {
			continue
		}
		discussion := "https://news.ycombinator.com/item?id=" + strconv.Itoa(id)
		link := discussion
		if item.URL != "" {
			link = articleURL(item.URL)
			if link == "" {
				continue
			}
		}
		out = append(out, Event{ID: "hacker-news:" + strconv.Itoa(id), Kind: "community", Title: title, URL: link, Source: "Hacker News", Publisher: "Hacker News", Summary: summary, DiscussionURL: discussion, Score: item.Score, Comments: item.Descendants, ArticleType: "discussion", PublishedAt: published, ReceivedAt: now, Markets: []Market{}, Coins: []string{}, Traders: []TraderRef{}})
	}
	if failures > 0 {
		warnings = append(warnings, "Hacker News "+coin+": official item verification unavailable; partial results")
		if failures == verified {
			return nil, warnings, fmt.Errorf("official verification unavailable")
		}
	}
	if response.NBHits > 20 || len(response.Hits) > 20 {
		warnings = append(warnings, "Hacker News "+coin+": showing first 20 discovery results")
	}
	return out, warnings, nil
}
