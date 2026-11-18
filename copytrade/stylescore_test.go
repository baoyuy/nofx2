package copytrade

import (
	"math"
	"testing"
	"time"

	"nofx/store"
)

func TestStyleDecayFollowsTheTradersOwnClock(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	held := func(hours float64) Position {
		return Position{OpenedAt: now.Add(-time.Duration(hours * float64(time.Hour))).UnixMilli()}
	}
	scalp := Style{Kind: StyleScalp, MedianHoldHours: 2}
	long := Style{Kind: StyleLong, MedianHoldHours: 100}
	for _, c := range []struct {
		name  string
		p     Position
		style Style
		want  float64
	}{
		{"scalp within its median", held(1), scalp, 1},
		{"scalp at three medians", held(6), scalp, 0.2},
		{"scalp long past", held(48), scalp, 0.2},
		{"long-term holder after two days", held(48), long, 1},
		{"long-term holder at two medians", held(200), long, 0.6},
	} {
		if got := styleDecay(c.p, c.style, now); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: decay %.3f, want %.3f", c.name, got, c.want)
		}
	}
	// No measured clock: the shared age curve applies.
	p := held(48)
	if got, want := styleDecay(p, Style{Kind: StyleFewSamples}, now), holdFactor(p, now); got != want {
		t.Errorf("few samples: decay %.3f, want holdFactor %.3f", got, want)
	}
}

func TestStyleReadingsWeighsTradersByTheirOwnRecord(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	trader := func(id, side string, style Style, acc Accuracy, highFreq bool) Snapshot {
		return Snapshot{
			Follow: store.CopyTradeFollow{PortfolioID: id, MarginBalance: 10000},
			Style:  style, StyleAccuracy: acc, HighFreq: highFreq,
			Positions: []Position{{Symbol: "XUSDT", Side: side, Qty: 1, EntryPrice: 5000, EntryValue: 5000,
				OpenedAt: now.Add(-time.Hour).UnixMilli()}},
		}
	}
	swing := Style{Kind: StyleSwing, MedianHoldHours: 30}
	good := Accuracy{Calls: 40, Hits: 30, Rate: 0.75}
	bad := Accuracy{Calls: 40, Hits: 12, Rate: 0.3}

	// Two poor records long against one good record short: the good one wins.
	r := StyleReadings([]Snapshot{
		trader("a", "long", swing, bad, false),
		trader("b", "long", swing, bad, false),
		trader("c", "short", swing, good, false),
	}, now)["XUSDT"]
	if r.Direction != "short" || r.Traders != 3 {
		t.Fatalf("direction %s with %d traders, want short with 3", r.Direction, r.Traders)
	}

	// Averaging down and high-frequency traders do not vote.
	r = StyleReadings([]Snapshot{
		trader("a", "long", Style{Kind: StyleMartingale, MedianHoldHours: 1}, good, false),
		trader("b", "long", swing, good, true),
		trader("c", "short", swing, Accuracy{}, false),
	}, now)["XUSDT"]
	if r.Direction != "short" || r.Traders != 1 {
		t.Fatalf("direction %s with %d traders, want short with 1", r.Direction, r.Traders)
	}
}
