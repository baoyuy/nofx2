package api

import (
	"math"

	"nofx/trader/binance"
)

// Indicator snapshot sent to the AI. Values are for the last closed bar and
// use the same standard periods the market page draws.
type indicatorSnapshot struct {
	MA7, MA25, MA99         float64
	EMA12, EMA26            float64
	MACD, MACDSignal, MACDH float64
	RSI6, RSI14             float64
	K, D, J                 float64
	BollUp, BollMid, BollDn float64
	ATR14                   float64
}

func closes(k []binance.MarketKline) []float64 {
	out := make([]float64, len(k))
	for i, c := range k {
		out[i] = c.Close
	}
	return out
}

func lastSMA(v []float64, n int) float64 {
	if len(v) < n || n <= 0 {
		return 0
	}
	sum := 0.0
	for _, x := range v[len(v)-n:] {
		sum += x
	}
	return sum / float64(n)
}

func emaSeries(v []float64, n int) []float64 {
	out := make([]float64, len(v))
	if len(v) == 0 {
		return out
	}
	k := 2 / float64(n+1)
	out[0] = v[0]
	for i := 1; i < len(v); i++ {
		out[i] = v[i]*k + out[i-1]*(1-k)
	}
	return out
}

func lastRSI(v []float64, n int) float64 {
	if len(v) <= n {
		return 0
	}
	var gain, loss float64
	for i := 1; i <= n; i++ {
		d := v[i] - v[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	gain, loss = gain/float64(n), loss/float64(n)
	for i := n + 1; i < len(v); i++ {
		d := v[i] - v[i-1]
		g, l := math.Max(d, 0), math.Max(-d, 0)
		gain = (gain*float64(n-1) + g) / float64(n)
		loss = (loss*float64(n-1) + l) / float64(n)
	}
	if loss == 0 {
		return 100
	}
	return 100 - 100/(1+gain/loss)
}

func computeIndicators(k []binance.MarketKline) indicatorSnapshot {
	c := closes(k)
	var s indicatorSnapshot
	s.MA7, s.MA25, s.MA99 = lastSMA(c, 7), lastSMA(c, 25), lastSMA(c, 99)

	e12, e26 := emaSeries(c, 12), emaSeries(c, 26)
	if len(c) > 0 {
		dif := make([]float64, len(c))
		for i := range c {
			dif[i] = e12[i] - e26[i]
		}
		dea := emaSeries(dif, 9)
		last := len(c) - 1
		s.EMA12, s.EMA26 = e12[last], e26[last]
		s.MACD, s.MACDSignal = dif[last], dea[last]
		s.MACDH = 2 * (dif[last] - dea[last]) // Binance-style histogram
	}
	s.RSI6, s.RSI14 = lastRSI(c, 6), lastRSI(c, 14)

	// KDJ(9,3,3)
	kv, dv := 50.0, 50.0
	for i := range k {
		if i < 8 {
			continue
		}
		hi, lo := k[i].High, k[i].Low
		for j := i - 8; j <= i; j++ {
			hi, lo = math.Max(hi, k[j].High), math.Min(lo, k[j].Low)
		}
		rsv := 50.0
		if hi > lo {
			rsv = (k[i].Close - lo) / (hi - lo) * 100
		}
		kv = (2*kv + rsv) / 3
		dv = (2*dv + kv) / 3
	}
	s.K, s.D, s.J = kv, dv, 3*kv-2*dv

	// BOLL(20,2)
	if len(c) >= 20 {
		mid := lastSMA(c, 20)
		var variance float64
		for _, x := range c[len(c)-20:] {
			variance += (x - mid) * (x - mid)
		}
		sd := math.Sqrt(variance / 20)
		s.BollMid, s.BollUp, s.BollDn = mid, mid+2*sd, mid-2*sd
	}

	// ATR(14), Wilder smoothing
	if len(k) > 14 {
		atr := 0.0
		for i := 1; i < len(k); i++ {
			tr := math.Max(k[i].High-k[i].Low, math.Max(math.Abs(k[i].High-k[i-1].Close), math.Abs(k[i].Low-k[i-1].Close)))
			if i <= 14 {
				atr += tr / 14
			} else {
				atr = (atr*13 + tr) / 14
			}
		}
		s.ATR14 = atr
	}
	return s
}
