package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestArticleSafetyAndMatching(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "https://user:pass@example.com/a", "http://127.0.0.1/a", "http://10.0.0.1/a", "http://[::1]/a", "https://localhost/a", "http://169.254.169.254/a", "file:///a", "//example.com/a"} {
		if got := articleURL(raw); got != "" {
			t.Errorf("unsafe URL accepted %s: %s", raw, got)
		}
	}
	if articleURL("https://Example.com/a?utm_source=x&b=2#frag") != "https://example.com/a?b=2" {
		t.Fatal("canonical URL")
	}
	if got := plainText(`<p>Bitcoin &amp; Ethereum</p><script>LINK token</script><style>OP</style>`, 2000); got != "Bitcoin & Ethereum" {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		coin, text string
		hn, want   bool
	}{
		{"BTC", "whether a solution works", false, false},
		{"BTC", "Bitcoin improves", true, true},
		{"LINK", "click this link", false, false},
		{"LINK", "LINK token launch", false, true},
		{"LINK", "LINK token launch", true, false},
		{"LINK", "Chainlink launch", true, true},
		{"NEAR", "near the station", false, false},
		{"OP", "Optimism about AI", true, false},
		{"OP", "Optimism blockchain upgrade", true, true},
		{"TAO", "AI research breakthrough", false, false},
		{"SOL", "a solution to crypto", false, false},
	} {
		if got := articleMatches(tc.coin, tc.text, tc.hn); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}

func TestFreeCryptoNewsFixtures(t *testing.T) {
	now := time.Now().UTC()
	base := newsArticle{Title: "Bitcoin news", Link: "https://example.com/btc?utm_source=a#x", Description: "<p>Bitcoin &amp; crypto</p><script>evil</script>", PubDate: now.Add(-time.Hour).Format(time.RFC3339), Source: "Example"}
	articles := []newsArticle{base}
	add := func(fn func(*newsArticle)) { a := base; fn(&a); articles = append(articles, a) }
	add(func(a *newsArticle) { a.Link = "https://example.com/btc?utm_campaign=b" })
	add(func(a *newsArticle) {
		a.Link = "https://example.com/opinion"
		a.SourceKey = "tradingview_crypto"
		a.ContentType = "news"
	})
	add(func(a *newsArticle) { a.Link = "https://example.com/analysis"; a.Category = "analysis" })
	add(func(a *newsArticle) {
		a.Link = "https://example.com/old"
		a.PubDate = now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	})
	add(func(a *newsArticle) {
		a.Link = "https://example.com/future"
		a.PubDate = now.Add(time.Hour).Format(time.RFC3339)
	})
	add(func(a *newsArticle) { a.Link = "javascript:bad" })
	add(func(a *newsArticle) {
		a.Link = "https://example.com/ai"
		a.Title = "AI innovation"
		a.Description = "generic AI"
	})
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"articles": articles, "totalCount": 100})
	})
	out, warn, err := f.freeCryptoNews(context.Background(), "BTC", now)
	if err != nil || len(out) != 3 || len(warn) != 1 {
		t.Fatal(out, warn, err)
	}
	for i, e := range out {
		if e.Kind != "news" || e.Markets == nil || len(e.Markets) != 0 || e.EndAt != nil || e.Publisher != "Example" || e.Summary != "Bitcoin & crypto" || !strings.HasPrefix(e.ID, "free-crypto-news:") {
			t.Fatal(e)
		}
		if i > 0 && e.ArticleType != "opinion" {
			t.Fatal("opinion marked report", e)
		}
	}
	none, _, err := f.freeCryptoNews(context.Background(), "ETH", now)
	if err != nil || len(none) != 0 {
		t.Fatal(none, err)
	}
}

