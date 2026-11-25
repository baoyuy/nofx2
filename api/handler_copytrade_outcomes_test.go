package api

import (
	"testing"
	"time"

	"nofx/store"
)

func TestAIOutcomeHitRateUsesDirectionAdjustedReturn(t *testing.T) {
	// A winning short has a positive stored return even though the raw coin
	// price fell. The market benchmark must not change direction hit-rate.
	shortHit, shortMiss := .02, -.01
	longHit, longMiss := .03, -.02
	stored := .10 // a stored per-row benchmark must not be what statFor uses
	rows := []store.CopyTradeScoreOutcome{
		{Symbol: "BTCUSDT", Bias: "short", AnalyzedAt: 1, Ret4h: &shortHit, Bench4h: &stored},
		{Symbol: "ETHUSDT", Bias: "short", AnalyzedAt: 1, Ret4h: &shortMiss},
		{Symbol: "SOLUSDT", Bias: "long", AnalyzedAt: 1, Ret4h: &longHit},
		{Symbol: "XRPUSDT", Bias: "long", AnalyzedAt: 1, Ret4h: &longMiss},
	}
	bench := buildBench(rows)
	short := statFor("4h", "short", "all", rows[:2], bench)
	long := statFor("4h", "long", "all", rows[2:], bench)
	if short.HitRate != 50 || long.HitRate != 50 {
		t.Fatalf("direction hit rate must use the stored signed return; short=%+v long=%+v", short, long)
	}
	if short.AvgExcess == 0 {
		t.Fatal("market-relative return should remain a separate metric")
	}
}

func TestDedupOutcomesKeepsFirstSameSideCallInFourHours(t *testing.T) {
	r0, r1, r4 := .01, -.01, .02
	rows := []store.CopyTradeScoreOutcome{
		{Symbol: "BTCUSDT", Bias: "short", AnalyzedAt: 4 * time.Hour.Milliseconds(), Ret4h: &r4},
		{Symbol: "BTCUSDT", Bias: "short", AnalyzedAt: time.Hour.Milliseconds(), Ret4h: &r1},
		{Symbol: "BTCUSDT", Bias: "short", AnalyzedAt: 0, Ret4h: &r0},
		{Symbol: "BTCUSDT", Bias: "long", AnalyzedAt: time.Hour.Milliseconds(), Ret4h: &r0},
	}
	got := dedupOutcomes(rows)
	if len(got) != 3 || got[0].AnalyzedAt != 0 || got[1].Bias != "long" || got[2].AnalyzedAt != 4*time.Hour.Milliseconds() {
		t.Fatalf("unexpected dedup rows: %+v", got)
	}
}
