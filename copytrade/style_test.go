package copytrade

import (
	"testing"
	"time"
)

const hourMs = int64(time.Hour / time.Millisecond)

// roundTrip is a long position opened at openH, optionally averaged at
// addPrices, then closed at closeH (all in hours).
func roundTrip(symbol string, openH, closeH int64, entry, exit float64, addPrices ...float64) []Fill {
	fills := []Fill{{Time: openH * hourMs, Symbol: symbol, Side: "BUY", PositionSide: "BOTH", Qty: 1, Price: entry}}
	qty, pnl := 1.0, exit-entry
	for i, p := range addPrices {
		fills = append(fills, Fill{Time: openH*hourMs + int64(i+1)*60000, Symbol: symbol, Side: "BUY",
			PositionSide: "BOTH", Qty: 1, Price: p})
		qty++
		pnl += exit - p
	}
	return append(fills, Fill{Time: closeH * hourMs, Symbol: symbol, Side: "SELL", PositionSide: "BOTH", Qty: qty,
		Price: exit, RealizedPnL: pnl})
}

// repeats calls one round trip per period, span hours apart, so they do not
// overlap.
func repeats(n int, span int64, trip func(i int64) []Fill) []Fill {
	var out []Fill
	for i := int64(0); i < int64(n); i++ {
		out = append(out, trip(i*span)...)
	}
	return out
}

func TestStyleOfHoldingTime(t *testing.T) {
	cases := []struct {
		name   string
		hold   int64 // hours from open to close
		want   string
		median float64
	}{
		{"scalp", 0, StyleScalp, 0},
		{"swing", 24, StyleSwing, 24},
		{"long", 24 * 10, StyleLong, 240},
	}
	for _, c := range cases {
		// Seven round trips: the first of each symbol is not counted.
		fills := repeats(7, c.hold+48, func(i int64) []Fill {
			return roundTrip("BTCUSDT", i, i+c.hold, 100, 101)
		})
		got := StyleOf(fills)
		if got.Kind != c.want || got.Trades != 6 || got.MedianHoldHours != c.median {
			t.Fatalf("%s: %+v", c.name, got)
		}
	}
}

// Averaging into a losing position is what makes a martingale, whatever its
// holding time: one add per trade, made underwater, however the trade ends.
func TestStyleOfMartingale(t *testing.T) {
	fills := repeats(7, 48, func(i int64) []Fill {
		// Opened at 100, averaged at 90, closed at 85: every trip loses.
		return roundTrip("BTCUSDT", i, i+24, 100, 85, 90)
	})
	got := StyleOf(fills)
	if got.Kind != StyleMartingale || got.Trades != 6 {
		t.Fatalf("martingale: %+v", got)
	}
	if got.AddsPerTrade != 1 || got.AdverseAddShare != 1 || got.LossExitShare != 1 {
		t.Fatalf("measures: %+v", got)
	}
	// The usual one: averages down twice and waits for the bounce, so every
	// trip closes green. Winning exits must not hide it.
	green := repeats(7, 48, func(i int64) []Fill {
		// Opened at 100, averaged at 90 and 80, closed at 95.
		return roundTrip("BTCUSDT", i, i+24, 100, 95, 90, 80)
	})
	if got := StyleOf(green); got.Kind != StyleMartingale || got.LossExitShare != 0 || got.AddsPerTrade != 2 {
		t.Fatalf("averaging down with green exits: %+v", got)
	}
	// Adding to a position that is winning is conviction, not martingale.
	better := repeats(7, 48, func(i int64) []Fill {
		// Opened at 100, added at 110 (in profit), closed at 120.
		return roundTrip("BTCUSDT", i, i+24, 100, 120, 110)
	})
	if got := StyleOf(better); got.Kind != StyleSwing {
		t.Fatalf("adding in profit is not martingale: %+v", got)
	}
}

func TestStyleOfFewSamplesAndNothing(t *testing.T) {
	few := repeats(3, 48, func(i int64) []Fill { return roundTrip("BTCUSDT", i, i+1, 100, 101) })
	if got := StyleOf(few); got.Kind != StyleFewSamples || got.Trades != 2 {
		t.Fatalf("three round trips, one dropped: %+v", got)
	}
	if got := StyleOf(nil); got.Kind != StyleFewSamples || got.Trades != 0 {
		t.Fatalf("no fills: %+v", got)
	}
}

// The first round trip of a symbol is dropped because it may have been opened
// before the stored fills: counting it would read as a very short hold.
func TestStyleSkipsTheFirstRoundTrip(t *testing.T) {
	fills := roundTrip("ETHUSDT", 0, 1, 100, 101)
	for i := int64(0); i < 6; i++ {
		fills = append(fills, roundTrip("ETHUSDT", 100+i*48, 100+i*48+24, 100, 101)...)
	}
	got := StyleOf(fills)
	if got.Trades != 6 || got.MedianHoldHours != 24 {
		t.Fatalf("the dropped trip must not pull the median down: %+v", got)
	}
}
