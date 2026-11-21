// Package events aggregates read-only prediction markets for strategy assets.
// Market prices are not verified facts, forecasts, or instructions to trade.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TraderRef struct {
	TraderID     string `json:"trader_id"`
	TraderName   string `json:"trader_name"`
	StrategyID   string `json:"strategy_id"`
	StrategyName string `json:"strategy_name"`
}
type Watch struct {
	Symbol  string      `json:"symbol"`
	Coin    string      `json:"coin"`
	Traders []TraderRef `json:"traders"`
}
type Outcome struct {
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}
type Market struct {
	ID            string        `json:"id"`
	Question      string        `json:"question"`
	Outcomes      []Outcome     `json:"outcomes"`
	PriceBasis    string        `json:"price_basis"`
	Change24h     *float64      `json:"change_24h,omitempty"`
	Changes       []PriceChange `json:"changes"`
	HistoryStatus string        `json:"history_status"`
	EndAt         *time.Time    `json:"end_at,omitempty"`
	Rules         string        `json:"-"`
}
type Event struct {
	ID                string      `json:"id"`
	Kind              string      `json:"kind"`
	Publisher         string      `json:"publisher,omitempty"`
	Summary           string      `json:"summary,omitempty"`
	DiscussionURL     string      `json:"discussion_url,omitempty"`
	Score             *int        `json:"score,omitempty"`
	Comments          *int        `json:"comments,omitempty"`
	ArticleType       string      `json:"article_type,omitempty"`
	Title             string      `json:"title"`
	URL               string      `json:"url"`
	Source            string      `json:"source"`
	PublishedAt       time.Time   `json:"published_at"`
	ReceivedAt        time.Time   `json:"received_at"`
	EndAt             *time.Time  `json:"end_at,omitempty"`
	Markets           []Market    `json:"markets"`
	Coins             []string    `json:"coins"`
	Traders           []TraderRef `json:"traders"`
	FirstSeenAt       *time.Time  `json:"first_seen_at,omitempty"`
	IsNew             bool        `json:"is_new"`
	EndingSoon        bool        `json:"ending_soon"`
	SignificantChange bool        `json:"significant_change"`
	Label             *Label      `json:"label,omitempty"`
}
type Board struct {
	GeneratedAt    time.Time `json:"generated_at"`
	RunningTraders int       `json:"running_traders"`
	Watches        []Watch   `json:"watches"`
	Events         []Event   `json:"events"`
	Warnings       []string  `json:"warnings"`
}

var validCoin = regexp.MustCompile(`^[\p{L}\p{N}]{1,24}$`)

