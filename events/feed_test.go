package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testFeed(t *testing.T, handler http.HandlerFunc) *Feed {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f := New()
	f.polyURL = server.URL
	f.kalshiURL = server.URL
	f.newsURL, f.hnSearchURL, f.hnURL = server.URL, server.URL, server.URL
	f.client = server.Client()
	return f
}
func polyFixture() string {
	return `{"events":[{"id":"1","slug":"bitcoin-ethereum","title":"Bitcoin and Ethereum prices","createdAt":"2026-01-01T00:00:00Z","endDate":"2099-01-01T00:00:00Z","active":true,"markets":[{"id":"m1","question":"Will Bitcoin rise?","active":true,"outcomes":"[\"Yes\",\"No\"]","outcomePrices":"[\"0.65\",\"0.35\"]","oneDayPriceChange":0.03}]}],"pagination":{"hasMore":false}}`
}
func defaultHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/public-search":
		fmt.Fprint(w, polyFixture())
	case "/series":
		fmt.Fprint(w, `{"series":[{"ticker":"BTC","title":"Bitcoin monthly price"},{"ticker":"SOL","title":"Solana price"}]}`)
	case "/events":
		fmt.Fprintf(w, `{"cursor":"","events":[{"event_ticker":"%s-event","title":"%s price this month","markets":[{"ticker":"yes","title":"Will price rise?","status":"active","created_time":"2026-01-01T00:00:00Z","close_time":"2099-01-01T00:00:00Z","last_price_dollars":"0.42","volume_fp":"100.0"},{"ticker":"closed","status":"finalized","close_time":"2099-01-01T00:00:00Z","last_price_dollars":"1","volume_fp":"20"}]}]}`, r.URL.Query().Get("series_ticker"), r.URL.Query().Get("series_ticker"))
	case "/api/search":
		fmt.Fprint(w, `{"articles":[]}`)
	case "/api/v1/search_by_date":
		fmt.Fprint(w, `{"hits":[]}`)
	default:
		w.WriteHeader(404)
	}
}
func TestCoinAndWatches(t *testing.T) {
	for input, want := range map[string]string{"BTCUSDT": "BTC", "ETH/USDC": "ETH", "1000PEPEUSDT": "PEPE", "xyz:NVDA": "NVDA", "SOL-PERP": "SOL", "哈基米USDT": "哈基米", "BTC OR ETH": "", "https://evil.test": ""} {
		if got := Coin(input); got != want {
			t.Errorf("%s = %s, want %s", input, got, want)
		}
	}
	a, b := TraderRef{TraderID: "a"}, TraderRef{TraderID: "b"}
	w := Watches([]Watch{{Symbol: "BTCUSDT", Traders: []TraderRef{a}}, {Symbol: "BTCUSDC", Traders: []TraderRef{b, a}}})
	if len(w) != 1 || len(w[0].Traders) != 2 {
		t.Fatal(w)
	}
	for _, c := range []struct {
		coin, text string
		want       bool
	}{{"SOL", "a solution", false}, {"ETH", "whether", false}, {"BTC", "Bitcoin above 100k", true}, {"ETH", "ETH price", true}} {
		if matches(c.coin, c.text) != c.want {
			t.Errorf("match %+v", c)
		}
	}
}
func TestPolymarketParsing(t *testing.T) {
	f := testFeed(t, defaultHandler)
	out, warnings, err := f.polymarket(context.Background(), "BTC", time.Now())
	if err != nil || len(warnings) != 0 || len(out) != 1 {
		t.Fatal(out, warnings, err)
	}
	e := out[0]
	if e.ID != "polymarket:1" || e.Source != "Polymarket" || len(e.Markets) != 1 || e.Markets[0].Outcomes[0].Price != 0.65 || *e.Markets[0].Change24h != 0.03 || e.EndAt == nil {
		t.Fatalf("bad event %+v", e)
	}
	out, _, err = f.polymarket(context.Background(), "SOL", time.Now())
	if err != nil || len(out) != 0 {
		t.Fatal("irrelevant event accepted", out, err)
	}
}
func TestPolymarketInvalidPricesClosedExpired(t *testing.T) {
	for name, mutate := range map[string]func(string) string{
		"closed":       func(s string) string { return strings.Replace(s, `"active":true`, `"active":true,"closed":true`, 1) },
		"expired":      func(s string) string { return strings.ReplaceAll(s, "2099-01-01", "2000-01-01") },
		"nan":          func(s string) string { return strings.ReplaceAll(s, "0.65", "NaN") },
		"out of range": func(s string) string { return strings.ReplaceAll(s, "0.65", "1.2") },
		"mismatch":     func(s string) string { return strings.ReplaceAll(s, `[\"Yes\",\"No\"]`, `[\"Yes\"]`) },
	} {
		t.Run(name, func(t *testing.T) {
			f := testFeed(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, mutate(polyFixture())) })
			out, _, err := f.polymarket(context.Background(), "BTC", time.Now())
			if err != nil || len(out) != 0 {
				t.Fatal(out, err)
			}
		})
	}
}
func TestKalshiParsing(t *testing.T) {
	f := testFeed(t, defaultHandler)
	out, warnings, err := f.kalshi(context.Background(), "BTC", time.Now())
	if err != nil || len(warnings) != 0 || len(out) != 1 {
		t.Fatal(out, warnings, err)
	}
	e := out[0]
	if e.Source != "Kalshi" || len(e.Markets) != 1 || e.Markets[0].Outcomes[0].Price != 0.42 || e.EndAt == nil || e.Markets[0].Change24h != nil {
		t.Fatal(e)
	}
	f = testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			fmt.Fprint(w, `{"events":[{"event_ticker":"untraded","markets":[{"ticker":"m","status":"active","close_time":"2099-01-01T00:00:00Z","last_price_dollars":"0","volume_fp":"0"}]}]}`)
			return
		}
		defaultHandler(w, r)
	})
	out, _, err = f.kalshi(context.Background(), "BTC", time.Now())
	if err != nil || len(out) != 1 || len(out[0].Markets[0].Outcomes) != 0 {
		t.Fatal("invented probability", out, err)
	}
}
func TestPaginationWarnings(t *testing.T) {
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/public-search" {
			fmt.Fprint(w, strings.Replace(polyFixture(), `"hasMore":false`, `"hasMore":true`, 1))
			return
		}
		if r.URL.Path == "/events" {
			fmt.Fprint(w, `{"events":[],"cursor":"next"}`)
			return
		}
		defaultHandler(w, r)
	})
	out, warn, err := f.polymarket(context.Background(), "BTC", time.Now())
	if err != nil || len(out) != 1 || len(warn) != 1 {
		t.Fatal(out, warn, err)
	}
	_, warn, err = f.kalshi(context.Background(), "BTC", time.Now())
	if err != nil || len(warn) != 1 {
		t.Fatal(warn, err)
	}
}
func TestBuildScopeAndCacheIsolation(t *testing.T) {
	var hits atomic.Int32
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); defaultHandler(w, r) })
	a, b := TraderRef{TraderID: "alice"}, TraderRef{TraderID: "bob"}
	board := f.Build(context.Background(), []Watch{{Symbol: "BTCUSDT", Traders: []TraderRef{a}}, {Symbol: "ETHUSDT", Traders: []TraderRef{a}}}, 1, nil)
	if len(board.Events) != 2 {
		t.Fatalf("bad board %+v", board)
	}
	for _, e := range board.Events {
		if e.Source == "Polymarket" && len(e.Coins) != 2 {
			t.Fatal("not merged", e)
		}
	}
	first := hits.Load()
	other := f.Build(context.Background(), []Watch{{Symbol: "BTCUSDT", Traders: []TraderRef{b}}}, 1, nil)
	if hits.Load() != first || len(other.Events) != 2 {
		t.Fatal(other)
	}
	for _, e := range other.Events {
		if len(e.Coins) != 1 || len(e.Traders) != 1 || e.Traders[0].TraderID != "bob" {
			t.Fatal("association leak", e)
		}
	}
	empty := f.Build(context.Background(), nil, 0, nil)
	if empty.Events == nil || empty.Watches == nil || empty.Warnings == nil || len(empty.Events) != 0 {
		t.Fatal(empty)
	}
}
func TestFailuresStaleCacheAndPartialProvider(t *testing.T) {
	var fail atomic.Bool
	var hits atomic.Int32
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/public-search" {
			hits.Add(1)
			if fail.Load() {
				w.WriteHeader(503)
				return
			}
		}
		defaultHandler(w, r)
	})
	items, warn := f.sourceEvents(context.Background(), "Polymarket", "BTC")
	if len(items) != 1 || len(warn) != 0 {
		t.Fatal(items, warn)
	}
	key := "Polymarket:BTC"
	entry := f.cache[key]
	entry.fetched = time.Now().Add(-3 * time.Minute)
	f.cache[key] = entry
	fail.Store(true)
	items, warn = f.sourceEvents(context.Background(), "Polymarket", "BTC")
	if len(items) != 1 || len(warn) != 1 || !strings.Contains(warn[0], "cached") {
		t.Fatal(items, warn)
	}
	f.sourceEvents(context.Background(), "Polymarket", "BTC")
	if hits.Load() != 2 {
		t.Fatal("no backoff")
	}
	entry = f.cache[key]
	entry.fetched = time.Now().Add(-16 * time.Minute)
	entry.retryAfter = time.Time{}
	f.cache[key] = entry
	board := f.Build(context.Background(), []Watch{{Symbol: "BTCUSDT"}}, 1, nil)
	if len(board.Events) != 1 || board.Events[0].Source != "Kalshi" || len(board.Warnings) != 1 {
		t.Fatal("partial provider lost", board)
	}
}
func TestConcurrentFetchCoalesced(t *testing.T) {
	var hits atomic.Int32
	f := testFeed(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(10 * time.Millisecond)
		defaultHandler(w, r)
	})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, warn := f.sourceEvents(context.Background(), "Polymarket", "BTC")
			if len(items) != 1 || len(warn) != 0 {
				t.Error(items, warn)
			}
		}()
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatal(hits.Load())
	}
}
func TestCancelledBuild(t *testing.T) {
	f := testFeed(t, defaultHandler)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := f.Build(ctx, []Watch{{Symbol: "BTCUSDT"}}, 1, nil)
	if len(b.Events) != 0 || len(b.Warnings) != 4 {
		t.Fatal(b)
	}
	if _, err := json.Marshal(b); err != nil {
		t.Fatal(err)
	}
}
