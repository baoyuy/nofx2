package marketboard

import (
	"context"
	"errors"
	"sort"
	"time"
)

// Factor directions.
const (
	bullish = "bullish"
	bearish = "bearish"
	neutral = "neutral"
)

type DirectionMarket struct {
	MarketType string `json:"marketType"`
	MarketID   string `json:"marketId"`
	Symbol     string `json:"symbol"`
}

// DirectionItem is one market on the direction board.
type DirectionItem struct {
	Market           DirectionMarket   `json:"market"`
	Symbol           string            `json:"symbol"`
	Bias             string            `json:"bias"`
	DirectionScore   float64           `json:"directionScore"`
	BullishCount     int               `json:"bullishCount"`
	BearishCount     int               `json:"bearishCount"`
	NeutralCount     int               `json:"neutralCount"`
	FactorDirections map[string]string `json:"factorDirections"`
	MarkPrice        float64           `json:"markPrice"`
	Rank             int               `json:"rank"`
	OIRank           int               `json:"oiRank"`
	FactorAsOf       int64             `json:"factorAsOf"`

	oiValue   float64
	volume    float64
	fetchedAt int64
}

type DirectionBoard struct {
	Band         int             `json:"band"`
	UniverseSize int             `json:"universeSize"`
	RankBy       string          `json:"rankBy"`
	Source       string          `json:"source"`
	AsOf         int64           `json:"asOf"`
	Items        []DirectionItem `json:"items"`
}

// Factors scores four independent reads of the last 24 hourly candles:
//   - trend:    close vs EMA20 vs EMA50
//   - momentum: 24h return beyond ±1%
//   - flow:     taker buy share of volume beyond 52% / 48%
//   - oi:       open interest up >2% confirms the price move's side
//
// oiChange is the fractional 24h open interest change, used only when oiKnown.
func Factors(candles []Candle, oiChange float64, oiKnown bool) map[string]string {
	f := map[string]string{"trend": neutral, "momentum": neutral, "flow": neutral, "oi": neutral}
	n := len(candles)
	if n < 25 {
		return f
	}
	closes := make([]float64, n)
	for i, k := range candles {
		closes[i] = k.Close
	}
	last := closes[n-1]
	e20, e50 := ema(closes, 20), ema(closes, 50)
	switch {
	case last > e20 && e20 > e50:
		f["trend"] = bullish
	case last < e20 && e20 < e50:
		f["trend"] = bearish
	}
	ret := last/closes[n-25] - 1
	switch {
	case ret > 0.01:
		f["momentum"] = bullish
	case ret < -0.01:
		f["momentum"] = bearish
	}
	var buy, total float64
	for _, k := range candles[n-24:] {
		buy += k.TakerBuyQuote
		total += k.QuoteVolume
	}
	if total > 0 {
		switch share := buy / total; {
		case share > 0.52:
			f["flow"] = bullish
		case share < 0.48:
			f["flow"] = bearish
		}
	}
	if oiKnown && oiChange > 0.02 {
		if ret > 0 {
			f["oi"] = bullish
		} else if ret < 0 {
			f["oi"] = bearish
		}
	}
	return f
}

// ema seeds with the first value, which is close enough for a direction read.
func ema(values []float64, period int) float64 {
	k := 2 / float64(period+1)
	e := values[0]
	for _, v := range values[1:] {
		e = v*k + e*(1-k)
	}
	return e
}

// Score turns factor directions into a bias and a 0–100 strength. A bias needs
// at least two more agreeing factors than opposing ones.
func Score(f map[string]string) (bias string, score float64, bull, bear, neut int) {
	for _, d := range f {
		switch d {
		case bullish:
			bull++
		case bearish:
			bear++
		default:
			neut++
		}
	}
	diff := bull - bear
	bias = neutral
	if diff >= 2 {
		bias = bullish
	} else if diff <= -2 {
		bias = bearish
	}
	if diff < 0 {
		diff = -diff
	}
	return bias, float64(diff) / float64(len(f)) * 100, bull, bear, neut
}

// Direction builds the bull/bear board for the most traded perpetuals,
// strongest signals first.
func (c *Client) Direction(ctx context.Context) (DirectionBoard, error) {
	v, err := c.cachedDo(ctx, "direction", func(ctx context.Context) (any, error) {
		tickers, err := c.topByVolume(ctx, universe)
		if err != nil {
			return nil, err
		}
		list, failed := c.scoreTickers(ctx, tickers)
		if len(tickers) == 0 || failed > len(tickers)/2 {
			return nil, errors.New("Binance candles unavailable for the direction board")
		}
		return rankDirection(list, universe), nil
	})
	if err != nil {
		return DirectionBoard{}, err
	}
	return v.(DirectionBoard), nil
}

