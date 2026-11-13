package explosive

import (
	"math"
	"sort"
)

// Weights of the four score parts. They are an initial guess: the ranking is
// recorded and measured against forward returns before anything trades on it.
const (
	wSpeed     = 0.30
	wVolume    = 0.25
	wOI        = 0.25
	wStructure = 0.20
)

const (
	// A 5m close-to-close move of speedFull average ranges scores full.
	speedFull = 1.8
	// Volume at volumeFull times the 20-bar median scores full.
	volumeFull = 3.5
	// Open interest +3% in an hour scores full.
	oiFull = 0.012
	// A worse-than-average open-interest drop of this size scores zero.
	oiDrain = 0.05

	// Gates and thresholds.
	MinVolume24h    = 20_000_000 // USDT of 24h turnover
	MaxDistanceHigh = 8.0        // % below the 24h high
	fundingHot      = 0.0005     // 0.05% per 8h: longs are crowded
	distanceChase   = 8.0        // % above EMA20
	rsiHot          = 85.0
	minBars5m       = 25
	minBars1h       = 120
	// maxBarsSinceBreakout is how long after a breakout a coin still counts
	// as "starting" rather than already running.
	maxBarsSinceBreakout = 2
	breakoutLookback     = 20
)

// Tags are machine keys; the page translates them.
const (
	TagBreakout   = "breakout"
	TagVolume     = "volume"
	TagOI         = "oi"
	TagAligned    = "aligned"
	TagHoldEMA    = "hold_ema"
	TagFundingHot = "funding_hot"
	TagChase      = "chase"
	TagFading     = "fading"
)

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func closes(bars []Kline) []float64 {
	out := make([]float64, len(bars))
	for i, b := range bars {
		out[i] = b.Close
	}
	return out
}

// atrPct is the average true range of the closed bars, as percent of price.
func atrPct(bars []Kline, period int) float64 {
	if len(bars) < period+1 {
		return 0
	}
	var sum float64
	for i := len(bars) - period; i < len(bars); i++ {
		tr := bars[i].High - bars[i].Low
		prev := bars[i-1].Close
		tr = math.Max(tr, math.Max(math.Abs(bars[i].High-prev), math.Abs(bars[i].Low-prev)))
		sum += tr
	}
	atr := sum / float64(period)
	if p := bars[len(bars)-1].Close; p > 0 {
		return atr / p * 100
	}
	return 0
}

// ema is the exponential moving average of the last n values.
func ema(values []float64, n int) float64 {
	if len(values) < n || n <= 0 {
		return 0
	}
	k := 2.0 / float64(n+1)
	out := mean(values[:n])
	for _, v := range values[n:] {
		out = v*k + out*(1-k)
	}
	return out
}

// rsi is the simple relative strength index over the last n closed bars.
func rsi(values []float64, n int) float64 {
	if len(values) < n+1 {
		return 50
	}
	var gain, loss float64
	for i := len(values) - n; i < len(values); i++ {
		d := values[i] - values[i-1]
		if d >= 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	if gain+loss == 0 {
		return 50
	}
	return 100 * gain / (gain + loss)
}

// median returns the middle value of a copy.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	m := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[m]
	}
	return (cp[m-1] + cp[m]) / 2
}

// volumeRatio is the last closed bar's volume over the median of the twenty
// before it. One is "normal".
func volumeRatio(bars []Kline) float64 {
	if len(bars) < 6 {
		return 1
	}
	last := bars[len(bars)-1].Volume
	var prev []float64
	for i := len(bars) - 21; i < len(bars)-1; i++ {
		if i >= 0 {
			prev = append(prev, bars[i].Volume)
		}
	}
	base := median(prev)
	if base <= 0 {
		return 1
	}
	return last / base
}

// breakoutBars counts closed bars since price first held above the prior
// breakoutLookback bars' high, or -1 when there was no breakout lately.
func breakoutBars(bars []Kline) int {
	for i := len(bars) - 1; i >= 1 && i >= len(bars)-10; i-- {
		from := i - breakoutLookback
		if from < 0 {
			break
		}
		high := 0.0
		for _, b := range bars[from:i] {
			high = math.Max(high, b.High)
		}
		if bars[i].Close > high {
			return len(bars) - 1 - i
		}
	}
	return -1
}

