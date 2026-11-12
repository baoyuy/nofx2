package copytrade

import (
	"math"
	"testing"
	"time"
)

func TestEffectiveCount(t *testing.T) {
	cases := []struct {
		name string
		ws   []float64
		want float64
	}{
		{"none", nil, 0},
		{"three equal voices", []float64{1, 1, 1}, 3},
		{"four equal voices", []float64{1, 1, 1, 1}, 4},
		// One account carrying most of the weight is one voice, not three.
		{"one dominant", []float64{10, 0.1, 0.1}, 1.04},
	}
	for _, c := range cases {
		if got := effectiveCount(c.ws); math.Abs(got-c.want) > 0.01 {
			t.Errorf("%s: effectiveCount(%v) = %.3f, want %.3f", c.name, c.ws, got, c.want)
		}
	}
}

func TestCrowdingFactor(t *testing.T) {
	if got := crowdingFactor(0); got != 1 {
		t.Errorf("no holders: %v", got)
	}
	if got := crowdingFactor(crowdFree); got != 1 {
		t.Errorf("at the free limit: %v", got)
	}
	if got := crowdingFactor(crowdFree + 2); math.Abs(got-0.9) > 1e-9 {
		t.Errorf("two over the limit: %v", got)
	}
	if got := crowdingFactor(100); got != crowdFloor {
		t.Errorf("crowded coin must not go below the floor: %v", got)
	}
}

func TestLiquidityFactor(t *testing.T) {
	if got := liquidityFactor(0); got != unknownLiquidity {
		t.Errorf("unknown depth: %v", got)
	}
	if got := liquidityFactor(1e8); got != 1 {
		t.Errorf("100M should count fully: %v", got)
	}
	if got := liquidityFactor(1e6); got != 0.4 {
		t.Errorf("1M should hit the floor: %v", got)
	}
}

func TestEntryFactor(t *testing.T) {
	if got := entryFactor(0); got != 1 {
		t.Errorf("at the average entry: %v", got)
	}
	if got := entryFactor(chaseFreePct); got != 1 {
		t.Errorf("chase-free band: %v", got)
	}
	if got := entryFactor(chaseFullPct); got != chaseFloor {
		t.Errorf("fully chased: %v", got)
	}
	// A better-than-average entry is a bonus, capped.
	if got := entryFactor(-10); got != entryBonusCap {
		t.Errorf("better entry must be capped: %v", got)
	}
	if mid := entryFactor(3.5); mid <= chaseFloor || mid >= 1 {
		t.Errorf("partly chased should sit between: %v", mid)
	}
}

// summary builds one coin's reading for RankOf.
func summary(sh Shadow, conf Confidence, holders int, volume float64) SymbolSummary {
	c := SymbolSummary{Symbol: "SOLUSDT", Shadow: sh, Confidence: conf, Volume24h: volume}
	if conf.Direction == "short" {
		c.Short.Traders = holders
	} else {
		c.Long.Traders = holders
	}
	c.Rank = RankOf(c)
	return c
}

func TestRankPrefersFreshCallsOverHoldings(t *testing.T) {
	fresh := summary(
		Shadow{Direction: "long", Score: 60, Flow: 1, OpenShare: 1, Effective: 2, LongCalls: 2, Burst: 1},
		Confidence{Direction: "long", Score: 80, Traders: 2, Effective: 2},
		2, 0)
	holding := summary(Shadow{Direction: "neutral"},
		Confidence{Direction: "long", Score: 80, Traders: 2, Effective: 2}, 2, 0)
	if fresh.Rank.Basis != "fresh" || holding.Rank.Basis != "holding" {
		t.Fatalf("basis: %s / %s", fresh.Rank.Basis, holding.Rank.Basis)
	}
	if fresh.Rank.Score <= holding.Rank.Score {
		t.Fatalf("a fresh call must outrank holdings alone: %.1f vs %.1f", fresh.Rank.Score, holding.Rank.Score)
	}
}

func TestRankRewardsIndependentCallers(t *testing.T) {
	one := summary(Shadow{Direction: "long", Flow: 1, OpenShare: 1, Effective: 1, LongCalls: 1},
		Confidence{Direction: "long", Score: 50, Traders: 1, Effective: 1}, 1, 0)
	three := summary(Shadow{Direction: "long", Flow: 1, OpenShare: 1, Effective: 3, LongCalls: 3},
		Confidence{Direction: "long", Score: 50, Traders: 3, Effective: 3}, 3, 0)
	if three.Rank.Score <= one.Rank.Score {
		t.Fatalf("three independent callers must outrank one: %.1f vs %.1f", three.Rank.Score, one.Rank.Score)
	}
	// Raw accounts that move together are one voice, not three.
	clustered := summary(Shadow{Direction: "long", Flow: 1, OpenShare: 1, Effective: 1, LongCalls: 3},
		Confidence{Direction: "long", Score: 50, Traders: 3, Effective: 1}, 3, 0)
	if clustered.Rank.Score > one.Rank.Score*1.01 {
		t.Fatalf("three correlated accounts must not count as three voices: %.1f vs %.1f", clustered.Rank.Score, one.Rank.Score)
	}
}

func TestRankDiscountsCrowdedCoins(t *testing.T) {
	room := summary(Shadow{Direction: "long", Flow: 1, OpenShare: 1, Effective: 2, LongCalls: 2},
		Confidence{Direction: "long", Score: 60, Traders: 2, Effective: 2}, crowdFree, 0)
	crowded := summary(Shadow{Direction: "long", Flow: 1, OpenShare: 1, Effective: 2, LongCalls: 2},
		Confidence{Direction: "long", Score: 60, Traders: 12, Effective: 2}, 12, 0)
	if crowded.Rank.Crowding >= room.Rank.Crowding {
		t.Fatalf("crowding must discount: %v vs %v", crowded.Rank.Crowding, room.Rank.Crowding)
	}
	if crowded.Rank.Score >= room.Rank.Score {
		t.Fatalf("crowded coin must rank lower: %.1f vs %.1f", crowded.Rank.Score, room.Rank.Score)
	}
}

