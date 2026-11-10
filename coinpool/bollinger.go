// Package coinpool screens Binance USDT-M perpetuals for the coin pool page.
// Screeners only read public market data and never trade.
package coinpool

import "math"

const (
	bollPeriod  = 20
	bollWidth   = 2.0
	squeezeLook = 40 // bars searched for the squeeze before the expansion
	historyLook = 120
	slopeBars   = 5

	// freshBars: a horn more than this many bars past its squeeze is treated
	// as already run (15m = 3h, 1h = 12h, 4h = 2 days).
	freshBars = 12
	// maxSqueezePctile: the squeeze must be among the tightest 35% of the
	// market's own recent bandwidths to count as a real squeeze.
	maxSqueezePctile = 0.35
	// Stage "starting": bands just began widening off a very tight squeeze
	// and price is pressing on one band.
	startMinExpansion = 1.15
	openMinExpansion  = 1.5
	startMaxBars      = 8
	startMaxPctile    = 0.25
)

// HornResult is the Bollinger "horn" (squeeze then expansion) reading of one
// market. Stage: starting (just lifting off a tight squeeze), opening (bands
// widening in both directions, still fresh), mature (wide or long past the
// squeeze), closing (bands contracting again), none (no expansion).
// Only starting and opening match: the screen looks for horns that are just
// forming, not ones that have already run.
type HornResult struct {
	Match         bool    `json:"match"`
	Direction     string  `json:"direction"` // long | short | neutral
	Score         float64 `json:"score"`     // 0-100
	Stage         string  `json:"stage"`
	Expansion     float64 `json:"expansion"`      // current bandwidth / squeeze bandwidth
	SqueezePctile float64 `json:"squeeze_pctile"` // squeeze bandwidth rank in history, 0 = tightest
	PercentB      float64 `json:"percent_b"`      // close position inside the bands
	BarsSince     int     `json:"bars_since"`     // bars since the bands lifted off the squeeze
	Bandwidth     float64 `json:"bandwidth"`
}
type bands struct{ mid, upper, lower, width []float64 }

func bollinger(closes []float64) bands {
	n := len(closes)
	b := bands{make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)}
	for i := bollPeriod - 1; i < n; i++ {
		var sum, sq float64
		for _, v := range closes[i-bollPeriod+1 : i+1] {
			sum += v
		}
		mean := sum / bollPeriod
		for _, v := range closes[i-bollPeriod+1 : i+1] {
			sq += (v - mean) * (v - mean)
		}
		sd := math.Sqrt(sq / bollPeriod)
		b.mid[i], b.upper[i], b.lower[i] = mean, mean+bollWidth*sd, mean-bollWidth*sd
		if mean > 0 {
			b.width[i] = (b.upper[i] - b.lower[i]) / mean
		}
	}
	return b
}

// AnalyzeHorn reads closed candles, oldest first. It needs at least
// bollPeriod+squeezeLook bars.
func AnalyzeHorn(closes []float64) HornResult {
	out := HornResult{Direction: "neutral", Stage: "none"}
	n := len(closes)
	if n < bollPeriod+squeezeLook {
		return out
	}
	b := bollinger(closes)
	last := n - 1
	first := bollPeriod - 1

	// Squeeze: the tightest bandwidth in the recent window, before the last bar.
	lo := last - squeezeLook
	if lo < first {
		lo = first
	}
	sqIdx := lo
	for i := lo; i <= last-1; i++ {
		if b.width[i] < b.width[sqIdx] {
			sqIdx = i
		}
	}
	squeeze := b.width[sqIdx]
	if squeeze <= 0 {
		return out
	}
	out.Bandwidth = b.width[last]
	out.Expansion = b.width[last] / squeeze
	// Freshness counts from lift-off: the last bar still inside the squeeze
	// (within 15% of its low), not the low itself, which can sit deep in a
	// long flat range.
	liftoff := sqIdx
	for i := sqIdx; i <= last-1; i++ {
		if b.width[i] <= squeeze*1.15 {
			liftoff = i
		}
	}
	out.BarsSince = last - liftoff

	// How tight was the squeeze compared with this market's own history?
	hist := last - historyLook
	if hist < first {
		hist = first
	}
	below, total := 0, 0
	for i := hist; i <= last; i++ {
		total++
		if b.width[i] < squeeze {
			below++
		}
	}
	out.SqueezePctile = float64(below) / float64(total)

	if span := b.upper[last] - b.lower[last]; span > 0 {
		out.PercentB = (closes[last] - b.lower[last]) / span
	}
	// The horn: upper band rising while the lower band falls.
	ref := last - slopeBars
	upperUp := b.upper[last] > b.upper[ref]
	lowerDown := b.lower[last] < b.lower[ref]
	diverging := upperUp && lowerDown
	peak := b.width[sqIdx]
	for i := sqIdx; i <= last; i++ {
		if b.width[i] > peak {
			peak = b.width[i]
		}
	}
	widening := b.width[last] >= b.width[last-1] && b.width[last] >= peak*0.98
	// Early lift-off: two rising bars of bandwidth, the band on the
	// breakout side moving outward, price pressing on that band.
	risingTwice := b.width[last] > b.width[last-1] && b.width[last-1] > b.width[last-2]
	pushUp := out.PercentB >= 0.8 && b.upper[last] > b.upper[last-2]
	pushDown := out.PercentB <= 0.2 && b.lower[last] < b.lower[last-2]
	fresh := out.BarsSince <= freshBars

	switch {
	case out.Expansion < startMinExpansion:
		out.Stage = "none"
	case out.Expansion < openMinExpansion:
		if risingTwice && (pushUp || pushDown) && out.BarsSince <= startMaxBars && out.SqueezePctile <= startMaxPctile {
			out.Stage = "starting"
		} else {
			out.Stage = "none"
		}
	case widening && diverging && fresh:
		out.Stage = "opening"
	case widening || b.width[last] >= peak*0.85:
		out.Stage = "mature"
	default:
		out.Stage = "closing"
	}

	midUp := b.mid[last] > b.mid[ref]
	switch {
	case out.Stage == "starting" && pushUp:
		out.Direction = "long"
	case out.Stage == "starting" && pushDown:
		out.Direction = "short"
	case out.Stage == "starting":
	case out.PercentB >= 0.6 && midUp:
		out.Direction = "long"
	case out.PercentB <= 0.4 && !midUp:
		out.Direction = "short"
	}

	out.Match = (out.Stage == "starting" || out.Stage == "opening") &&
		out.Direction != "neutral" && out.SqueezePctile <= maxSqueezePctile
	out.Score = hornScore(out, diverging)
	return out
}

// hornScore favours horns that are just forming: freshness 35, squeeze
// tightness 25, direction clarity 20, bands diverging 10, early expansion 10
// (full credit by 1.75x; a wider horn earns nothing extra). Mature and
// closing horns keep 40% so they sort below every fresh one.
func hornScore(r HornResult, diverging bool) float64 {
	if r.Stage == "none" {
		return 0
	}
	score := 35 * (1 - clamp(float64(r.BarsSince-1)/freshBars))
	score += 25 * (1 - clamp(r.SqueezePctile/maxSqueezePctile))
	if r.Direction == "long" {
		score += 20 * clamp((r.PercentB-0.5)/0.5)
	} else if r.Direction == "short" {
		score += 20 * clamp((0.5-r.PercentB)/0.5)
	}
	if diverging {
		score += 10
	}
	score += 10 * clamp((r.Expansion-startMinExpansion)/0.6)
	if r.Stage == "mature" || r.Stage == "closing" {
		score *= 0.4
	}
	return math.Round(score)
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }
