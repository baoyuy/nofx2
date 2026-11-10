package kernel

import (
	"fmt"
	"time"

	"nofx/market"
)

// copyTradeSignalGate applies the signal-quality gates of follow/fade to an
// open on side, given the coin's copy-trade summary c (leaning one way).
// Each gate is off at its zero setting.
func (e *StrategyEngine) copyTradeSignalGate(c *CopyTradeCoin, ctx *Context, mode, side, symbol string) string {
	src := e.config.CoinSource
	src.NormalizeCopyTrade()
	if n := src.CopyTradeMinOpeners; n > 0 && c.leading().Openers < n {
		return fmt.Sprintf("copy-trade %s mode: only %d lead traders opened or added %s on %s in 24h (minimum %d); holding is not a fresh call",
			mode, c.leading().Openers, c.Direction, symbol, n)
	}
	if min := src.CopyTradeMinHitRate; min > 0 {
		hr, _ := ctx.CopyTradeCoverage.hitRate(c.Confidence, c.Direction)
		if hr.Samples < src.CopyTradeMinSamples {
			return fmt.Sprintf("copy-trade %s mode: only %d past %s-confidence %s signals were scored (need %d) to trust the %.0f%% hit-rate bar",
				mode, hr.Samples, hr.Band, c.Direction, src.CopyTradeMinSamples, min)
		}
		rate := hr.Rate
		if mode == "fade" {
			rate = 1 - rate // fading wins when they are wrong
		}
		if rate*100 < min {
			return fmt.Sprintf("copy-trade %s mode: past %s-confidence %s signals were right %.0f%% of %d times after 24h (for this mode %.0f%%), below the %.0f%% bar",
				mode, hr.Band, c.Direction, hr.Rate*100, hr.Samples, rate*100, min)
		}
	}
	if src.CopyTradeTrendFilter {
		if trend := e.trend4h(ctx, symbol); (trend == "down" && side == "long") || (trend == "up" && side == "short") {
			return fmt.Sprintf("copy-trade %s mode: the 4h trend on %s is %s, open %s against it rejected", mode, symbol, trend, side)
		}
	}
	return ""
}

// copyTradeVolumeGate refuses opens on coins too thin to trade.
func (e *StrategyEngine) copyTradeVolumeGate(ctx *Context, symbol string) string {
	src := e.config.CoinSource
	min := src.CopyTradeMinVolumeM
	if min <= 0 {
		return ""
	}
	vol, ok := volume24h(e.marketData(ctx, symbol))
	if !ok {
		return "" // unknown volume: the OI filter and the AI judge it
	}
	if vol/1e6 < min {
		return fmt.Sprintf("copy-trade liquidity: %s traded %.1fM USDT in 24h, below the %.0fM minimum", symbol, vol/1e6, min)
	}
	return ""
}

func (e *StrategyEngine) marketData(ctx *Context, symbol string) *market.Data {
	if ctx == nil || ctx.MarketDataMap == nil {
		return nil
	}
	if d := ctx.MarketDataMap[symbol]; d != nil {
		return d
	}
	return ctx.MarketDataMap[market.NormalizeFor(symbol, e.exchange)]
}

// volume24h estimates the last 24h traded value (quote) from the shortest
// timeframe whose bars cover a day, scaling up a series that covers less.
func volume24h(data *market.Data) (float64, bool) {
	if data == nil {
		return 0, false
	}
	// The finest series that covers a full day; else the one covering most.
	var best *market.TimeframeSeriesData
	var bestSpan, bestCovered time.Duration
	for tf, s := range data.TimeframeData {
		span := timeframeDuration(tf)
		if s == nil || len(s.Klines) < 2 || span <= 0 {
			continue
		}
		covered := span * time.Duration(len(s.Klines))
		full, bestFull := covered >= 24*time.Hour, bestCovered >= 24*time.Hour
		if best == nil || (full && !bestFull) || (full && bestFull && span < bestSpan) ||
			(!full && !bestFull && covered > bestCovered) {
			best, bestSpan, bestCovered = s, span, covered
		}
	}
	if best == nil {
		return 0, false
	}
	n := int(24 * time.Hour / bestSpan)
	if n < 1 {
		n = 1
	}
	bars := best.Klines
	if len(bars) > n {
		bars = bars[len(bars)-n:]
	}
	var sum float64
	for _, b := range bars {
		sum += b.Volume * b.Close
	}
	if covered := bestSpan * time.Duration(len(bars)); covered < 24*time.Hour {
		sum *= float64(24*time.Hour) / float64(covered)
	}
	return sum, sum > 0
}

// trend4h reads the 4h trend from EMA20 against EMA50 ("up", "down" or ""),
// falling back to the longer-term context.
func (e *StrategyEngine) trend4h(ctx *Context, symbol string) string {
	data := e.marketData(ctx, symbol)
	if data == nil {
		return ""
	}
	ema20, ema50 := 0.0, 0.0
	if s := data.TimeframeData["4h"]; s != nil && len(s.EMA20Values) > 0 && len(s.EMA50Values) > 0 {
		ema20, ema50 = s.EMA20Values[len(s.EMA20Values)-1], s.EMA50Values[len(s.EMA50Values)-1]
	} else if lt := data.LongerTermContext; lt != nil {
		ema20, ema50 = lt.EMA20, lt.EMA50
	}
	switch {
	case ema20 <= 0 || ema50 <= 0:
		return ""
	case ema20 > ema50*1.002:
		return "up"
	case ema20 < ema50*0.998:
		return "down"
	}
	return "" // flat: no filter
}

func timeframeDuration(tf string) time.Duration {
	switch tf {
	case "1m":
		return time.Minute
	case "3m":
		return 3 * time.Minute
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "30m":
		return 30 * time.Minute
	case "1h":
		return time.Hour
	case "2h":
		return 2 * time.Hour
	case "4h":
		return 4 * time.Hour
	case "6h":
		return 6 * time.Hour
	case "1d":
		return 24 * time.Hour
	}
	return 0
}
