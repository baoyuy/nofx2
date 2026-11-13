package explosive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"nofx/internal/apischeduler"
	"nofx/logger"
)

const (
	// streamURL is the raw futures stream: subscriptions are sent on it after
	// connecting, so the watched kline set follows the board.
	streamURL = "wss://fstream.binance.com/ws"
	// tickerStream is the all-market mini ticker: price, 24h change and
	// turnover for every contract, once a second.
	tickerStream = "!miniTicker@arr"
	// maxStreamBars bounds the kline history kept per symbol and interval.
	maxStreamBars = 200
	// klineIntervals are the frames the board scores on.
	kline5m = "5m"
	kline1h = "1h"
	// pricePoll is how often prices are refreshed when the ticker stream is
	// silent. Binance's futures websocket does not deliver ticker or kline
	// frames on every network (bookTicker does), so prices and the 24h stats
	// have a REST fallback that costs two light requests.
	pricePoll = time.Second
	statsPoll = 15 * time.Second
)

// liveTicker is one contract's latest reading from the stream.
type liveTicker struct {
	Price       float64
	ChangePct   float64
	QuoteVolume float64
	At          time.Time
}

// LiveTickers keeps the websocket feeds in memory: the all-market ticker plus
// one kline connection per interval. A failed or stale feed is simply a cache
// miss: callers fall back to REST.
type LiveTickers struct {
	mu        sync.RWMutex
	m         map[string]liveTicker
	updatedAt time.Time
	statsAt   time.Time
	updatedK  time.Time
	bars      map[string][]Kline         // symbol|interval, oldest first
	subs      map[string]map[string]bool // interval -> symbols currently watched

	tickerConn *websocket.Conn
	tickerSend sync.Mutex
	klineConn  map[string]*websocket.Conn
	klineSend  map[string]*sync.Mutex
	connected  atomic.Bool
	once       sync.Once
	baseURL    string
	http       *http.Client
}

func newLiveTickers() *LiveTickers {
	return &LiveTickers{
		m:         map[string]liveTicker{},
		bars:      map[string][]Kline{},
		subs:      map[string]map[string]bool{kline5m: {}, kline1h: {}},
		klineConn: map[string]*websocket.Conn{},
		klineSend: map[string]*sync.Mutex{kline5m: {}, kline1h: {}},
		baseURL:   "https://fapi.binance.com",
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

// Start connects the ticker and kline feeds and keeps them up until ctx ends.
func (l *LiveTickers) Start(ctx context.Context) {
	l.once.Do(func() {
		go l.keep(ctx, l.runTicker)
		go l.keep(ctx, func(ctx context.Context) { l.runKline(ctx, kline5m) })
		go l.keep(ctx, func(ctx context.Context) { l.runKline(ctx, kline1h) })
		// REST fallbacks: they keep the board live even where the futures
		// websocket is silent.
		go l.keep(ctx, l.pollPrices)
		go l.keep(ctx, l.pollStats)
	})
}

// keep reconnects a feed until ctx ends.
func (l *LiveTickers) keep(ctx context.Context, run func(context.Context)) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		run(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func dial(ctx context.Context) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            proxyFromEnvironment,
	}
	conn, _, err := dialer.DialContext(ctx, streamURL, nil)
	return conn, err
}

// readWindow is longer than Binance's 3-minute ping; any frame, including a
// pong, extends it.
const readWindow = 5 * time.Minute

func readLoop(conn *websocket.Conn, onMsg func([]byte)) {
	_ = conn.SetReadDeadline(time.Now().Add(readWindow))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(readWindow))
		return nil
	})
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(readWindow))
		onMsg(msg)
	}
}

func (l *LiveTickers) runTicker(ctx context.Context) {
	conn, err := dial(ctx)
	if err != nil {
		logger.Warnf("explosive: ticker stream: %v (retrying)", err)
		return
	}
	defer conn.Close()
	l.tickerConn = conn
	l.connected.Store(true)
	logger.Infof("explosive: ticker stream connected")
	if err := conn.WriteJSON(map[string]any{"method": "SUBSCRIBE", "params": []string{tickerStream}, "id": 1}); err != nil {
		return
	}
	readLoop(conn, l.apply)
	l.connected.Store(false)
}

