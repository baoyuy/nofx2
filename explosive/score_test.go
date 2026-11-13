package explosive

import (
	"math"
	"testing"
)

// flatBars is n bars at price with a small range and constant volume.
func flatBars(n int, price, vol float64) []Kline {
	out := make([]Kline, n)
	for i := range out {
		out[i] = Kline{Time: int64(i), Open: price, High: price * 1.0005, Low: price * 0.9995, Close: price, Volume: vol}
	}
	return out
}

// trendBars rises by step percent per bar and ends with a breakout bar.
func trendBars(n int, price, step float64) []Kline {
	out := make([]Kline, n)
	p := price
	for i := range out {
		p *= 1 + step/100
		out[i] = Kline{Time: int64(i), Open: p, High: p * 1.001, Low: p * 0.999, Close: p, Volume: 1000}
	}
	// A fresh breakout on the last closed bar.
	last := &out[n-1]
	last.Close = out[n-2].Close * 1.02
	last.High = last.Close * 1.001
	last.Low = out[n-2].Close
	return out
}

// burst5m adds a volume and price burst to the last closed 5m bar.
func burst5m(bars []Kline) []Kline {
	last := &bars[len(bars)-1]
	prev := bars[len(bars)-2].Close
	last.Close = prev * 1.03
	last.High = last.Close * 1.001
	last.Low = prev
	last.Volume = bars[len(bars)-2].Volume * 6
	return bars
}

func strongInput() Input {
	hourly := trendBars(150, 100, 0.3)
	five := burst5m(flatBars(60, 100, 1000))
	return Input{
		Symbol: "TESTUSDT", Price: hourly[len(hourly)-1].Close, Change24h: 15, Volume24h: 50e6,
		Bars5m: five, Bars1h: hourly, OIChange1h: 0.03, OIKnown: true, Funding: 0,
	}
}

func TestEvaluateScoresAFreshBreakout(t *testing.T) {
	row, ok := Evaluate(strongInput())
	if !ok {
		t.Fatal("expected a row")
	}
	if row.Score < 80 {
		t.Fatalf("a textbook breakout should score high, got %.1f (%+v)", row.Score, row)
	}
	if row.State != StateStarting {
		t.Fatalf("state = %s, want starting", row.State)
	}
	for _, want := range []string{TagBreakout, TagAligned, TagHoldEMA, TagVolume, TagOI} {
		if !hasTag(row.Tags, want) {
			t.Fatalf("missing tag %s in %v", want, row.Tags)
		}
	}
	if row.VolRatio < 5 {
		t.Fatalf("volume ratio = %.2f", row.VolRatio)
	}
}

func TestEvaluatePenalisesChasing(t *testing.T) {
	in := strongInput()
	hours := trendBars(150, 100, 0.3)
	in.Bars1h = hours
	e20 := ema(closes(hours), 20)
	in.Price = e20 * 1.25 // 25% above the short EMA: vertical, not a trend
	row, _ := Evaluate(in)
	if row.Risk > 0.71 {
		t.Fatalf("chase discount missing: %+v", row)
	}
	if row.State != StateOverheated {
		t.Fatalf("state = %s, want overheated", row.State)
	}
	if !hasTag(row.Tags, TagChase) {
		t.Fatalf("chase tag missing: %v", row.Tags)
	}
}

func TestEvaluatePenalisesHotFunding(t *testing.T) {
	in := strongInput()
	in.Funding = 0.002 // 0.2% per 8h
	row, _ := Evaluate(in)
	if row.Risk > 0.71 || !hasTag(row.Tags, TagFundingHot) {
		t.Fatalf("funding penalty missing: %+v", row)
	}
	if row.State != StateOverheated {
		t.Fatalf("state = %s, want overheated", row.State)
	}
}

func TestRelativeStrengthRanksTheRealMover(t *testing.T) {
	big := strongInput()
	big.Change24h = 120
	big.BTCChange24h = 1.5
	small := strongInput()
	small.Change24h = 4
	small.BTCChange24h = 1.5
	a, _ := Evaluate(big)
	b, _ := Evaluate(small)
	if a.Strength <= b.Strength {
		t.Fatalf("a coin far ahead of BTC must score higher: %.1f vs %.1f", a.Strength, b.Strength)
	}
}

func TestFallingOpenInterestScoresLower(t *testing.T) {
	up := strongInput()
	up.OIChange1h = 0.03
	down := strongInput()
	down.OIChange1h = -0.03
	a, _ := Evaluate(up)
	b, _ := Evaluate(down)
	if b.Score >= a.Score {
		t.Fatalf("falling open interest must score lower: %.1f vs %.1f", b.Score, a.Score)
	}
	if b.OpenInt > 0.2 {
		t.Fatalf("an open-interest drain should barely score, got %.2f", b.OpenInt)
	}
	drained := strongInput()
	drained.OIChange1h = -0.05
	if row, _ := Evaluate(drained); row.OpenInt != 0 {
		t.Fatalf("a 5%% open-interest drain should score zero, got %.2f", row.OpenInt)
	}
}

func TestFadingNeedsNoVolumeAndNoOpenInterest(t *testing.T) {
	in := strongInput()
	in.OIChange1h = -0.02
	in.Bars5m = flatBars(60, 100, 1000)
	last := &in.Bars5m[len(in.Bars5m)-1]
	last.Volume = 400 // below the median
	row, _ := Evaluate(in)
	if row.State != StateFading || !hasTag(row.Tags, TagFading) {
		t.Fatalf("state = %s tags=%v, want fading", row.State, row.Tags)
	}
}

func TestEvaluateNeedsEnoughCandles(t *testing.T) {
	if _, ok := Evaluate(Input{Symbol: "X", Price: 1, Bars5m: flatBars(5, 1, 1)}); ok {
		t.Fatal("too few candles must not produce a row")
	}
}

func TestIndicators(t *testing.T) {
	bars := flatBars(30, 100, 1000)
	if got := atrPct(bars, 14); math.Abs(got-0.1) > 0.01 {
		t.Fatalf("atrPct = %.3f, want ~0.1", got)
	}
	if got := volumeRatio(burst5m(flatBars(30, 100, 1000))); got < 5 {
		t.Fatalf("volumeRatio = %.2f", got)
	}
	up := trendBars(150, 100, 0.3)
	if e20, e60, e120 := ema(closes(up), 20), ema(closes(up), 60), ema(closes(up), 120); !(e20 > e60 && e60 > e120) {
		t.Fatalf("EMAs not aligned: %.2f %.2f %.2f", e20, e60, e120)
	}
	if got := rsi(closes(up), 14); got < 90 {
		t.Fatalf("rsi of a steady rise = %.1f", got)
	}
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Fatalf("median = %v", got)
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
