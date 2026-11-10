package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nofx/internal/apischeduler"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Public Binance USDT-M perpetual market data. No API key; used by the
// market page, AI analysis and the paper simulator's chart.
//
// Data source: the futures API (fapi.binance.com), so the market page shows
// the same contracts, prices and gainers as the Binance futures app and the
// contracts strategies actually trade. fapi answers HTTP 451 from some
// hosting IP ranges; such a server needs a proxy (HTTPS_PROXY) or another
// host. Errors keep Binance's message so a 451 is easy to recognize.

type MarketKline struct {
	Time   int64   `json:"time"` // open time, unix seconds
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

type MarketTicker struct {
	Symbol          string  `json:"symbol"`
	LastPrice       float64 `json:"last_price"`
	MarkPrice       float64 `json:"mark_price"`
	PriceChange     float64 `json:"price_change"`
	PriceChangePct  float64 `json:"price_change_pct"`
	High24h         float64 `json:"high_24h"`
	Low24h          float64 `json:"low_24h"`
	Volume24h       float64 `json:"volume_24h"`
	QuoteVolume24h  float64 `json:"quote_volume_24h"`
	FundingRate     float64 `json:"funding_rate"`
	NextFundingTime int64   `json:"next_funding_time"`
}

// publicBaseURL is a var so tests can point it at a stub server.
var publicBaseURL = "https://fapi.binance.com"

var publicHTTPClient = &http.Client{Timeout: 15 * time.Second}

var (
	symbolsMu       sync.Mutex
	symbolsCache    []string
	symbolsCachedAt time.Time
)

// ValidMarketIntervals are the chart periods the market page offers.
var ValidMarketIntervals = map[string]bool{
	"1m": true, "3m": true, "5m": true, "15m": true, "30m": true,
	"1h": true, "2h": true, "4h": true, "6h": true, "12h": true,
	"1d": true, "3d": true, "1w": true,
}

func parseF(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func publicGet(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicBaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := apischeduler.Do(publicHTTPClient, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binance public data %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// GetPublicKlines returns up to limit candles, oldest first.
func GetPublicKlines(ctx context.Context, symbol, interval string, limit int) ([]MarketKline, error) {
	if !ValidMarketIntervals[interval] {
		return nil, fmt.Errorf("unsupported interval %q", interval)
	}
	if limit <= 0 || limit > 1500 {
		limit = 500
	}
	path := fmt.Sprintf("/fapi/v1/klines?symbol=%s&interval=%s&limit=%d", symbol, interval, limit)
	var rows [][]json.RawMessage
	if err := publicGet(ctx, path, &rows); err != nil {
		return nil, fmt.Errorf("binance klines: %w", err)
	}
	out := make([]MarketKline, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		var openTime int64
		var o, h, l, c, v string
		if err := json.Unmarshal(r[0], &openTime); err != nil {
			continue
		}
		if err := json.Unmarshal(r[1], &o); err != nil {
			continue
		}
		if err := json.Unmarshal(r[2], &h); err != nil {
			continue
		}
		if err := json.Unmarshal(r[3], &l); err != nil {
			continue
		}
		if err := json.Unmarshal(r[4], &c); err != nil {
			continue
		}
		if err := json.Unmarshal(r[5], &v); err != nil {
			continue
		}
		out = append(out, MarketKline{
			Time: openTime / 1000, Open: parseF(o), High: parseF(h),
			Low: parseF(l), Close: parseF(c), Volume: parseF(v),
		})
	}
	return out, nil
}

type futuresTicker24h struct {
	Symbol             string `json:"symbol"`
	LastPrice          string `json:"lastPrice"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
}

// GetPublicTicker combines 24h stats with mark price and funding rate.
func GetPublicTicker(ctx context.Context, symbol string) (*MarketTicker, error) {
	var s futuresTicker24h
	if err := publicGet(ctx, "/fapi/v1/ticker/24hr?symbol="+symbol, &s); err != nil {
		return nil, fmt.Errorf("binance 24h ticker: %w", err)
	}
	if s.Symbol == "" {
		return nil, fmt.Errorf("symbol %s not found", symbol)
	}
	last := parseF(s.LastPrice)
	t := &MarketTicker{
		Symbol: s.Symbol, LastPrice: last, MarkPrice: last,
		PriceChange: parseF(s.PriceChange), PriceChangePct: parseF(s.PriceChangePercent),
		High24h: parseF(s.HighPrice), Low24h: parseF(s.LowPrice),
		Volume24h: parseF(s.Volume), QuoteVolume24h: parseF(s.QuoteVolume),
	}
	// Funding is supplementary; the ticker is still useful without it.
	var prem struct {
		MarkPrice       string `json:"markPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
	}
	if err := publicGet(ctx, "/fapi/v1/premiumIndex?symbol="+symbol, &prem); err == nil {
		if mark := parseF(prem.MarkPrice); mark > 0 {
			t.MarkPrice = mark
		}
		t.FundingRate = parseF(prem.LastFundingRate)
		t.NextFundingTime = prem.NextFundingTime
	}
	return t, nil
}

// GetPublicSymbols lists trading USDT-margined crypto perpetuals, cached for
// an hour. Stock and commodity contracts are excluded.
func GetPublicSymbols(ctx context.Context) ([]string, error) {
	symbolsMu.Lock()
	defer symbolsMu.Unlock()
	if len(symbolsCache) > 0 && time.Since(symbolsCachedAt) < time.Hour {
		return symbolsCache, nil
	}
	var info struct {
		Symbols []struct {
			Symbol       string `json:"symbol"`
			Status       string `json:"status"`
			QuoteAsset   string `json:"quoteAsset"`
			ContractType string `json:"contractType"`
		} `json:"symbols"`
	}
	if err := publicGet(ctx, "/fapi/v1/exchangeInfo", &info); err != nil {
		return nil, fmt.Errorf("binance exchangeInfo: %w", err)
	}
	var out []string
	for _, s := range info.Symbols {
		if s.QuoteAsset == "USDT" && s.ContractType == "PERPETUAL" && s.Status == "TRADING" {
			out = append(out, s.Symbol)
		}
	}
	sort.Strings(out)
	symbolsCache, symbolsCachedAt = out, time.Now()
	return out, nil
}

// MarketTickerLite is one row of the coin list / gainers ranking.
type MarketTickerLite struct {
	Symbol         string  `json:"symbol"`
	LastPrice      float64 `json:"last_price"`
	PriceChangePct float64 `json:"price_change_pct"`
	QuoteVolume24h float64 `json:"quote_volume_24h"`
}

var (
	tickersMu       sync.Mutex
	tickersCache    []MarketTickerLite
	tickersCachedAt time.Time
)

// GetPublicTickers returns 24h stats for all trading USDT perpetuals.
// One Binance call covers every symbol; cached briefly so many open pages
// share it.
func GetPublicTickers(ctx context.Context) ([]MarketTickerLite, error) {
	tickersMu.Lock()
	defer tickersMu.Unlock()
	if len(tickersCache) > 0 && time.Since(tickersCachedAt) < 5*time.Second {
		return tickersCache, nil
	}
	symbols, err := GetPublicSymbols(ctx)
	if err != nil {
		return nil, err
	}
	trading := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		trading[s] = true
	}
	var stats []futuresTicker24h
	if err := publicGet(ctx, "/fapi/v1/ticker/24hr", &stats); err != nil {
		return nil, fmt.Errorf("binance 24h tickers: %w", err)
	}
	out := make([]MarketTickerLite, 0, len(symbols))
	for _, s := range stats {
		if !trading[s.Symbol] {
			continue
		}
		out = append(out, MarketTickerLite{
			Symbol: s.Symbol, LastPrice: parseF(s.LastPrice),
			PriceChangePct: parseF(s.PriceChangePercent), QuoteVolume24h: parseF(s.QuoteVolume),
		})
	}
	tickersCache, tickersCachedAt = out, time.Now()
	return out, nil
}

// NormalizeUSDTSymbol turns "btc", "BTC/USDT" or "btc-usdt" into "BTCUSDT".
func NormalizeUSDTSymbol(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.NewReplacer("/", "", "-", "", "_", "", " ", "").Replace(s)
	if s != "" && !strings.HasSuffix(s, "USDT") {
		s += "USDT"
	}
	return s
}

// MarketDepth is an L2 order book snapshot; levels are [price, qty].
// Bids are sorted best (highest) first, asks best (lowest) first.
type MarketDepth struct {
	Bids [][2]float64 `json:"bids"`
	Asks [][2]float64 `json:"asks"`
}

// GetPublicDepth returns the top limit levels per side (clamped to [5,50],
// default 20).
func GetPublicDepth(ctx context.Context, symbol string, limit int) (*MarketDepth, error) {
	if limit <= 0 {
		limit = 20
	} else if limit < 5 {
		limit = 5
	} else if limit > 50 {
		limit = 50
	}
	// Binance only accepts limit = 5, 10, 20, 50, ... (code -4021 otherwise),
	// so request the next valid size up and trim to the requested count.
	reqLimit := 50
	for _, v := range []int{5, 10, 20, 50} {
		if limit <= v {
			reqLimit = v
			break
		}
	}
	path := fmt.Sprintf("/fapi/v1/depth?symbol=%s&limit=%d", symbol, reqLimit)
	var raw struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := publicGet(ctx, path, &raw); err != nil {
		return nil, fmt.Errorf("binance depth: %w", err)
	}
	toLevels := func(rows [][]string) [][2]float64 {
		if len(rows) > limit {
			rows = rows[:limit]
		}
		out := make([][2]float64, 0, len(rows))
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			out = append(out, [2]float64{parseF(r[0]), parseF(r[1])})
		}
		return out
	}
	return &MarketDepth{Bids: toLevels(raw.Bids), Asks: toLevels(raw.Asks)}, nil
}

// GetPublicMarkPrices returns every perpetual's mark price by symbol, in one
// request.
func GetPublicMarkPrices(ctx context.Context) (map[string]float64, error) {
	var rows []struct {
		Symbol    string `json:"symbol"`
		MarkPrice string `json:"markPrice"`
	}
	if err := publicGet(ctx, "/fapi/v1/premiumIndex", &rows); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		if p := parseF(r.MarkPrice); p > 0 {
			out[r.Symbol] = p
		}
	}
	return out, nil
}
