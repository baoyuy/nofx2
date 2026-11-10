// Package marketboard builds the trader dashboard's market panels (net flow,
// direction board, cost/liquidation map) from free Binance USDT-M futures
// data. They replace the paid Claw402/Vergex feeds with the same response
// shapes.
package marketboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nofx/internal/apischeduler"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	cacheTTL    = 30 * time.Second
	staleTTL    = 2 * time.Minute
	universe    = 30 // markets ranked by 24h quote volume
	concurrency = 8
)

// Client reads public Binance futures data. One client is shared by all users;
// results are cached briefly so dashboards polling together reuse one scan.
type Client struct {
	BaseURL string
	HTTP    *http.Client

	mu         sync.Mutex
	cache      map[string]cached
	flight     singleflight.Group
	refreshing map[string]bool
}

type cached struct {
	at      time.Time
	v       any
	retryAt time.Time
	err     error
}

func New() *Client {
	return &Client{BaseURL: "https://fapi.binance.com", HTTP: &http.Client{Timeout: 15 * time.Second}, cache: map[string]cached{}}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := apischeduler.Do(c.HTTP, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binance futures HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// cachedDo serves a bounded stale snapshot while a single refresh runs in the
// background. Cold requests wait only for their own context; shared work has
// its own deadline. Original data timestamps are left untouched.
func (c *Client) cachedDo(ctx context.Context, key string, fn func(ctx context.Context) (any, error)) (any, error) {
	c.mu.Lock()
	hit, ok := c.cache[key]
	if ok && time.Since(hit.at) < cacheTTL {
		c.mu.Unlock()
		return hit.v, nil
	}
	load := func() (any, error) {
		// A previous refresh may have completed between the cache lookup and
		// singleflight admission. Do not immediately scan the same data again.
		c.mu.Lock()
		fresh, found := c.cache[key]
		c.mu.Unlock()
		if found && time.Since(fresh.at) < cacheTTL {
			return fresh.v, nil
		}
		if found && time.Now().Before(fresh.retryAt) {
			return nil, fresh.err
		}
		shared, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		v, err := fn(shared)
		c.mu.Lock()
		if err == nil {
			c.cache[key] = cached{at: time.Now(), v: v}
		} else {
			old := c.cache[key]
			old.retryAt, old.err = time.Now().Add(15*time.Second), err
			c.cache[key] = old
		}
		c.mu.Unlock()
		return v, err
	}
	if ok && time.Since(hit.at) < staleTTL {
		if !c.refreshing[key] && !time.Now().Before(hit.retryAt) {
			if c.refreshing == nil {
				c.refreshing = make(map[string]bool)
			}
			c.refreshing[key] = true
			go func() {
				_, _, _ = c.flight.Do(key, load)
				c.mu.Lock()
				delete(c.refreshing, key)
				c.mu.Unlock()
			}()
		}
		c.mu.Unlock()
		return hit.v, nil
	}
	if ok && time.Now().Before(hit.retryAt) {
		c.mu.Unlock()
		return nil, hit.err
	}
	c.mu.Unlock()
	result := c.flight.DoChan(key, load)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-result:
		return r.Val, r.Err
	}
}

// Ticker is a perpetual's 24h statistics.
type Ticker struct {
	Symbol      string
	Price       float64
	ChangePct   float64
	QuoteVolume float64
}

// topByVolume returns the n most traded USDT-margined crypto perpetuals, or
// all of them when n <= 0.
func (c *Client) topByVolume(ctx context.Context, n int) ([]Ticker, error) {
	v, err := c.cachedDo(ctx, "top", func(ctx context.Context) (any, error) {
		var info struct {
			Symbols []struct{ Symbol, QuoteAsset, MarginAsset, Status, ContractType, UnderlyingType string }
		}
		if err := c.get(ctx, "/fapi/v1/exchangeInfo", &info); err != nil {
			return nil, err
		}
		perps := map[string]bool{}
		for _, m := range info.Symbols {
			// UnderlyingType filters out stock and commodity contracts.
			if m.Status == "TRADING" && m.QuoteAsset == "USDT" && m.MarginAsset == "USDT" && m.ContractType == "PERPETUAL" &&
				(m.UnderlyingType == "" || m.UnderlyingType == "COIN") {
				perps[m.Symbol] = true
			}
		}
		var rows []struct{ Symbol, LastPrice, PriceChangePercent, QuoteVolume string }
		if err := c.get(ctx, "/fapi/v1/ticker/24hr", &rows); err != nil {
			return nil, err
		}
		out := make([]Ticker, 0, len(rows))
		for _, r := range rows {
			price, _ := strconv.ParseFloat(r.LastPrice, 64)
			if !perps[r.Symbol] || price <= 0 {
				continue
			}
			change, _ := strconv.ParseFloat(r.PriceChangePercent, 64)
			volume, _ := strconv.ParseFloat(r.QuoteVolume, 64)
			out = append(out, Ticker{Symbol: r.Symbol, Price: price, ChangePct: change, QuoteVolume: volume})
		}
		sort.SliceStable(out, func(a, b int) bool { return out[a].QuoteVolume > out[b].QuoteVolume })
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	all := v.([]Ticker)
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	return all, nil
}

// Candle is one kline with Binance's taker-buy split.
type Candle struct {
	OpenTime      int64
	Open, High    float64
	Low, Close    float64
	QuoteVolume   float64
	TakerBuyQuote float64
	Trades        int64
}

func (c *Client) klines(ctx context.Context, symbol, interval string, limit int) ([]Candle, error) {
	var rows [][]json.RawMessage
	q := url.Values{"symbol": {symbol}, "interval": {interval}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, "/fapi/v1/klines?"+q.Encode(), &rows); err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(rows))
	for _, r := range rows {
		if len(r) < 11 {
			return nil, errors.New("invalid candle")
		}
		var k Candle
		if json.Unmarshal(r[0], &k.OpenTime) != nil || json.Unmarshal(r[8], &k.Trades) != nil {
			return nil, errors.New("invalid candle")
		}
		for _, f := range []struct {
			i   int
			dst *float64
		}{{1, &k.Open}, {2, &k.High}, {3, &k.Low}, {4, &k.Close}, {7, &k.QuoteVolume}, {10, &k.TakerBuyQuote}} {
			var s string
			if json.Unmarshal(r[f.i], &s) != nil {
				return nil, errors.New("invalid candle")
			}
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, errors.New("invalid candle")
			}
			*f.dst = v
		}
		out = append(out, k)
	}
	return out, nil
}

// openInterestHistory returns open interest notional (USDT) per period, oldest first.
func (c *Client) openInterestHistory(ctx context.Context, symbol, period string, limit int) ([]float64, error) {
	var rows []struct{ SumOpenInterestValue string }
	q := url.Values{"symbol": {symbol}, "period": {period}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, "/futures/data/openInterestHist?"+q.Encode(), &rows); err != nil {
		return nil, err
	}
	out := make([]float64, 0, len(rows))
	for _, r := range rows {
		v, err := strconv.ParseFloat(r.SumOpenInterestValue, 64)
		if err != nil {
			return nil, errors.New("invalid open interest")
		}
		out = append(out, v)
	}
	return out, nil
}

// forEach runs fn for every ticker with bounded concurrency and reports how
// many calls failed.
func forEach(tickers []Ticker, fn func(i int, t Ticker) error) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	slots := make(chan struct{}, concurrency)
	failed := 0
	for i, t := range tickers {
		wg.Add(1)
		go func(i int, t Ticker) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if fn(i, t) != nil {
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}(i, t)
	}
	wg.Wait()
	return failed
}

// BaseSymbol turns BTCUSDT into BTC.
func BaseSymbol(symbol string) string { return strings.TrimSuffix(symbol, "USDT") }

// PairSymbol turns BTC, btc or BTCUSDT into BTCUSDT.
func PairSymbol(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "XYZ:")
	s = strings.NewReplacer("-", "", "_", "", "/", "").Replace(s)
	if s == "" || strings.HasSuffix(s, "USDT") {
		return s
	}
	return s + "USDT"
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