func TestHackerNewsOfficialVerification(t *testing.T) {
	now := time.Now().UTC()
	var requests atomic.Int32
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/search_by_date" {
			fmt.Fprint(w, `{"hits":[{"objectID":"1","title":"Bitcoin discovery"},{"objectID":"2","title":"Bitcoin discovery"},{"objectID":"3","title":"Bitcoin discovery"},{"objectID":"4","title":"Bitcoin discovery"},{"objectID":"5","title":"Bitcoin discovery"},{"objectID":"6","title":"Bitcoin discovery"},{"objectID":"7","title":"unrelated author","url":"https://example.com/bitcoin"},{"objectID":"1","title":"Bitcoin discovery"}]}`)
			return
		}
		requests.Add(1)
		switch r.URL.Path {
		case "/v0/item/1.json":
			fmt.Fprintf(w, `{"id":1,"type":"story","title":"Official Bitcoin title","text":"<p>Bitcoin &amp; crypto</p>","url":"https://example.com/official","time":%d,"score":42,"descendants":7}`, now.Unix())
		case "/v0/item/2.json":
			fmt.Fprint(w, `{"id":2,"type":"story","deleted":true}`)
		case "/v0/item/3.json":
			fmt.Fprint(w, `null`)
		case "/v0/item/4.json":
			fmt.Fprint(w, `{"id":4,"type":"story","dead":true}`)
		case "/v0/item/5.json":
			fmt.Fprint(w, `{"id":5,"type":"comment","title":"Bitcoin"}`)
		case "/v0/item/6.json":
			fmt.Fprintf(w, `{"id":6,"type":"story","title":"Unrelated official title","url":"https://example.com/Bitcoin","time":%d}`, now.Unix())
		default:
			t.Errorf("unexpected arbitrary fetch %s", r.URL)
			w.WriteHeader(404)
		}
	})
	out, warn, err := f.hackerNews(context.Background(), "BTC", now)
	if err != nil || len(warn) != 0 || len(out) != 1 || requests.Load() != 6 {
		t.Fatal(out, warn, err, requests.Load())
	}
	e := out[0]
	if e.ID != "hacker-news:1" || e.Title != "Official Bitcoin title" || e.Kind != "community" || e.ArticleType != "discussion" || *e.Score != 42 || *e.Comments != 7 || e.DiscussionURL != "https://news.ycombinator.com/item?id=1" || e.Markets == nil || e.EndAt != nil {
		t.Fatal(e)
	}
}

func TestHackerNewsVerificationBound(t *testing.T) {
	now := time.Now().UTC()
	var hits atomic.Int32
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/search_by_date" {
			items := []map[string]string{}
			for i := 1; i <= 20; i++ {
				items = append(items, map[string]string{"objectID": fmt.Sprint(i), "title": "Bitcoin"})
			}
			json.NewEncoder(w).Encode(map[string]any{"hits": items})
			return
		}
		hits.Add(1)
		fmt.Fprint(w, `null`)
	})
	out, warn, err := f.hackerNews(context.Background(), "BTC", now)
	if err != nil || len(out) != 0 || hits.Load() != 10 || len(warn) != 1 {
		t.Fatal(out, warn, err, hits.Load())
	}
}

