package backtest

import (
	"math"
	"testing"
	"time"
)

func syntheticDay() (int64, []Bar) {
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, Beijing).UnixMilli()
	bars := make([]Bar, 288)
	for i := range bars {
		price := 100.0
		if i < 120 { // first 10 hours rise to 120
			price = 100 + 20*float64(i)/120
		} else if i < 240 {
			price = 120
		} else {
			price = 120 - 10*float64(i-240)/48
		}
		vol := 1000.0
		if i >= 60 && i < 96 {
			vol = 4000 // volume burst around hours 5-8
		}
		bars[i] = Bar{
			Time: day + int64(i)*5*60*1000, Open: price, High: price * 1.001,
			Low: price * 0.999, Close: price, Volume: vol, Quote: 1000, Taker: vol * 0.6,
		}
	}
	return day, bars
}

func TestBuildRowsComputesAndLabels(t *testing.T) {
	day, bars := syntheticDay()
	rows := BuildRows("TESTUSDT", bars, map[int64]float64{day: 0.01}, nil, nil, 0.05, 0)
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	var r *Row
	for i := range rows {
		if rows[i].Hour == 6 {
			r = &rows[i]
		}
	}
	if r == nil {
		t.Fatal("hour 6 missing")
	}
	if r.Price <= 100 || r.DayGain < 0.05 {
		t.Fatalf("price/gain: %+v", r)
	}
	if math.Abs(r.ElapsedH-6) > 1e-9 {
		t.Fatalf("elapsed %v", r.ElapsedH)
	}
	// The day closes at 110, so a price above it has negative remaining room.
	if r.Remaining >= 0 {
		t.Fatalf("remaining should be negative: %+v", r)
	}
	if r.MaxDD > 0 {
		t.Fatalf("max drawdown must be <= 0: %+v", r.MaxDD)
	}
	// Relative strength subtracts BTC's day gain.
	if math.Abs(r.RelStrength-(r.DayGain-0.01)) > 1e-9 {
		t.Fatalf("relative strength: %+v", r)
	}
	if r.TakerRatio <= 0 || r.TakerRatio > 1 {
		t.Fatalf("taker ratio: %v", r.TakerRatio)
	}
}

func TestBuildRowsSkipsIncompleteDays(t *testing.T) {
	day, bars := syntheticDay()
	partial := bars[:200] // the day is still running
	if rows := BuildRows("TESTUSDT", partial, map[int64]float64{day: 0.01}, nil, nil, 0.05, 0); len(rows) != 0 {
		t.Fatalf("a running day must not be labelled: %d rows", len(rows))
	}
}

func TestBandsAcceptQuantileEdges(t *testing.T) {
	rows := []Row{{Remaining: 0.1}, {Remaining: -0.1}, {Remaining: 0.2}}
	bs := Bands(rows, func(r Row) float64 { return r.Remaining }, []string{"Q1", "Q2", "Q3"}, []float64{-0.1, 0.1})
	if len(bs) != 3 || bs[2].Samples != 1 {
		t.Fatalf("quantile bands: %+v", bs)
	}
}

func TestSpearmanSigns(t *testing.T) {
	var rows []Row
	for i := 0; i < 20; i++ {
		rows = append(rows, Row{Remaining: float64(i) / 100, DayGain: float64(i)})
	}
	if ic := Spearman(rows, func(r Row) float64 { return r.DayGain }); ic < 0.99 {
		t.Fatalf("perfect positive IC, got %.2f", ic)
	}
	if ic := Spearman(rows, func(r Row) float64 { return -r.DayGain }); ic > -0.99 {
		t.Fatalf("perfect negative IC, got %.2f", ic)
	}
}

func TestBandsSplitOnThresholds(t *testing.T) {
	rows := []Row{
		{Remaining: 0.2, MaxDD: -0.01},
		{Remaining: 0.1, MaxDD: -0.05},
		{Remaining: -0.1, MaxDD: -0.02},
	}
	score := func(r Row) float64 { return r.Remaining * 100 }
	bs := Bands(rows, score, []string{"<1", "1-2", "2-5", "5-10", ">=10"}, []float64{0, 1, 2, 5, 10})
	if bs[4].Samples != 2 || bs[0].Samples != 1 {
		t.Fatalf("band split: %+v", bs)
	}
	if bs[4].Up != 1 || bs[4].Sustained != 0.5 {
		t.Fatalf("band stats: %+v", bs[4])
	}
	if bs[0].AvgRet >= 0 {
		t.Fatalf("band average: %+v", bs[0])
	}
}

func TestOIAndFundingAttach(t *testing.T) {
	day, bars := syntheticDay()
	hour := int64(6) * 3600 * 1000
	oi := map[int64]float64{
		day + hour - 3600*1000: 1000,
		day + hour:             1100,
	}
	fund := []Funding{{Time: day + hour - 4*3600*1000, Rate: 0.0001}, {Time: day + hour - 3600*1000, Rate: 0.0003}}
	rows := BuildRows("TESTUSDT", bars, map[int64]float64{day: 0.01}, oi, fund, 0.05, 0)
	var r *Row
	for i := range rows {
		if rows[i].Hour == 6 {
			r = &rows[i]
		}
	}
	if r == nil || !r.OIKnown {
		t.Fatalf("OI not attached: %+v", r)
	}
	if math.Abs(r.OIChange1h-0.10) > 1e-9 {
		t.Fatalf("OI change = %v", r.OIChange1h)
	}
	if math.Abs(r.Funding-0.0003) > 1e-12 {
		t.Fatalf("funding = %v", r.Funding)
	}
}

func TestSameHourExcessRemovesTheCommonMove(t *testing.T) {
	rows := []Row{
		{Day: 1, Hour: 5, Remaining: 0.10},
		{Day: 1, Hour: 5, Remaining: -0.02},
		{Day: 1, Hour: 5, Remaining: -0.08},
	}
	out := AddSameHourExcess(rows)
	// Mean is 0.0, so excess equals remaining here.
	if math.Abs(out[0].Excess-0.10) > 1e-9 || out[2].Excess >= 0 {
		t.Fatalf("excess: %+v", out)
	}
	// A uniformly rising hour gives zero excess to everyone.
	flat := AddSameHourExcess([]Row{{Day: 2, Hour: 3, Remaining: 0.05}, {Day: 2, Hour: 3, Remaining: 0.05}})
	if flat[0].Excess != 0 || flat[1].Excess != 0 {
		t.Fatalf("flat hour excess: %+v", flat)
	}
	if out[0].Hit != true || out[1].Hit != false {
		t.Fatalf("hit flag: %+v", out)
	}
}