func Coin(symbol string) string {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, "-PERP")
	s = strings.ReplaceAll(s, "/", "")
	for _, quote := range []string{"USDT", "USDC", "USD"} {
		if strings.HasSuffix(s, quote) && len(s) > len(quote) {
			s = strings.TrimSuffix(s, quote)
			break
		}
	}
	for _, prefix := range []string{"1000000", "1000"} {
		if strings.HasPrefix(s, prefix) && len(s) > len(prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	if !validCoin.MatchString(s) {
		return ""
	}
	return s
}
func appendRef(refs []TraderRef, ref TraderRef) []TraderRef {
	for _, old := range refs {
		if old.TraderID == ref.TraderID {
			return refs
		}
	}
	return append(refs, ref)
}
func Watches(input []Watch) []Watch {
	byCoin := map[string]*Watch{}
	for _, w := range input {
		coin := Coin(w.Symbol)
		if coin == "" {
			continue
		}
		out := byCoin[coin]
		if out == nil {
			out = &Watch{Symbol: w.Symbol, Coin: coin, Traders: []TraderRef{}}
			byCoin[coin] = out
		}
		for _, ref := range w.Traders {
			out.Traders = appendRef(out.Traders, ref)
		}
	}
	out := make([]Watch, 0, len(byCoin))
	for _, w := range byCoin {
		sort.Slice(w.Traders, func(i, j int) bool { return w.Traders[i].TraderID < w.Traders[j].TraderID })
		out = append(out, *w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Coin < out[j].Coin })
	return out
}

var aliases = map[string][]string{
	"BTC": {"Bitcoin", "BTC"}, "ETH": {"Ethereum", "ETH", "Ether"}, "SOL": {"Solana", "SOL"}, "DOGE": {"Dogecoin", "DOGE"}, "XRP": {"XRP", "Ripple"}, "BNB": {"BNB"}, "ADA": {"Cardano", "ADA"}, "AVAX": {"Avalanche", "AVAX"}, "DOT": {"Polkadot", "DOT"}, "LINK": {"Chainlink", "LINK"}, "PEPE": {"Pepe", "PEPE"}, "SUI": {"Sui"}, "ZEC": {"Zcash", "ZEC"}, "BCH": {"Bitcoin Cash", "BCH"}, "TON": {"Toncoin", "TON"}, "NEAR": {"NEAR Protocol", "NEAR"}, "LTC": {"Litecoin", "LTC"}, "AAVE": {"Aave"}, "UNI": {"Uniswap", "UNI"}, "ARB": {"Arbitrum", "ARB"}, "OP": {"Optimism", "OP"}, "APT": {"Aptos", "APT"}, "TAO": {"Bittensor", "TAO"}, "HYPE": {"Hyperliquid", "HYPE"},
}

func terms(coin string) []string {
	if a := aliases[coin]; len(a) > 0 {
		return a
	}
	return []string{coin}
}
func matches(coin, text string) bool {
	// Boundaries prevent SOL matching 'solution' and ETH matching 'whether'.
	// Descriptions are intentionally excluded: boilerplate can mention unrelated coins.
	for _, term := range terms(coin) {
		re := regexp.MustCompile(`(?i)(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(term) + `($|[^\p{L}\p{N}])`)
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

type cacheEntry struct {
	events                    []Event
	warnings                  []string
	fetched, retryAfter, used time.Time
}
type series struct {
	Ticker string `json:"ticker"`
	Title  string `json:"title"`
}
type Feed struct {
	client                      *http.Client
	polyURL, kalshiURL          string
	newsURL, hnSearchURL, hnURL string
	mu                          sync.Mutex
	cache                       map[string]cacheEntry
	flights                     map[string]chan struct{}
	seriesMu                    sync.Mutex
	series                      []series
	seriesFetched               time.Time
	sem                         chan struct{}
	// Extra information sources (macro / earnings / crypto calendar / telegram).
	macroURL       string
	earnURL        string
	cryptoCalPath  string
	fearGreedURL   string
	rssFeeds       []string
	cryptopanicKey string
	whaleAlertKey  string
	tgChannels     []string
	extraEnabled   bool
	extraMu        sync.Mutex
	extraCache     map[string]extraEntry
}

func New() *Feed {
	f := &Feed{client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, polyURL: "https://gamma-api.polymarket.com", kalshiURL: "https://api.elections.kalshi.com/trade-api/v2", newsURL: "https://cryptocurrency.cv", hnSearchURL: "https://hn.algolia.com", hnURL: "https://hacker-news.firebaseio.com", cache: map[string]cacheEntry{}, flights: map[string]chan struct{}{}, sem: make(chan struct{}, 6), extraCache: map[string]extraEntry{}}
	f.extraEnabled = isTruthy(os.Getenv("EVENT_EXTRA_SOURCES"))
	f.macroURL = envOr("MACRO_CALENDAR_URL", "https://nfs.faireconomy.media/ff_calendar_thisweek.json")
	f.earnURL = envOr("EARNINGS_CALENDAR_URL", "https://api.nasdaq.com/api/calendar/earnings")
	f.cryptoCalPath = strings.TrimSpace(os.Getenv("CRYPTO_CALENDAR_PATH"))
	f.fearGreedURL = envOr("FEAR_GREED_URL", "https://api.alternative.me/fng/?limit=2")
	f.cryptopanicKey = strings.TrimSpace(os.Getenv("CRYPTOPANIC_API_KEY"))
	f.whaleAlertKey = strings.TrimSpace(os.Getenv("WHALE_ALERT_API_KEY"))
	if v := strings.TrimSpace(os.Getenv("RSS_FEEDS")); v != "" {
		for _, u := range strings.Split(v, ",") {
			if u = strings.TrimSpace(u); u != "" {
				f.rssFeeds = append(f.rssFeeds, u)
			}
		}
	} else {
		f.rssFeeds = []string{
			"https://www.coindesk.com/arc/outboundfeeds/rss/",
			"https://www.theblock.co/rss.xml",
			"https://decrypt.co/feed",
			"https://cointelegraph.com/rss",
		}
	}
	for _, c := range strings.Split(os.Getenv("TG_PUBLIC_CHANNELS"), ",") {
		if c = strings.TrimSpace(strings.TrimPrefix(c, "@")); c != "" {
			f.tgChannels = append(f.tgChannels, c)
		}
	}
	return f
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
func (f *Feed) get(ctx context.Context, raw string, out any) error {
	select {
	case f.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-f.sem }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NOFX-Events/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}
func (f *Feed) sourceEvents(ctx context.Context, source, coin string) ([]Event, []string) {
	key := source + ":" + coin
	for {
		now := time.Now().UTC()
		f.mu.Lock()
		entry, ok := f.cache[key]
		if ok && (now.Sub(entry.fetched) < 2*time.Minute || now.Before(entry.retryAfter)) {
			entry.used = now
			f.cache[key] = entry
			f.mu.Unlock()
			return entry.events, entry.warnings
		}
		if done := f.flights[key]; done != nil {
			f.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, []string{source + " request timed out for " + coin}
			}
		}
		done := make(chan struct{})
		f.flights[key] = done
		f.mu.Unlock()
		var items []Event
		var warnings []string
		var err error
		switch source {
		case "Polymarket":
			items, warnings, err = f.polymarket(ctx, coin, now)
		case "Kalshi":
			items, warnings, err = f.kalshi(ctx, coin, now)
		case "Free Crypto News":
			items, warnings, err = f.freeCryptoNews(ctx, coin, now)
		case "Hacker News":
			items, warnings, err = f.hackerNews(ctx, coin, now)
		default:
			err = fmt.Errorf("unknown source")
		}
		f.mu.Lock()
		if len(f.cache) >= 512 {
			oldest := ""
			var stamp time.Time
			for k, e := range f.cache {
				if k != key && (oldest == "" || e.used.Before(stamp)) {
					oldest, stamp = k, e.used
				}
			}
			delete(f.cache, oldest)
		}
		if err == nil {
			entry = cacheEntry{events: items, warnings: warnings, fetched: now, used: now}
		} else {
			warning := source + " unavailable for " + coin
			if entry.fetched.IsZero() || now.Sub(entry.fetched) > 15*time.Minute {
				entry.events = nil
			} else {
				warning += "; showing cached events"
			}
			entry.warnings = append(warnings, warning)
			entry.retryAfter = now.Add(time.Minute)
			entry.used = now
		}
		f.cache[key] = entry
		delete(f.flights, key)
		close(done)
		f.mu.Unlock()
		return entry.events, entry.warnings
	}
}
func parseTime(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t.UTC() }
func price(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}
func deadline(s string) *time.Time {
	t := parseTime(s)
	if t.IsZero() {
		return nil
	}
	return &t
}

type polyMarket struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Outcomes    string   `json:"outcomes"`
	Prices      string   `json:"outcomePrices"`
	Active      bool     `json:"active"`
	Closed      bool     `json:"closed"`
	Archived    bool     `json:"archived"`
	End         string   `json:"endDate"`
	Change      *float64 `json:"oneDayPriceChange"`
	Description string   `json:"description"`
}
type polyEvent struct {
	ID       string       `json:"id"`
	Slug     string       `json:"slug"`
	Title    string       `json:"title"`
	Created  string       `json:"createdAt"`
	End      string       `json:"endDate"`
	Active   bool         `json:"active"`
	Closed   bool         `json:"closed"`
	Archived bool         `json:"archived"`
	Markets  []polyMarket `json:"markets"`
}

func (f *Feed) polymarket(ctx context.Context, coin string, now time.Time) ([]Event, []string, error) {
	out := []Event{}
	warnings := []string{}
	seen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		q := url.Values{"q": {terms(coin)[0]}, "limit_per_type": {"20"}, "events_status": {"active"}, "page": {strconv.Itoa(page)}, "search_profiles": {"false"}, "search_tags": {"false"}, "keep_closed_markets": {"0"}}
		var response struct {
			Events     []polyEvent `json:"events"`
			Pagination struct {
				HasMore bool `json:"hasMore"`
			} `json:"pagination"`
		}
		if err := f.get(ctx, f.polyURL+"/public-search?"+q.Encode(), &response); err != nil {
			if len(out) > 0 {
				return out, append(warnings, "Polymarket "+coin+": later search page unavailable; partial results"), nil
			}
			return nil, warnings, err
		}
		for _, e := range response.Events {
			if seen[e.ID] || e.ID == "" || e.Slug == "" || !e.Active || e.Closed || e.Archived {
				continue
			}
			seen[e.ID] = true
			if end := deadline(e.End); end != nil && !end.After(now) {
				continue
			}
			event := Event{ID: "polymarket:" + e.ID, Title: e.Title, URL: "https://polymarket.com/event/" + url.PathEscape(e.Slug), Source: "Polymarket", PublishedAt: parseTime(e.Created), ReceivedAt: now, EndAt: deadline(e.End), Markets: []Market{}, Coins: []string{}, Traders: []TraderRef{}}
			event.Kind = "prediction"
			eventMatch := matches(coin, e.Title)
			for _, m := range e.Markets {
				if !m.Active || m.Closed || m.Archived || (!eventMatch && !matches(coin, m.Question)) {
					continue
				}
				if end := deadline(m.End); end != nil && !end.After(now) {
					continue
				}
				var labels, prices []string
				if json.Unmarshal([]byte(m.Outcomes), &labels) != nil || json.Unmarshal([]byte(m.Prices), &prices) != nil || len(labels) != len(prices) || len(labels) == 0 {
					continue
				}
				market := Market{ID: m.ID, Question: m.Question, Outcomes: []Outcome{}, PriceBasis: "Polymarket outcome price", EndAt: deadline(m.End), Rules: m.Description, Changes: []PriceChange{}, HistoryStatus: "unavailable"}
				valid := true
				for i, label := range labels {
					v, ok := price(prices[i])
					if !ok {
						valid = false
						break
					}
					market.Outcomes = append(market.Outcomes, Outcome{Name: label, Price: v})
				}
				if !valid {
					continue
				}
				// Platform day change refers to its first outcome; do not invent history.
				if m.Change != nil && !math.IsNaN(*m.Change) && !math.IsInf(*m.Change, 0) && math.Abs(*m.Change) <= 1 {
					market.Change24h = m.Change
				}
				event.Markets = append(event.Markets, market)
			}
			if len(event.Markets) > 0 {
				out = append(out, event)
			}
		}
		if !response.Pagination.HasMore {
			break
		}
		if page == 2 {
			warnings = append(warnings, "Polymarket "+coin+": showing first 40 search results")
		}
	}
	return out, warnings, nil
}
func (f *Feed) cryptoSeries(ctx context.Context) ([]series, error) {
	f.seriesMu.Lock()
	defer f.seriesMu.Unlock()
	if !f.seriesFetched.IsZero() && time.Since(f.seriesFetched) < 30*time.Minute {
		return f.series, nil
	}
	var response struct {
		Series []series `json:"series"`
	}
	if err := f.get(ctx, f.kalshiURL+"/series?category=Crypto", &response); err != nil {
		return nil, err
	}
	f.series = response.Series
	f.seriesFetched = time.Now()
	return f.series, nil
}

type kalshiMarket struct {
	Ticker         string `json:"ticker"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Created        string `json:"created_time"`
	Close          string `json:"close_time"`
	Last           string `json:"last_price_dollars"`
	Volume         string `json:"volume_fp"`
	RulesPrimary   string `json:"rules_primary"`
	RulesSecondary string `json:"rules_secondary"`
}
type kalshiEvent struct {
	Ticker  string         `json:"event_ticker"`
	Title   string         `json:"title"`
	Markets []kalshiMarket `json:"markets"`
}

func (f *Feed) kalshi(ctx context.Context, coin string, now time.Time) ([]Event, []string, error) {
	all, err := f.cryptoSeries(ctx)
	if err != nil {
		return nil, nil, err
	}
	relevant := []series{}
	for _, s := range all {
		if matches(coin, s.Title) {
			relevant = append(relevant, s)
		}
	}
	sort.Slice(relevant, func(i, j int) bool { return relevant[i].Ticker < relevant[j].Ticker })
	warnings := []string{}
	if len(relevant) > 30 {
		relevant = relevant[:30]
		warnings = append(warnings, "Kalshi "+coin+": showing first 30 matching series")
	}
	out := []Event{}
	for _, s := range relevant {
		cursor := ""
		for page := 0; page < 2; page++ {
			q := url.Values{"series_ticker": {s.Ticker}, "status": {"open"}, "with_nested_markets": {"true"}, "limit": {"100"}}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			var response struct {
				Events []kalshiEvent `json:"events"`
				Cursor string        `json:"cursor"`
			}
			if err := f.get(ctx, f.kalshiURL+"/events?"+q.Encode(), &response); err != nil {
				if len(out) > 0 {
					return out, append(warnings, "Kalshi "+coin+": series request incomplete; partial results"), nil
				}
				return nil, warnings, err
			}
			for _, e := range response.Events {
				if e.Ticker == "" {
					continue
				}
				event := Event{ID: "kalshi:" + e.Ticker, Title: e.Title, URL: "https://kalshi.com/markets/" + url.PathEscape(strings.ToLower(s.Ticker)) + "/" + url.PathEscape(strings.ToLower(e.Ticker)), Source: "Kalshi", ReceivedAt: now, Markets: []Market{}, Coins: []string{}, Traders: []TraderRef{}}
				event.Kind = "prediction"
				for _, m := range e.Markets {
					if m.Status != "active" && m.Status != "open" {
						continue
					}
					end := deadline(m.Close)
					if end == nil || !end.After(now) {
						continue
					}
					// Zero volume means no observed trade: do not present default zero as 0%.
					volume, _ := strconv.ParseFloat(m.Volume, 64)
					v, ok := price(m.Last)
					market := Market{ID: m.Ticker, Question: m.Title, Outcomes: []Outcome{}, PriceBasis: "Kalshi last traded Yes price (may be stale)", EndAt: end, Rules: m.RulesPrimary + "\n" + m.RulesSecondary, Changes: []PriceChange{}, HistoryStatus: "unavailable"}
					if ok && volume > 0 {
						market.Outcomes = []Outcome{{Name: "Yes", Price: v}, {Name: "No", Price: 1 - v}}
					}
					event.Markets = append(event.Markets, market)
					created := parseTime(m.Created)
					if !created.IsZero() && (event.PublishedAt.IsZero() || created.Before(event.PublishedAt)) {
						event.PublishedAt = created
					}
					if event.EndAt == nil || end.After(*event.EndAt) {
						event.EndAt = end
					}
				}
				if len(event.Markets) > 0 {
					out = append(out, event)
				}
			}
			cursor = response.Cursor
			if cursor == "" {
				break
			}
			if page == 1 {
				warnings = append(warnings, "Kalshi "+s.Ticker+": event pages truncated")
			}
		}
	}
	return out, warnings, nil
}
func (f *Feed) Build(ctx context.Context, watches []Watch, running int, warnings []string) Board {
	board := Board{GeneratedAt: time.Now().UTC(), RunningTraders: running, Watches: Watches(watches), Events: []Event{}, Warnings: append([]string{}, warnings...)}
	type result struct {
		watch    Watch
		events   []Event
		warnings []string
	}
	// Fetch market-wide / calendar sources first so they are not starved of the
	// shared HTTP semaphore by the many per-coin source goroutines.
	gevents, gwarn := f.globalEvents(ctx, board.Watches, time.Now().UTC())
	results := make(chan result, 4*len(board.Watches))
	var wg sync.WaitGroup
	for _, w := range board.Watches {
		for _, source := range []string{"Polymarket", "Kalshi", "Free Crypto News", "Hacker News"} {
			wg.Add(1)
			go func(w Watch, source string) {
				defer wg.Done()
				items, warnings := f.sourceEvents(ctx, source, w.Coin)
				results <- result{w, items, warnings}
			}(w, source)
		}
	}
	wg.Wait()
	board.GeneratedAt = time.Now().UTC()
	close(results)
	merged := map[string]*Event{}
	add := func(e Event, coins []string, refs []TraderRef) {
		if e.EndAt != nil && !e.EndAt.After(board.GeneratedAt) {
			return
		}
		out := merged[e.ID]
		if out == nil {
			copyEvent := e
			if copyEvent.Kind == "" {
				copyEvent.Kind = "prediction"
			}
			copyEvent.Coins = []string{}
			copyEvent.Traders = []TraderRef{}
			out = &copyEvent
			merged[e.ID] = out
		}
		for _, coin := range coins {
			found := false
			for _, c := range out.Coins {
				if c == coin {
					found = true
					break
				}
			}
			if !found && coin != "" {
				out.Coins = append(out.Coins, coin)
			}
		}
		for _, ref := range refs {
			out.Traders = appendRef(out.Traders, ref)
		}
	}
	for r := range results {
		board.Warnings = append(board.Warnings, r.warnings...)
		for _, e := range r.events {
			add(e, []string{r.watch.Coin}, r.watch.Traders)
		}
	}
	// Merge the global sources computed before the per-coin fetch.
	board.Warnings = append(board.Warnings, gwarn...)
	for _, e := range gevents {
		add(e, e.Coins, e.Traders)
	}
	for _, e := range merged {
		sort.Strings(e.Coins)
		sort.Slice(e.Traders, func(i, j int) bool { return e.Traders[i].TraderID < e.Traders[j].TraderID })
		board.Events = append(board.Events, *e)
	}
	sort.Slice(board.Events, func(i, j int) bool {
		a, b := board.Events[i], board.Events[j]
		if a.PublishedAt.Equal(b.PublishedAt) {
			return a.ID < b.ID
		}
		return a.PublishedAt.After(b.PublishedAt)
	})
	sort.Strings(board.Warnings)
	return board
}
