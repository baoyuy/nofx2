package explosive

// detectReversal looks for the "kept falling, then turned" setup: a prior
// decline, a break of the recent swing high, a volume confirmation and
// momentum turning up. It is a watch signal, not a prediction.
func detectReversal(bars []Kline) (bool, int, []string, float64) {
	n := len(bars)
	if n < 40 {
		return false, 0, nil, 0
	}
	closes := make([]float64, n)
	highs := make([]float64, n)
	vols := make([]float64, n)
	for i, b := range bars {
		closes[i], highs[i], vols[i] = b.Close, b.High, b.Volume
	}
	price := closes[n-1]
	ma20 := smaSeries(closes, 20)

	// 1) Prior decline / base: spent time below MA20, or drew down from the
	// recent high, or is still down over 24h.
	below := 0
	for i := n - 24; i < n; i++ {
		if i >= 0 && ma20[i] > 0 && closes[i] < ma20[i] {
			below++
		}
	}
	peak := 0.0
	for i := n - 48; i < n; i++ {
		if i >= 0 && highs[i] > peak {
			peak = highs[i]
		}
	}
	drawdown := 0.0
	if peak > 0 {
		drawdown = 1 - price/peak
	}
	chg24 := 0.0
	if n >= 25 && closes[n-25] > 0 {
		chg24 = (price/closes[n-25] - 1) * 100
	}
	priorDown := below >= 6 || drawdown >= 0.08 || chg24 <= -3

	// 2) Structure break: close above the highest high of the prior 12 bars.
	neck := 0.0
	for i := n - 13; i < n-1; i++ {
		if i >= 0 && highs[i] > neck {
			neck = highs[i]
		}
	}
	breakUp := neck > 0 && price > neck

	// 3) Volume confirmation on the breakout bar.
	avgVol := 0.0
	for i := n - 21; i < n-1; i++ {
		if i >= 0 {
			avgVol += vols[i]
		}
	}
	avgVol /= 20
	volSpike := 0.0
	if avgVol > 0 {
		volSpike = vols[n-1] / avgVol
	}
	volOK := volSpike >= 1.5

	// 4) Momentum turning up: MACD golden cross, or RSI recovering from low.
	_, _, dif, dea := macdSeries(closes)
	cross := false
	for i := n - 3; i < n; i++ {
		if i >= 1 && dif[i] > dea[i] && dif[i-1] <= dea[i-1] {
			cross = true
		}
	}
	rsiNow := rsiSeries(closes, 14)
	rsiRecover := false
	if n >= 15 {
		low := 100.0
		for i := n - 10; i < n; i++ {
			if i >= 14 && rsiNow[i] < low {
				low = rsiNow[i]
			}
		}
		if low < 35 && rsiNow[n-1] > 40 {
			rsiRecover = true
		}
	}
	momOK := cross || rsiRecover

	score := 0
	reasons := make([]string, 0, 4)
	if priorDown {
		score++
		if drawdown >= 0.08 {
			reasons = append(reasons, "自高点回撤≥8%")
		} else {
			reasons = append(reasons, "前期弱势/横盘")
		}
	}
	if breakUp {
		score++
		reasons = append(reasons, "突破近12根高点")
	}
	if volOK {
		score++
		reasons = append(reasons, "放量")
	}
	if momOK {
		score++
		if cross {
			reasons = append(reasons, "MACD 金叉")
		} else {
			reasons = append(reasons, "RSI 回升")
		}
	}
	return score >= 3, score, reasons, neck
}

func smaSeries(v []float64, p int) []float64 {
	out := make([]float64, len(v))
	sum := 0.0
	for i, x := range v {
		sum += x
		if i >= p {
			sum -= v[i-p]
		}
		if i >= p-1 {
			out[i] = sum / float64(p)
		}
	}
	return out
}

func emaSeries(v []float64, p int) []float64 {
	out := make([]float64, len(v))
	if len(v) == 0 {
		return out
	}
	k := 2.0 / float64(p+1)
	out[0] = v[0]
	for i := 1; i < len(v); i++ {
		out[i] = v[i]*k + out[i-1]*(1-k)
	}
	return out
}

// macd returns the EMA12/EMA26, DIF and DEA series.
func macdSeries(v []float64) ([]float64, []float64, []float64, []float64) {
	e12, e26 := emaSeries(v, 12), emaSeries(v, 26)
	dif := make([]float64, len(v))
	for i := range v {
		dif[i] = e12[i] - e26[i]
	}
	return e12, e26, dif, emaSeries(dif, 9)
}

func rsiSeries(v []float64, p int) []float64 {
	out := make([]float64, len(v))
	if len(v) <= p {
		return out
	}
	var gain, loss float64
	for i := 1; i <= p; i++ {
		d := v[i] - v[i-1]
		if d >= 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	gain /= float64(p)
	loss /= float64(p)
	out[p] = rsiSeriesValue(gain, loss)
	for i := p + 1; i < len(v); i++ {
		d := v[i] - v[i-1]
		g, l := 0.0, 0.0
		if d >= 0 {
			g = d
		} else {
			l = -d
		}
		gain = (gain*float64(p-1) + g) / float64(p)
		loss = (loss*float64(p-1) + l) / float64(p)
		out[i] = rsiSeriesValue(gain, loss)
	}
	return out
}

func rsiSeriesValue(gain, loss float64) float64 {
	if loss == 0 {
		return 100
	}
	rs := gain / loss
	return 100 - 100/(1+rs)
}
