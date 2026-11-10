package marketboard

import (
	"context"
	"errors"
	"math"
	"time"
)

const (
	heatmapBars     = 168 // 7 days of 1h candles
	heatmapBand     = 0.15
	heatmapBins     = 60
	heatmapHalfLife = 36.0 // hours; older entries are more likely closed
)

// leverageMix is the assumed spread of leverage among open positions.
var leverageMix = []struct{ lev, weight float64 }{{10, 0.35}, {25, 0.3}, {50, 0.2}, {100, 0.15}}

type HeatmapBin struct {
	Px               float64 `json:"px"`
	BucketStartPrice float64 `json:"bucketStartPrice"`
	BucketEndPrice   float64 `json:"bucketEndPrice"`
	LongCost         float64 `json:"longCost"`
	ShortCost        float64 `json:"shortCost"`
	LongLiq          float64 `json:"longLiq"`
	ShortLiq         float64 `json:"shortLiq"`
}

type HeatmapMarket struct {
	MarketType  string `json:"marketType"`
	MarketID    string `json:"marketId"`
	Symbol      string `json:"symbol"`
	DisplayName string `json:"displayName"`
	IsActive    bool   `json:"isActive"`
}

type HeatmapState struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type HeatmapData struct {
	Market      HeatmapMarket `json:"market"`
	MarkPrice   float64       `json:"markPrice"`
	BinStep     float64       `json:"binStep"`
	Bins        []HeatmapBin  `json:"bins"`
	Cost        HeatmapState  `json:"cost"`
	Liquidation HeatmapState  `json:"liquidation"`
	Source      string        `json:"source"`
	Estimated   bool          `json:"estimated"`
	// ScaledToOI is true when the levels are scaled to current open interest.
	ScaledToOI bool `json:"scaledToOI"`
}

type HeatmapResponse struct {
	Data HeatmapData `json:"data"`
}

// Heatmap estimates where positions were opened and where they would be
// liquidated, from the last 7 days of hourly candles: each candle's taker buy
// notional counts as long entries at its typical price and taker sell notional
// as short entries, decayed by age. Liquidation levels assume leverageMix and
// drop positions the price has already run through. Totals are scaled to the
// current open interest when Binance reports it. Binance does not publish
// positions, so the result is an estimate.
func (c *Client) Heatmap(ctx context.Context, rawSymbol string) (HeatmapResponse, error) {
	symbol := PairSymbol(rawSymbol)
	base := BaseSymbol(symbol)
	resp := HeatmapResponse{Data: HeatmapData{
		Market: HeatmapMarket{MarketType: "core_perp", MarketID: symbol, Symbol: base, DisplayName: base},
		Bins:   []HeatmapBin{}, Source: "binance", Estimated: true,
	}}
	listed, err := c.topByVolume(ctx, 0)
	if err != nil {
		return resp, err
	}
	known := false
	for _, t := range listed {
		known = known || t.Symbol == symbol
	}
	if !known {
		resp.Data.Cost = HeatmapState{State: "unsupported", Reason: "not a Binance USDT-M crypto perpetual"}
		resp.Data.Liquidation = resp.Data.Cost
		return resp, nil
	}
	resp.Data.Market.IsActive = true
	v, err := c.cachedDo(ctx, "heatmap:"+symbol, func(ctx context.Context) (any, error) {
		candles, err := c.klines(ctx, symbol, "1h", heatmapBars)
		if err != nil || len(candles) < 24 {
			return nil, errors.New("Binance candles unavailable for the heatmap")
		}
		var oi float64
		if hist, err := c.openInterestHistory(ctx, symbol, "1h", 1); err == nil && len(hist) == 1 {
			oi = hist[0]
		}
		return BuildHeatmap(candles, oi, time.UnixMilli(candles[len(candles)-1].OpenTime)), nil
	})
	if err != nil {
		return resp, err
	}
	d := v.(HeatmapData)
	d.Market = resp.Data.Market
	resp.Data = d
	return resp, nil
}

// BuildHeatmap bins the estimate around the last close. now is the newest
// candle's open time; decay is measured from it.
func BuildHeatmap(candles []Candle, openInterest float64, now time.Time) HeatmapData {
	mark := candles[len(candles)-1].Close
	lo, hi := mark*(1-heatmapBand), mark*(1+heatmapBand)
	step := (hi - lo) / heatmapBins
	bins := make([]HeatmapBin, heatmapBins)
	for i := range bins {
		start := lo + float64(i)*step
		bins[i] = HeatmapBin{Px: start + step/2, BucketStartPrice: start, BucketEndPrice: start + step}
	}
	at := func(px float64) int {
		if px < lo || px >= hi {
			return -1
		}
		return int((px - lo) / step)
	}
	// Lowest low and highest high after each candle, to tell whether a
	// liquidation level has already been hit.
	n := len(candles)
	minAfter, maxAfter := make([]float64, n), make([]float64, n)
	low, high := math.Inf(1), math.Inf(-1)
	for i := n - 1; i >= 0; i-- {
		minAfter[i], maxAfter[i] = low, high
		low, high = math.Min(low, candles[i].Low), math.Max(high, candles[i].High)
	}
	var longTotal, shortTotal float64
	for i, k := range candles {
		age := now.Sub(time.UnixMilli(k.OpenTime)).Hours()
		w := math.Pow(0.5, math.Max(0, age)/heatmapHalfLife)
		long, short := k.TakerBuyQuote*w, (k.QuoteVolume-k.TakerBuyQuote)*w
		longTotal += long
		shortTotal += short
		px := (k.High + k.Low + k.Close) / 3
		if b := at(px); b >= 0 {
			bins[b].LongCost += long
			bins[b].ShortCost += short
		}
		for _, m := range leverageMix {
			// Liquidation sits a little inside 1/leverage for maintenance margin.
			move := 1/m.lev - 0.005
			if longLiq := px * (1 - move); longLiq < minAfter[i] {
				if b := at(longLiq); b >= 0 {
					bins[b].LongLiq += long * m.weight
				}
			}
			if shortLiq := px * (1 + move); shortLiq > maxAfter[i] {
				if b := at(shortLiq); b >= 0 {
					bins[b].ShortLiq += short * m.weight
				}
			}
		}
	}
	d := HeatmapData{MarkPrice: mark, BinStep: step, Bins: bins, Source: "binance", Estimated: true,
		Cost: HeatmapState{State: "estimated"}, Liquidation: HeatmapState{State: "estimated"}}
	if total := longTotal + shortTotal; openInterest > 0 && total > 0 {
		// Open interest counts each position once; long and short sides are
		// each the full open interest.
		scale := 2 * openInterest / total
		for i := range bins {
			bins[i].LongCost *= scale
			bins[i].ShortCost *= scale
			bins[i].LongLiq *= scale
			bins[i].ShortLiq *= scale
		}
		d.ScaledToOI = true
	}
	return d
}