func TestFourSourcesIsolationCoalescingAndStaleNews(t *testing.T) {
	var fail atomic.Bool
	var newsHits atomic.Int32
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			newsHits.Add(1)
			if fail.Load() {
				w.WriteHeader(503)
				return
			}
			fmt.Fprintf(w, `{"articles":[{"title":"Bitcoin news","link":"https://example.com/btc","pubDate":%q}]}`, time.Now().UTC().Format(time.RFC3339))
		case "/api/v1/search_by_date":
			fmt.Fprint(w, `{"hits":[{"objectID":"1","title":"Bitcoin"}]}`)
		case "/v0/item/1.json":
			fmt.Fprintf(w, `{"id":1,"type":"story","title":"Bitcoin discussion","time":%d}`, time.Now().Unix())
		default:
			defaultHandler(w, r)
		}
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.sourceEvents(context.Background(), "Free Crypto News", "BTC") }()
	}
	wg.Wait()
	if newsHits.Load() != 1 {
		t.Fatal("not coalesced", newsHits.Load())
	}
	board := f.Build(context.Background(), []Watch{{Symbol: "BTCUSDT", Traders: []TraderRef{{TraderID: "a"}}}}, 1, nil)
	if len(board.Events) != 4 || len(board.Warnings) != 0 {
		t.Fatal(board)
	}
	fail.Store(true)
	entry := f.cache["Free Crypto News:BTC"]
	entry.fetched = time.Now().Add(-3 * time.Minute)
	f.cache["Free Crypto News:BTC"] = entry
	stale, warnings := f.sourceEvents(context.Background(), "Free Crypto News", "BTC")
	if len(stale) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "cached") {
		t.Fatal(stale, warnings)
	}
	if !stale[0].ReceivedAt.Equal(entry.events[0].ReceivedAt) {
		t.Fatal("stale timestamp refreshed")
	}
	entry = f.cache["Free Crypto News:BTC"]
	entry.fetched = time.Now().Add(-16 * time.Minute)
	entry.retryAfter = time.Time{}
	f.cache["Free Crypto News:BTC"] = entry
	board = f.Build(context.Background(), []Watch{{Symbol: "BTCUSDT", Traders: []TraderRef{{TraderID: "b"}}}}, 1, nil)
	if len(board.Events) != 3 || len(board.Warnings) != 1 {
		t.Fatal(board)
	}
	for _, e := range board.Events {
		if len(e.Traders) != 1 || e.Traders[0].TraderID != "b" {
			t.Fatal("cache association leak", e)
		}
	}
	_, warnings = f.sourceEvents(context.Background(), "unknown", "BTC")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unknown") {
		t.Fatal(warnings)
	}
}

func TestArticleLabelProvenanceFingerprint(t *testing.T) {
	e := Event{ID: "news:1", Kind: "news", Publisher: "Publisher", Summary: "Bitcoin report", ArticleType: "report", Coins: []string{"BTC"}, Markets: []Market{}}
	in, hash, err := BuildLabelInput(e, "model")
	if err != nil || in.Kind != "news" || strings.Contains(strings.Join(in.Limitations, ""), "市场价格") {
		t.Fatal(in, err)
	}
	for _, mutate := range []func(*Event){func(e *Event) { e.Publisher = "Other" }, func(e *Event) { e.Summary = "Updated" }, func(e *Event) { e.Kind = "community" }, func(e *Event) { e.ArticleType = "opinion" }, func(e *Event) { n := 5; e.Score = &n }} {
		changed := e
		mutate(&changed)
		_, next, _ := BuildLabelInput(changed, "model")
		if next == hash {
			t.Fatal("provenance not hashed")
		}
	}
	e.ReceivedAt = time.Now()
	_, next, _ := BuildLabelInput(e, "model")
	if next != hash {
		t.Fatal("fetch time hashed")
	}
	if !strings.Contains(LabelSystemPrompt, "opinion") || !strings.Contains(LabelSystemPrompt, "未核实报道") {
		t.Fatal("missing guidance")
	}
}

func TestHackerNewsFailuresDoNotBreakOtherSources(t *testing.T) {
	for _, endpoint := range []string{"/api/v1/search_by_date", "/v0/item/1.json"} {
		t.Run(endpoint, func(t *testing.T) {
			f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == endpoint {
					w.WriteHeader(503)
					return
				}
				if r.URL.Path == "/api/v1/search_by_date" {
					fmt.Fprint(w, `{"hits":[{"objectID":"1","title":"Bitcoin"}]}`)
					return
				}
				defaultHandler(w, r)
			})
			b := f.Build(context.Background(), []Watch{{Symbol: "BTCUSDT"}}, 1, nil)
			if len(b.Events) != 2 || len(b.Warnings) == 0 {
				t.Fatal(b)
			}
			for _, warning := range b.Warnings {
				if !strings.Contains(warning, "Hacker News") {
					t.Fatal(warning)
				}
			}
		})
	}
}