func (l *LiveTickers) runKline(ctx context.Context, interval string) {
	conn, err := dial(ctx)
	if err != nil {
		logger.Warnf("explosive: kline %s stream: %v (retrying)", interval, err)
		return
	}
	defer conn.Close()
	l.mu.Lock()
	l.klineConn[interval] = conn
	symbols := make([]string, 0, len(l.subs[interval]))
	for s := range l.subs[interval] {
		symbols = append(symbols, s+"@kline_"+interval)
	}
	l.mu.Unlock()
	if len(symbols) > 0 {
		l.sendKline(interval, map[string]any{"method": "SUBSCRIBE", "params": symbols, "id": 2})
	}
	logger.Infof("explosive: kline %s stream connected (%d symbols)", interval, len(symbols))
	readLoop(conn, func(msg []byte) { l.applyKline(interval, msg) })
}

func (l *LiveTickers) sendKline(interval string, v any) {
	mu := l.klineSend[interval]
	if mu == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	l.mu.RLock()
	conn := l.klineConn[interval]
	l.mu.RUnlock()
	if conn == nil {
		return
	}
	_ = conn.WriteJSON(v)
}

// proxyFromEnvironment honours HTTPS_PROXY, normalising the socks5h scheme Go
// accepts but gorilla does not.
func proxyFromEnvironment(req *http.Request) (*url.URL, error) {
	u, err := http.ProxyFromEnvironment(req)
	if u == nil || err != nil {
		return u, err
	}
	if u.Scheme == "socks5h" {
		cp := *u
		cp.Scheme = "socks5"
		return &cp, nil
	}
	return u, nil
}

type miniTickerRow struct {
	Symbol      string `json:"s"`
	Close       string `json:"c"`
	Open        string `json:"o"`
	QuoteVolume string `json:"q"`
}

// apply decodes one mini-ticker message. The all-market stream sends an array.
func (l *LiveTickers) apply(msg []byte) {
	var rows []miniTickerRow
	if json.Unmarshal(msg, &rows) != nil || len(rows) == 0 {
		return
	}
	now := time.Now()
	next := make(map[string]liveTicker, len(rows))
	for _, r := range rows {
		closeP, err1 := strconv.ParseFloat(r.Close, 64)
		openP, err2 := strconv.ParseFloat(r.Open, 64)
		quote, _ := strconv.ParseFloat(r.QuoteVolume, 64)
		if err1 != nil || err2 != nil || closeP <= 0 || openP <= 0 {
			continue
		}
		next[r.Symbol] = liveTicker{Price: closeP, ChangePct: (closeP/openP - 1) * 100, QuoteVolume: quote, At: now}
	}
	if len(next) == 0 {
		return
	}
	l.mu.Lock()
	for k, v := range next {
		l.m[k] = v
	}
	l.updatedAt, l.statsAt = now, now
	l.mu.Unlock()
}

type klineEvent struct {
	Symbol string `json:"s"`
	K      struct {
		Start  int64  `json:"t"`
		Open   string `json:"o"`
		High   string `json:"h"`
		Low    string `json:"l"`
		Close  string `json:"c"`
		Volume string `json:"v"`
		Closed bool   `json:"x"`
	} `json:"k"`
}

// applyKline updates the forming bar of one interval, or appends a closed one.
func (l *LiveTickers) applyKline(interval string, msg []byte) {
	var ev klineEvent
	if json.Unmarshal(msg, &ev) != nil || ev.Symbol == "" || ev.K.Start == 0 {
		return
	}
	bar := Kline{
		Time: ev.K.Start, Open: parseNum(ev.K.Open), High: parseNum(ev.K.High),
		Low: parseNum(ev.K.Low), Close: parseNum(ev.K.Close), Volume: parseNum(ev.K.Volume),
	}
	if bar.Close <= 0 {
		return
	}
	key := barKey(ev.Symbol, interval)
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.bars[key]
	if n := len(list); n > 0 {
		switch {
		case list[n-1].Time == bar.Time:
			list[n-1] = bar
		case list[n-1].Time < bar.Time:
			list = append(list, bar)
			if len(list) > maxStreamBars {
				list = list[len(list)-maxStreamBars:]
			}
		default:
			return // an older bar, ignore
		}
	} else {
		list = []Kline{bar}
	}
	l.bars[key] = list
	l.updatedK = time.Now()
}

func parseNum(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func barKey(symbol, interval string) string { return symbol + "|" + interval }

// Seed stores REST-fetched bars so a newly subscribed symbol starts with
// history instead of waiting for the stream to fill it.
func (l *LiveTickers) Seed(symbol, interval string, bars []Kline) {
	if len(bars) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bars[barKey(symbol, interval)] = append([]Kline(nil), bars...)
}

// Bars returns the stream's bars for one symbol and interval, including the
// forming one, oldest first.
func (l *LiveTickers) Bars(symbol, interval string) []Kline {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Kline(nil), l.bars[barKey(symbol, interval)]...)
}