func TestRankUsesLiquidityAndEntry(t *testing.T) {
	sh := Shadow{Direction: "long", Flow: 1, OpenShare: 1, Effective: 2, LongCalls: 2}
	conf := Confidence{Direction: "long", Score: 60, Traders: 2, Effective: 2}
	thin := summary(sh, conf, 2, 2e6)
	deep := summary(sh, conf, 2, 5e8)
	if deep.Rank.Score <= thin.Rank.Score {
		t.Fatalf("a deeper coin must rank higher: %.1f vs %.1f", deep.Rank.Score, thin.Rank.Score)
	}
	if deep.Rank.Cost >= thin.Rank.Cost {
		t.Fatalf("a deeper coin must cost less to trade: %.3f vs %.3f", deep.Rank.Cost, thin.Rank.Cost)
	}
	chased := sh
	chased.EntryGapPct = 8
	if got := summary(chased, conf, 2, 5e8).Rank; got.Score >= deep.Rank.Score {
		t.Fatalf("a chased coin must rank lower: %.1f vs %.1f", got.Score, deep.Rank.Score)
	}
}

func TestRankCrowdingUsesTheCalledSide(t *testing.T) {
	// A short coin crowded on the short side is crowded even when the long
	// side is empty: the count must follow the direction, not a stale field.
	sh := Shadow{Direction: "short", Flow: -1, OpenShare: 1, Effective: 2, ShortCalls: 2}
	conf := Confidence{Direction: "short", Score: 60, Traders: 10, Effective: 2}
	c := SymbolSummary{Symbol: "SOLUSDT", Shadow: sh, Confidence: conf}
	c.Short.Traders = 10
	c.Rank = RankOf(c)
	if c.Rank.Direction != "short" || c.Rank.Holders != 10 {
		t.Fatalf("holders must follow the short side: %+v", c.Rank)
	}
	if c.Rank.Crowding >= 1 {
		t.Fatalf("a crowded short side must be discounted: %+v", c.Rank)
	}
}

func TestRankNeutralWhenNothingToGoOn(t *testing.T) {
	r := RankOf(SymbolSummary{Symbol: "X"})
	if r.Direction != "neutral" || r.Score != 0 || r.Basis != "" {
		t.Fatalf("empty coin: %+v", r)
	}
}

func TestAttachRanksFillsVolume(t *testing.T) {
	sums := []SymbolSummary{summary(Shadow{Direction: "long", Flow: 1, OpenShare: 1, Effective: 2, LongCalls: 2},
		Confidence{Direction: "long", Score: 60, Traders: 2, Effective: 2}, 2, 0)}
	AttachRanks(sums, map[string]float64{"SOLUSDT": 5e8})
	if sums[0].Volume24h != 5e8 || sums[0].Rank.Liquidity != 1 {
		t.Fatalf("volume must reach the rank: %+v", sums[0].Rank)
	}
	// A coin missing from the map keeps its earlier reading.
	AttachRanks(sums, map[string]float64{})
	if sums[0].Rank.Liquidity != 1 {
		t.Fatalf("missing volume must not reset the rank: %+v", sums[0].Rank)
	}
}

func TestShadowEffectiveMergesSameSourceCalls(t *testing.T) {
	now := time.Now()
	snaps := []Snapshot{
		caller("a", "long", 3*time.Hour, now),
		caller("b", "long", 3*time.Hour-time.Minute/2, now),
		caller("c", "long", 3*time.Hour-time.Minute, now),
	}
	sh := shadowFor(t, snaps, now)
	if sh.LongCalls != 1 || sh.Effective > 1.05 {
		t.Fatalf("calls within a minute are one source: calls=%d effective=%.2f", sh.LongCalls, sh.Effective)
	}

	spread := []Snapshot{
		caller("a", "long", 3*time.Hour, now),
		caller("b", "long", 5*time.Hour, now),
		caller("c", "long", 7*time.Hour, now),
	}
	sh = shadowFor(t, spread, now)
	if sh.LongCalls != 3 || math.Abs(sh.Effective-3) > 0.05 {
		t.Fatalf("independent calls count separately: calls=%d effective=%.2f", sh.LongCalls, sh.Effective)
	}
}

func TestConfidenceEffectiveCountsVoices(t *testing.T) {
	votes := []vote{
		{trader: "a", side: "long", weight: 1, base: 1},
		{trader: "b", side: "long", weight: 1, base: 1},
		{trader: "c", side: "short", weight: 0.2, base: 0.2},
	}
	c := confidence(votes)
	if math.Abs(c.Effective-2) > 0.01 {
		t.Fatalf("two equal long votes: %+v", c)
	}
}

func TestSymbolsCarryRank(t *testing.T) {
	now := time.Now()
	var snaps []Snapshot
	for i, id := range []string{"a", "b", "c"} {
		snaps = append(snaps, caller(id, "long", time.Duration(2+2*i)*time.Hour, now))
	}
	coins := SymbolsAt(snaps, now)
	if len(coins) == 0 {
		t.Fatal("no coins")
	}
	r := coins[0].Rank
	if r.Direction != "long" || r.Basis != "fresh" || r.Score <= 0 {
		t.Fatalf("rank must follow the fresh calls: %+v", r)
	}
	if r.Independence <= 1 || r.Holders != 3 {
		t.Fatalf("independence and holders: %+v", r)
	}
}