// MaxDirectionSymbols caps an explicit symbol list (the AI's candidates plus
// open positions never exceed this).
const MaxDirectionSymbols = 30

// DirectionFor scores only the given perpetuals, e.g. the coins a trader's AI
// is looking at this cycle. Each symbol is cached on its own, so a changing
// candidate set reuses earlier work and the cache stays bounded by the number
// of listed markets. Symbols Binance does not list are left out.
func (c *Client) DirectionFor(ctx context.Context, symbols []string) DirectionBoard {
	tickers := make([]Ticker, 0, len(symbols))
	seen := map[string]bool{}
	for _, raw := range symbols {
		pair := PairSymbol(raw)
		if pair == "" || seen[pair] || len(tickers) >= MaxDirectionSymbols {
			continue
		}
		seen[pair] = true
		tickers = append(tickers, Ticker{Symbol: pair})
	}
	items := make([]DirectionItem, len(tickers))
	ok := make([]bool, len(tickers))
	forEach(tickers, func(i int, t Ticker) error {
		v, err := c.cachedDo(ctx, "direction1:"+t.Symbol, func(ctx context.Context) (any, error) {
			list, failed := c.scoreTickers(ctx, []Ticker{t})
			if failed > 0 || len(list) == 0 {
				return nil, errors.New("candles unavailable")
			}
			return list[0], nil
		})
		if err != nil {
			return err
		}
		items[i], ok[i] = v.(DirectionItem), true
		return nil
	})
	var list []DirectionItem
	for i := range items {
		if ok[i] {
			list = append(list, items[i])
		}
	}
	return rankDirection(list, len(tickers))
}

// scoreTickers computes the factor directions for each ticker and reports how
// many could not be scored.
func (c *Client) scoreTickers(ctx context.Context, tickers []Ticker) ([]DirectionItem, int) {
	items := make([]DirectionItem, len(tickers))
	ok := make([]bool, len(tickers))
	failed := forEach(tickers, func(i int, t Ticker) error {
		candles, err := c.klines(ctx, t.Symbol, "1h", 60)
		if err != nil || len(candles) < 25 {
			return errors.New("candles unavailable")
		}
		// Open interest is optional: the board still works without it.
		oi, oiErr := c.openInterestHistory(ctx, t.Symbol, "1h", 25)
		var oiChange, oiValue float64
		oiKnown := oiErr == nil && len(oi) >= 2 && oi[0] > 0
		if oiKnown {
			oiValue = oi[len(oi)-1]
			oiChange = oiValue/oi[0] - 1
		}
		f := Factors(candles, oiChange, oiKnown)
		bias, score, bull, bear, neut := Score(f)
		base := BaseSymbol(t.Symbol)
		items[i] = DirectionItem{
			Market: DirectionMarket{MarketType: "core_perp", MarketID: t.Symbol, Symbol: base},
			Symbol: base, Bias: bias, DirectionScore: score,
			BullishCount: bull, BearishCount: bear, NeutralCount: neut, FactorDirections: f,
			MarkPrice: candles[len(candles)-1].Close, FactorAsOf: candles[len(candles)-1].OpenTime,
			fetchedAt: time.Now().UnixMilli(),
			oiValue:   oiValue, volume: t.QuoteVolume,
		}
		ok[i] = true
		return nil
	})
	var list []DirectionItem
	for i := range items {
		if ok[i] {
			list = append(list, items[i])
		}
	}
	return list, failed
}

// rankDirection assigns OI ranks and orders the board strongest signal first.
func rankDirection(list []DirectionItem, band int) DirectionBoard {
	byOI := make([]int, len(list))
	for i := range byOI {
		byOI[i] = i
	}
	sort.SliceStable(byOI, func(a, b int) bool { return list[byOI[a]].oiValue > list[byOI[b]].oiValue })
	for r, i := range byOI {
		list[i].OIRank = r + 1
	}
	sort.SliceStable(list, func(a, b int) bool {
		if list[a].DirectionScore != list[b].DirectionScore {
			return list[a].DirectionScore > list[b].DirectionScore
		}
		return list[a].volume > list[b].volume
	})
	for i := range list {
		list[i].Rank = i + 1
	}
	if list == nil {
		list = []DirectionItem{}
	}
	asOf := time.Now().UnixMilli()
	for _, item := range list {
		if item.fetchedAt > 0 && item.fetchedAt < asOf {
			asOf = item.fetchedAt
		}
	}
	return DirectionBoard{Band: band, UniverseSize: len(list), RankBy: "directionScore", Source: "binance",
		AsOf: asOf, Items: list}
}
