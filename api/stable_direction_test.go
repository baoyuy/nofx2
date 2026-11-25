package api

import (
	"testing"

	"nofx/store"
)

func f64(v float64) *float64 { return &v }

// The detail panel used to show "0 samples / insufficient" no matter how much
// history existed, because only the list endpoint computed the stable
// direction. Both now share stableCounts.
func TestStableCountsUsesMatureRowsAndSkipsNeutralMoves(t *testing.T) {
	// Distinct coins, so every row is an independent call (same-coin repeats
	// inside one window are deduplicated; see the test below).
	ai := []store.CopyTradeScoreOutcome{
		{Symbol: "AUSDT", Bias: "short", Ret4h: f64(0.02)},   // hit
		{Symbol: "BUSDT", Bias: "short", Ret4h: f64(-0.02)},  // miss
		{Symbol: "CUSDT", Bias: "long", Ret4h: f64(0.001)},   // inside the neutral band: skipped
		{Symbol: "DUSDT", Bias: "neutral", Ret4h: f64(0.05)}, // no direction: skipped
		{Symbol: "EUSDT", Bias: "long", Ret4h: nil},          // not mature: skipped
	}
	bench := f64(0.01)
	trades := []store.CopyTradeSignal{
		{Symbol: "AUSDT", Direction: "long", Ret4h: f64(0.03), Bench4h: bench},   // +3% vs +1% => hit
		{Symbol: "BUSDT", Direction: "long", Ret4h: f64(0.005), Bench4h: bench},  // under the market => miss
		{Symbol: "CUSDT", Direction: "short", Ret4h: f64(-0.04), Bench4h: bench}, // fell more than market => hit
		{Symbol: "DUSDT", Direction: "neutral", Ret4h: f64(0.09)},                // skipped
		{Symbol: "EUSDT", Direction: "long", Ret4h: nil},                         // skipped: would have panicked before
	}
	ah, an, th, tn := stableCounts(ai, trades)
	if an != 2 || ah != 1 {
		t.Fatalf("AI counts = %d/%d, want 1/2", ah, an)
	}
	if tn != 3 || th != 2 {
		t.Fatalf("trader counts = %d/%d, want 2/3", th, tn)
	}
}

// Both sources repeat the same call: coins are re-scored every interval and
// trader signals are recorded hourly. Counting each repeat as a new opinion
// made "30 samples" out of ~8 independent calls, and let a single unchanged
// run satisfy the 4-sample minimum.
func TestStableCountsCountsOneCallPerRunNotPerRecord(t *testing.T) {
	h := int64(3600)
	var ai []store.CopyTradeScoreOutcome
	var trades []store.CopyTradeSignal
	// The same coin, same side, re-recorded every hour for 8 hours.
	for i := int64(0); i < 8; i++ {
		ai = append(ai, store.CopyTradeScoreOutcome{Symbol: "AUSDT", Bias: "short",
			AnalyzedAt: i * h * 1000, Ret4h: f64(0.02)})
		trades = append(trades, store.CopyTradeSignal{Symbol: "AUSDT", Direction: "short",
			Hour: i * h, Ret4h: f64(-0.04), Bench4h: f64(0.0)})
	}
	ah, an, th, tn := stableCounts(ai, trades)
	// 8 hourly records spanning 7h cover two 4h windows.
	if an != 2 || ah != 2 {
		t.Fatalf("AI counts = %d/%d, want 2/2 (one call per 4h run)", ah, an)
	}
	if tn != 2 || th != 2 {
		t.Fatalf("trader counts = %d/%d, want 2/2 (one call per 4h run)", th, tn)
	}
}

// A pending signal (ret_4h NULL) must not crash the page: the old list code
// dereferenced it directly.
func TestStableCountsSurvivesPendingSignals(t *testing.T) {
	ah, an, th, tn := stableCounts(nil, []store.CopyTradeSignal{{Direction: "long", Ret4h: nil}})
	if ah+an+th+tn != 0 {
		t.Fatalf("pending rows must count for nothing, got %d/%d %d/%d", ah, an, th, tn)
	}
}

// BTC is the benchmark every other coin is measured against, so its own excess
// over itself is exactly 0 and every BTC call used to score as a miss (all 320
// BTC signals on the server did). It must be judged on its absolute move.
func TestStableCountsJudgesTheBenchmarkOnItsAbsoluteMove(t *testing.T) {
	own := f64(0.03) // BTC rose 3%; as the benchmark, bench == ret
	rows := []store.CopyTradeSignal{
		{Symbol: "BTCUSDT", Direction: "long", Ret4h: own, Bench4h: own},
		{Symbol: "BTCUSDT", Direction: "short", Ret4h: own, Bench4h: own},
	}
	_, _, th, tn := stableCounts(nil, rows)
	if tn != 2 {
		t.Fatalf("samples = %d, want 2", tn)
	}
	if th != 1 {
		t.Fatalf("hits = %d, want 1: the long was right and the short was wrong on BTC's own move", th)
	}
}

// A non-benchmark coin is still judged on excess over BTC, unchanged.
func TestStableCountsStillUsesExcessForOtherCoins(t *testing.T) {
	bench := f64(0.03) // BTC +3%
	rows := []store.CopyTradeSignal{
		// Rose 1%, but less than BTC: a long on it did NOT beat the market.
		{Symbol: "ETHUSDT", Direction: "long", Ret4h: f64(0.01), Bench4h: bench},
		// Rose 5%, more than BTC: a long on it did beat the market.
		{Symbol: "SOLUSDT", Direction: "long", Ret4h: f64(0.05), Bench4h: bench},
	}
	_, _, th, tn := stableCounts(nil, rows)
	if tn != 2 || th != 1 {
		t.Fatalf("hits/samples = %d/%d, want 1/2 (excess over BTC decides)", th, tn)
	}
}

// A ranking that shows one direction must report the accuracy of calls on
// that direction. Mixing both sides let a "short" badge carry a hit rate
// earned by long calls.
func TestStableCountsForCountsOnlyTheShownSide(t *testing.T) {
	ai := []store.CopyTradeScoreOutcome{
		{Symbol: "AUSDT", Bias: "short", Ret4h: f64(0.02)}, // short hit
		{Symbol: "BUSDT", Bias: "long", Ret4h: f64(-0.02)}, // long miss
		{Symbol: "CUSDT", Bias: "long", Ret4h: f64(-0.03)}, // long miss
	}
	if ah, an, _, _ := stableCountsFor(ai, nil, "short"); an != 1 || ah != 1 {
		t.Fatalf("short-only = %d/%d, want 1/1", ah, an)
	}
	if ah, an, _, _ := stableCountsFor(ai, nil, "long"); an != 2 || ah != 0 {
		t.Fatalf("long-only = %d/%d, want 0/2", ah, an)
	}
	if ah, an, _, _ := stableCounts(ai, nil); an != 3 || ah != 1 {
		t.Fatalf("both sides = %d/%d, want 1/3", ah, an)
	}
}
