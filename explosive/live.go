package explosive

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"nofx/logger"
)

// miniTickerURL is Binance's all-market stream: one small message per second
// with every USDT-M contract's price, 24h change and turnover. It replaces the
// heavy REST 24h ticker on every scan and keeps the fast fields live.
const miniTickerURL = "wss://fstream.binance.com/ws/!miniTicker@arr"

// liveTicker is one contract's latest reading from the stream.
type liveTicker struct {
	Price       float64
	ChangePct   float64
	QuoteVolume float64
	At          time.Time
}

// LiveTickers keeps the websocket feed in memory. A failed or stale feed is
// simply a cache miss: callers fall back to REST.
type LiveTickers struct {
	mu        sync.RWMutex
	m         map[string]liveTicker
	updatedAt time.Time
	connected atomic.Bool
	once      sync.Once
}

func newLiveTickers() *LiveTickers {
	return &LiveTickers{m: map[string]liveTicker{}}
}

// Start connects and reconnects until ctx ends. It never blocks.
func (l *LiveTickers) Start(ctx context.Context) {
	l.once.Do(func() {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				l.run(ctx)
				l.connected.Store(false)
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}()
	})
}

func (l *LiveTickers) run(ctx context.Context) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            proxyFromEnvironment,
	}
	conn, _, err := dialer.DialContext(ctx, miniTickerURL, nil)
	if err != nil {
		logger.Warnf("explosive: live ticker stream: %v (retrying)", err)
		return
	}
	logger.Infof("explosive: live ticker stream connected")
	defer conn.Close()
	l.connected.Store(true)

	const readWindow = 5 * time.Minute
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
		l.apply(msg)
	}
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

// apply decodes one stream message into the cache.
func (l *LiveTickers) apply(msg []byte) {
	var rows []miniTickerRow
	if json.Unmarshal(msg, &rows) != nil {
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
	l.m, l.updatedAt = next, now
	l.mu.Unlock()
}

// Fresh tells whether the feed delivered a message recently enough to use.
func (l *LiveTickers) Fresh(maxAge time.Duration) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return !l.updatedAt.IsZero() && time.Since(l.updatedAt) < maxAge
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
// strength. Zero when the feed has no BTCUSDT row.
func (l *LiveTickers) BTCChange() float64 {
	if t, ok := l.Get("BTCUSDT"); ok {
		return t.ChangePct
	}
	return math.NaN()
}