// SetSymbols makes the kline subscriptions match the board: 5m for every
// candidate and 1h for the refined ones, dropping what is no longer watched.
func (l *LiveTickers) SetSymbols(candidates, refined []string) {
	want := map[string]map[string]bool{kline5m: {}, kline1h: {}}
	for _, s := range candidates {
		want[kline5m][s] = true
	}
	for _, s := range refined {
		want[kline1h][s] = true
	}
	for interval, symbols := range want {
		l.mu.Lock()
		var add, drop []string
		for s := range symbols {
			if !l.subs[interval][s] {
				add = append(add, s)
				l.subs[interval][s] = true
			}
		}
		for s := range l.subs[interval] {
			if !symbols[s] {
				drop = append(drop, s)
				delete(l.subs[interval], s)
				delete(l.bars, barKey(s, interval)) // no longer needed
			}
		}
		l.mu.Unlock()
		if len(add) > 0 {
			params := make([]string, 0, len(add))
			for _, s := range add {
				params = append(params, strings.ToLower(s)+"@kline_"+interval)
			}
			l.sendKline(interval, map[string]any{"method": "SUBSCRIBE", "params": params, "id": 3})
		}
		if len(drop) > 0 {
			params := make([]string, 0, len(drop))
			for _, s := range drop {
				params = append(params, strings.ToLower(s)+"@kline_"+interval)
			}
			l.sendKline(interval, map[string]any{"method": "UNSUBSCRIBE", "params": params, "id": 4})
		}
	}
}

// Fresh tells whether the feed delivered a ticker message recently enough.
func (l *LiveTickers) Fresh(maxAge time.Duration) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return !l.updatedAt.IsZero() && time.Since(l.updatedAt) < maxAge
}

// StatsFresh tells whether the 24h change and turnover are recent. The price
// poller updates the same map, so freshness of the price alone is not enough.
func (l *LiveTickers) StatsFresh(maxAge time.Duration) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return !l.statsAt.IsZero() && time.Since(l.statsAt) < maxAge
}

// Get returns one contract's latest reading.
func (l *LiveTickers) Get(symbol string) (liveTicker, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	t, ok := l.m[symbol]
	return t, ok
}

// Snapshot returns a copy of every contract's reading.
func (l *LiveTickers) Snapshot() map[string]liveTicker {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]liveTicker, len(l.m))
	for k, v := range l.m {
		out[k] = v
	}
	return out
}

// BTCChange is the market's own 24h change, the benchmark for relative
// strength. NaN when the feed has no BTCUSDT row.
func (l *LiveTickers) BTCChange() float64 {
	if t, ok := l.Get("BTCUSDT"); ok {
		return t.ChangePct
	}
	return math.NaN()
}

// getJSON fetches one public futures endpoint.
func (l *LiveTickers) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := apischeduler.Do(l.http, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binance HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// pollPrices refreshes every contract's last price once a second.
func (l *LiveTickers) pollPrices(ctx context.Context) {
	t := time.NewTicker(pricePoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var rows []struct{ Symbol, Price string }
		if err := l.getJSON(ctx, "/fapi/v1/ticker/price", &rows); err != nil {
			return // keep() retries
		}
		now := time.Now()
		l.mu.Lock()
		for _, r := range rows {
			p, err := strconv.ParseFloat(r.Price, 64)
			if err != nil || p <= 0 {
				continue
			}
			tk := l.m[r.Symbol]
			tk.Price, tk.At = p, now
			l.m[r.Symbol] = tk
		}
		l.updatedAt = now
		l.mu.Unlock()
	}
}

// pollStats refreshes the 24h change and turnover, which the price endpoint
// does not carry.
func (l *LiveTickers) pollStats(ctx context.Context) {
	t := time.NewTicker(statsPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var rows []struct{ Symbol, LastPrice, PriceChangePercent, QuoteVolume string }
		if err := l.getJSON(ctx, "/fapi/v1/ticker/24hr", &rows); err != nil {
			return
		}
		now := time.Now()
		l.mu.Lock()
		for _, r := range rows {
			p, e1 := strconv.ParseFloat(r.LastPrice, 64)
			chg, e2 := strconv.ParseFloat(r.PriceChangePercent, 64)
			quote, _ := strconv.ParseFloat(r.QuoteVolume, 64)
			if e1 != nil || e2 != nil || p <= 0 {
				continue
			}
			tk := l.m[r.Symbol]
			tk.Price, tk.ChangePct, tk.QuoteVolume, tk.At = p, chg, quote, now
			l.m[r.Symbol] = tk
		}
		l.updatedAt, l.statsAt = now, now
		l.mu.Unlock()
	}
}
