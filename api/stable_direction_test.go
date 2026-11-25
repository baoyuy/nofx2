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
	ai := []store.CopyTradeScoreOutcome{
		{Bias: "short", Ret4h: f64(0.02)},   // hit
		{Bias: "short", Ret4h: f64(-0.02)},  // miss
		{Bias: "long", Ret4h: f64(0.001)},   // inside the neutral band: skipped
		{Bias: "neutral", Ret4h: f64(0.05)}, // no direction: skipped
		{Bias: "long", Ret4h: nil},          // not mature: skipped
	}
	bench := f64(0.01)
	trades := []store.CopyTradeSignal{
		{Direction: "long", Ret4h: f64(0.03), Bench4h: bench},  // +3% vs +1% => hit
		{Direction: "long", Ret4h: f64(0.005), Bench4h: bench}, // under the market => miss
		{Direction: "short", Ret4h: f64(-0.04), Bench4h: bench}, // fell more than market => hit
		{Direction: "neutral", Ret4h: f64(0.09)},                // skipped
		{Direction: "long", Ret4h: nil},                         // skipped: would have panicked before
	}
	ah, an, th, tn := stableCounts(ai, trades)
	if an != 2 || ah != 1 {
		t.Fatalf("AI counts = %d/%d, want 1/2", ah, an)
	}
	if tn != 3 || th != 2 {
		t.Fatalf("trader counts = %d/%d, want 2/3", th, tn)
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
