package explosive

import (
	"math"
	"sort"
	"time"
)

// Weights of the four score parts. They are an initial guess: the ranking is
// recorded and measured against forward returns before anything trades on it.
const (
	wSpeed     = 0.25
	wVolume    = 0.20
	wOI        = 0.20
	wStructure = 0.15
	wRel       = 0.20
)

const (
	// A 5m close-to-close move of speedFull average ranges scores full.
	speedFull = 1.2
	// Volume at volumeFull times the 20-bar median scores full.
	volumeFull = 3.5
	// Open interest +3% in an hour scores full.
	oiFull = 0.012
	// A worse-than-average open-interest drop of this size scores zero.
	oiDrain = 0.05

	// Gates and thresholds.
	MinVolume24h    = 20_000_000 // USDT of 24h turnover
	MaxDistanceHigh = 8.0        // % below the 24h high: a coin already rolled over
	fundingHot      = 0.001      // 0.1% per 8h: longs are crowded
	distanceChase   = 20.0       // % above EMA20: a vertical move, not a trend
	relFull         = 50.0       // 50 points over BTC's 24h change scores full
	oiDrainRisk     = -0.01      // 1h open-interest drain that marks a move suspect
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
	TagOIDrain    = "oi_drain"
	TagFading     = "fading"
	TagFalling    = "falling"
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
	// A forming bar built from the live price has no volume yet; compare the
	// last closed bar instead of reporting zero.
	end := len(bars)
	if bars[end-1].Volume <= 0 {
		end--
	}
	if end < 6 {
		return 1
	}
	last := bars[end-1].Volume
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
	// Speed is signed: a coin selling off must lose points, not merely stop
	// gaining them.
	speedRaw := math.Max(-1, math.Min(1, (ret5/math.Max(atr, 0.05))/speedFull))
	speed := clamp01(speedRaw)
	volScore := clamp01((vr - 1) / (volumeFull - 1))
	// Unknown open interest is neutral, not zero: a failed request must not
	// sink an otherwise good coin.
	oi := 0.5
	if in.OIKnown {
		oi = oiScore(in.OIChange1h)
	}

	// Always a non-nil slice: a nil one marshals to null and the page maps it.
	// Multi-timeframe context, recorded for later tuning.
	ch1, ch4, slope := 0.0, 0.0, 0.0
	takerRatio, efficiency := 0.0, 0.0
	if len(in.Bars1h) >= 5 {
		cl1 := closes(in.Bars1h)
		last := cl1[len(cl1)-1]
		if prev := cl1[len(cl1)-2]; prev > 0 {
			ch1 = (last/prev - 1) * 100
		}
		if prev := cl1[len(cl1)-5]; prev > 0 {
			ch4 = (last/prev - 1) * 100
		}
		nowE := ema(cl1, 20)
		beforeE := ema(cl1[:len(cl1)-3], 20)
		if beforeE > 0 {
			slope = (nowE/beforeE - 1) * 100
		}
	}
	if n := len(in.Bars5m); n >= 13 {
		from := n - 12
		var buyVol, totalVol, path float64
		for i := from; i < n; i++ {
			buyVol += in.Bars5m[i].TakerBuy
			totalVol += in.Bars5m[i].Volume
			if i > from {
				path += math.Abs(in.Bars5m[i].Close - in.Bars5m[i-1].Close)
			}
		}
		if totalVol > 0 && buyVol > 0 {
			takerRatio = clamp01(buyVol / totalVol)
		}
		if path > 0 {
			efficiency = clamp01(math.Abs(in.Bars5m[n-1].Close-in.Bars5m[from].Close) / path)
		}
	}

	structure, tags := 0.3, []string{}
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
	risk := 1.0
	if in.Funding > fundingHot {
		risk *= 0.7
		tags = append(tags, TagFundingHot)
	}
	if dist > distanceChase {
		risk *= 0.7
		tags = append(tags, TagChase)
	}
	if in.OIKnown && in.OIChange1h < oiDrainRisk {
		risk *= 0.85
		tags = append(tags, TagOIDrain)
	}
	if !fresh {
		if r := rsi(closes(in.Bars1h), 14); r > rsiHot {
			risk *= 0.8
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

	rel := clamp01((in.Change24h - in.BTCChange24h) / relFull)
	strength := 100 * (wSpeed*speedRaw + wVolume*volScore + wOI*oi + wStructure*structure + wRel*rel)
	strength = math.Max(0, strength)
	if speedRaw < -0.3 {
		tags = append(tags, TagFalling)
	}
	score := strength * risk

	state := StateAccelerating
	switch {
	case speedRaw <= -0.5:
		state = StateFalling
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
		Change1h: ch1, Change4h: ch4, EMASlope: slope, TakerBuyRatio: takerRatio, RiseEfficiency: efficiency,
		ATRPct: atr, DistanceHigh: distHigh, EMA20: e20, EMA60: e60, EMA120: e120,
		Strength: strength, Risk: risk, Score: score, Speed: speed, Volume: volScore, OpenInt: oi,
		Structure: structure, RelStrength: rel, State: state, Tags: tags,
	}, true
}

// formingBar returns bars with the current, not yet closed 5m candle appended
// or updated from the live price. Volume is left at zero: it is not known.
func formingBar(bars []Kline, price float64, interval time.Duration, now time.Time) []Kline {
	if len(bars) == 0 || price <= 0 {
		return bars
	}
	last := bars[len(bars)-1]
	step := int64(interval / time.Millisecond)
	out := make([]Kline, len(bars), len(bars)+1)
	copy(out, bars)
	if now.UnixMilli() >= last.Time+step {
		// After a long outage do not invent a bar across the gap: the jump
		// from an old close would read as a huge move.
		if now.UnixMilli() > last.Time+3*step {
			return bars
		}
		return append(out, Kline{
			Time: last.Time + step, Open: last.Close,
			High: math.Max(last.Close, price), Low: math.Min(last.Close, price), Close: price,
		})
	}
	cur := &out[len(out)-1]
	cur.Close = price
	cur.High = math.Max(cur.High, price)
	if cur.Low == 0 {
		cur.Low = price
	} else {
		cur.Low = math.Min(cur.Low, price)
	}
	return out
}