// structureScore reads the 1h trend: alignment, a fresh breakout and whether
// price still holds the short EMA.
func structureScore(hourly []Kline, price float64) (score float64, tags []string, e20, e60, e120 float64) {
	cl := closes(hourly)
	e20, e60, e120 = ema(cl, 20), ema(cl, 60), ema(cl, 120)
	if e20 > e60 && e60 > e120 {
		score += 0.4
		tags = append(tags, TagAligned)
	} else if e20 > e60 {
		score += 0.2
	}
	if b := breakoutBars(hourly); b >= 0 && b <= maxBarsSinceBreakout+3 {
		score += 0.3
		tags = append(tags, TagBreakout)
	}
	if e20 > 0 && price >= e20 {
		score += 0.3
		tags = append(tags, TagHoldEMA)
	}
	return clamp01(score), tags, e20, e60, e120
}

// oiScore credits open interest that grows with price: fresh money entering.
// Falling open interest means the move is mostly short covering and does not
// last.
func oiScore(change float64) float64 {
	if change > 0 {
		return clamp01(change / oiFull)
	}
	return math.Max(0, 0.3*(1+change/oiDrain))
}

// Evaluate scores one coin. ok is false when there is not enough data.
func Evaluate(in Input) (Row, bool) {
	if in.Price <= 0 || len(in.Bars5m) < minBars5m {
		return Row{}, false
	}
	cl5 := closes(in.Bars5m)
	atr := atrPct(in.Bars5m, 14)
	last := cl5[len(cl5)-1]
	ret5 := 0.0
	if prev := cl5[len(cl5)-2]; prev > 0 {
		ret5 = (last/prev - 1) * 100
	}
	ret15 := 0.0
	if prev := cl5[len(cl5)-4]; prev > 0 {
		ret15 = (last/prev - 1) * 100
	}
	vr := volumeRatio(in.Bars5m)
	speed := clamp01((ret5 / math.Max(atr, 0.05)) / speedFull)
	volScore := clamp01((vr - 1) / (volumeFull - 1))
	// Unknown open interest is neutral, not zero: a failed request must not
	// sink an otherwise good coin.
	oi := 0.5
	if in.OIKnown {
		oi = oiScore(in.OIChange1h)
	}

	structure, tags := 0.3, []string(nil)
	var e20, e60, e120 float64
	if len(in.Bars1h) >= minBars1h {
		var stags []string
		structure, stags, e20, e60, e120 = structureScore(in.Bars1h, in.Price)
		tags = append(tags, stags...)
	}

	dist := 0.0
	if e20 > 0 {
		dist = (in.Price - e20) / e20 * 100
	}
	// A fresh breakout is allowed to look overbought on RSI: that is what a
	// breakout is. The RSI discount only applies once the move is older.
	since := breakoutBars(in.Bars1h)
	fresh := since >= 0 && since <= maxBarsSinceBreakout+3
	penalty := 1.0
	if in.Funding > fundingHot {
		penalty *= 0.8
		tags = append(tags, TagFundingHot)
	}
	if dist > distanceChase {
		// Chasing is the failure mode this board exists to avoid: a coin
		// already far above its short EMA has to fall well below a fresh
		// breakout to rank.
		penalty *= 0.45
		tags = append(tags, TagChase)
	}
	if !fresh {
		if r := rsi(closes(in.Bars1h), 14); r > rsiHot {
			penalty *= 0.7
		}
	}
	if vr >= 1.5 {
		tags = append(tags, TagVolume)
	}
	if in.OIKnown && in.OIChange1h >= 0.01 {
		tags = append(tags, TagOI)
	}

	high24 := 0.0
	if n := len(in.Bars1h); n > 0 {
		for i := max(0, n-24); i < n; i++ {
			high24 = math.Max(high24, in.Bars1h[i].High)
		}
	}
	distHigh := 0.0
	if high24 > 0 {
		distHigh = (high24 - in.Price) / high24 * 100
	}

	score := 100 * (wSpeed*speed + wVolume*volScore + wOI*oi + wStructure*structure) * penalty

	state := StateAccelerating
	switch {
	case in.Funding > fundingHot || dist > distanceChase:
		state = StateOverheated
	case in.OIChange1h < 0 && vr < 1:
		state = StateFading
		tags = append(tags, TagFading)
	case since >= 0 && since <= maxBarsSinceBreakout && vr >= 1.5:
		state = StateStarting
	}

	return Row{
		Symbol: in.Symbol, Price: in.Price, Change24h: in.Change24h, Change5m: ret5, Change15m: ret15,
		Volume24h: in.Volume24h, VolRatio: vr, OIChange1h: in.OIChange1h, OIKnown: in.OIKnown, Funding: in.Funding,
		ATRPct: atr, DistanceHigh: distHigh, EMA20: e20, EMA60: e60, EMA120: e120,
		Score: score, Speed: speed, Volume: volScore, OpenInt: oi, Structure: structure, Penalty: penalty,
		State: state, Tags: tags,
	}, true
}
