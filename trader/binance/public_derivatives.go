package binance

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Public derivatives context for the AI coin scorer: open interest, its recent
// change, the long/short account ratio, taker buy/sell pressure, and a coarse
// BTC regime. All best-effort: a missing piece is left zero and never blocks.

type DerivativeInfo struct {
	OpenInterest     float64 // contracts/base units
	OIChangePct1h    float64 // %
	LongShortRatio   float64 // >1 = more long accounts
	LongAccountPct   float64 // 0-100
	ShortAccountPct  float64 // 0-100
	TakerBuySellRatio float64 // >1 = taker buying
	BasisPct          float64 // (mark - index)/index * 100
}

// GetDerivatives reads OI, OI change, long/short ratio and taker ratio.
// Any failed call leaves that field zero (the caller still gets the rest).
func GetDerivatives(ctx context.Context, symbol string) DerivativeInfo {
	var d DerivativeInfo

	// Open interest change over the last two 1h buckets.
	var hist []struct {
		SumOpenInterest string `json:"sumOpenInterest"`
		Timestamp       int64  `json:"timestamp"`
	}
	if err := publicGet(ctx, "/futures/data/openInterestHist?period=1h&limit=2&symbol="+url.QueryEscape(symbol), &hist); err == nil && len(hist) >= 2 {
		prev, last := parseF(hist[0].SumOpenInterest), parseF(hist[len(hist)-1].SumOpenInterest)
		d.OpenInterest = last
		if prev > 0 {
			d.OIChangePct1h = (last - prev) / prev * 100
		}
	} else {
		var oi struct {
			OpenInterest string `json:"openInterest"`
		}
		if err := publicGet(ctx, "/fapi/v1/openInterest?symbol="+url.QueryEscape(symbol), &oi); err == nil {
			d.OpenInterest = parseF(oi.OpenInterest)
		}
	}

	var lsr []struct {
		LongShortRatio string `json:"longShortRatio"`
		LongAccount    string `json:"longAccount"`
		ShortAccount   string `json:"shortAccount"`
	}
	if err := publicGet(ctx, "/futures/data/globalLongShortAccountRatio?period=1h&limit=1&symbol="+url.QueryEscape(symbol), &lsr); err == nil && len(lsr) > 0 {
		d.LongShortRatio = parseF(lsr[0].LongShortRatio)
		d.LongAccountPct = parseF(lsr[0].LongAccount) * 100
		d.ShortAccountPct = parseF(lsr[0].ShortAccount) * 100
	}

	var tk []struct {
		BuySellRatio string `json:"buySellRatio"`
	}
	if err := publicGet(ctx, "/futures/data/takerlongshortRatio?period=1h&limit=1&symbol="+url.QueryEscape(symbol), &tk); err == nil && len(tk) > 0 {
		d.TakerBuySellRatio = parseF(tk[0].BuySellRatio)
	}

	var prem struct {
		MarkPrice  string `json:"markPrice"`
		IndexPrice string `json:"indexPrice"`
	}
	if err := publicGet(ctx, "/fapi/v1/premiumIndex?symbol="+url.QueryEscape(symbol), &prem); err == nil {
		m, ix := parseF(prem.MarkPrice), parseF(prem.IndexPrice)
		if ix > 0 {
			d.BasisPct = (m - ix) / ix * 100
		}
	}
	return d
}

// GetBTCRegime returns a one-line description of the market regime from BTC 1h
// candles: direction vs a short MA plus the 24h change. Empty on failure.
func GetBTCRegime(ctx context.Context) string {
	k, err := GetPublicKlines(ctx, "BTCUSDT", "1h", 30)
	if err != nil || len(k) < 21 {
		return ""
	}
	last := k[len(k)-1].Close
	var sum float64
	for _, c := range k[len(k)-20:] {
		sum += c.Close
	}
	ma := sum / 20
	dir := "range"
	switch {
	case last > ma*1.005:
		dir = "uptrend"
	case last < ma*0.995:
		dir = "downtrend"
	}
	chg := 0.0
	if n := len(k) - 25; n >= 0 && k[n].Close > 0 {
		chg = (last - k[n].Close) / k[n].Close * 100
	}
	return fmt.Sprintf("BTC 1h %s (close %.4f vs MA20 %.4f), ~24h change %+.2f%%", dir, last, ma, chg)
}

// Slow-perp funding helper for logs is unnecessary here; keep time import used.
var _ = time.Second
