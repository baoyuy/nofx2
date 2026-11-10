package market

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"
)

// TrendCoin is one entry of the free trend board. Score is a 0-100 relative
// rank among the scanned uptrending markets, not a return forecast.
type TrendCoin struct {
	Pair        string  `json:"pair"`
	Score       float64 `json:"score"`
	Return7D    float64 `json:"return_7d"`
	EMASlope    float64 `json:"ema_slope"`
	VolumeRatio float64 `json:"volume_ratio"`
}
type TrendBoard struct {
	Coins     []TrendCoin `json:"coins"`
	Scanned   int         `json:"scanned"`
	FetchedAt time.Time   `json:"fetched_at"`
	Sources   []string    `json:"sources"`
}

const (
	trendScanLimit  = 60  // most liquid Binance USDT crypto perpetuals
	trendBoardLimit = 30  // board size
	trendCandles    = 100 // 4h candles, about 16 days
)

// trendMetrics derives trend inputs from closed 4h candles. ok is false unless
// the market is in an uptrend: close > EMA20 > EMA50 and EMA50 rising.
func trendMetrics(closes, volumes []float64) (ret7d, slope, volRatio float64, ok bool) {
	n := len(closes)
	if n < 60 || len(volumes) != n {
		return 0, 0, 0, false
	}
	ema := func(period int) []float64 {
		k := 2 / float64(period+1)
		out := make([]float64, n)
		out[0] = closes[0]
		for i := 1; i < n; i++ {
			out[i] = closes[i]*k + out[i-1]*(1-k)
		}
		return out
	}
	fast, slow := ema(20), ema(50)
	last := closes[n-1]
	if !(last > fast[n-1] && fast[n-1] > slow[n-1] && slow[n-1] > slow[n-7]) {
		return 0, 0, 0, false
	}
	// 42 x 4h = 7 days; 6 x 4h = 1 day.
	if closes[n-43] <= 0 || fast[n-7] <= 0 {
		return 0, 0, 0, false
	}
	ret7d = last/closes[n-43] - 1
	slope = fast[n-1]/fast[n-7] - 1
	var recent, base float64
	for i := n - 6; i < n; i++ {
		recent += volumes[i]
	}
	for i := n - 48; i < n-6; i++ {
		base += volumes[i]
	}
	if base <= 0 {
		return 0, 0, 0, false
	}
	volRatio = (recent / 6) / (base / 42)
	return ret7d, slope, volRatio, true
}

// rankTrend scores uptrending coins by percentile rank: 40% 7-day return,
// 40% EMA20 slope, 20% volume expansion. Highest score first.
func rankTrend(coins []TrendCoin) []TrendCoin {
	n := len(coins)
	if n == 0 {
		return coins
	}
	percentile := func(value func(TrendCoin) float64) []float64 {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return value(coins[idx[a]]) < value(coins[idx[b]]) })
		out := make([]float64, n)
		for rank, i := range idx {
			if n == 1 {
				out[i] = 1
			} else {
				out[i] = float64(rank) / float64(n-1)
			}
		}
		return out
	}
	r := percentile(func(c TrendCoin) float64 { return c.Return7D })
	s := percentile(func(c TrendCoin) float64 { return c.EMASlope })
	v := percentile(func(c TrendCoin) float64 { return c.VolumeRatio })
	for i := range coins {
		coins[i].Score = math.Round((0.4*r[i] + 0.4*s[i] + 0.2*v[i]) * 100)
	}
	sort.SliceStable(coins, func(a, b int) bool {
		if coins[a].Score != coins[b].Score {
			return coins[a].Score > coins[b].Score
		}
		return coins[a].Pair < coins[b].Pair
	})
	return coins
}

// TrendBoard scans the most liquid Binance USDT crypto perpetuals using public
// candles only. It replaces the paid Claw402 AI500 board; no wallet is used.
func (c *Client) TrendBoard(ctx context.Context) (TrendBoard, error) {
	out := TrendBoard{Coins: []TrendCoin{}}
	list, err := c.Markets(ctx, "binance")
	if err != nil {
		return out, err
	}
	candidates := []string{}
	markets := append([]MarketSymbol(nil), list.Markets...)
	sort.SliceStable(markets, func(a, b int) bool { return markets[a].Volume24H > markets[b].Volume24H })
	for _, m := range markets {
		if m.Category == "crypto" {
			candidates = append(candidates, m.Symbol)
		}
		if len(candidates) == trendScanLimit {
			break
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	failed := 0
	for _, symbol := range candidates {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			rows, e := c.Candles(ctx, CandleQuery{Venue: "binance", Symbol: symbol, Interval: "4h", Limit: trendCandles})
			if e != nil || len(rows) < 2 {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			rows = rows[:len(rows)-1] // the last 4h candle is still forming
			closes := make([]float64, len(rows))
			volumes := make([]float64, len(rows))
			for i, row := range rows {
				closes[i], _ = strconv.ParseFloat(string(row.Close), 64)
				volumes[i], _ = strconv.ParseFloat(string(row.Volume), 64)
			}
			ret, slope, vol, ok := trendMetrics(closes, volumes)
			if !ok {
				return
			}
			mu.Lock()
			out.Coins = append(out.Coins, TrendCoin{Pair: symbol, Return7D: ret, EMASlope: slope, VolumeRatio: vol})
			mu.Unlock()
		}(symbol)
	}
	wg.Wait()
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if len(candidates) == 0 || failed > len(candidates)/2 {
		return out, errors.New("Binance candles unavailable for the trend board")
	}
	out.Coins = rankTrend(out.Coins)
	if len(out.Coins) > trendBoardLimit {
		out.Coins = out.Coins[:trendBoardLimit]
	}
	out.Scanned = len(candidates)
	out.FetchedAt = time.Now().UTC()
	out.Sources = []string{c.BinanceFutures + "/fapi/v1/ticker/24hr", c.BinanceFutures + "/fapi/v1/klines (4h)"}
	return out, nil
}
