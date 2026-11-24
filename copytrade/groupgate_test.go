package copytrade

import (
	"math"
	"testing"
)

func obs(side string, conv float64, rets []float64, mkt float64, hasMkt bool) []GroupObservation {
	out := make([]GroupObservation, 0, len(rets))
	for i, r := range rets {
		out = append(out, GroupObservation{Symbol: "X", AnalyzedAt: int64(i), Side: side,
			Conviction: conv, Return: r, MarketReturn: mkt, HasMarket: hasMkt})
	}
	return out
}

func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestConvBandCutsAndRejectsWeakReadings(t *testing.T) {
	if got := ConvBand(19.9); got != "" {
		t.Fatalf("below the first cut must have no band, got %q", got)
	}
	for conv, want := range map[float64]string{20: "20-35", 34.9: "20-35", 35: "35-50", 49.9: "35-50", 50: "50+", 90: "50+"} {
		if got := ConvBand(conv); got != want {
			t.Fatalf("ConvBand(%v) = %q, want %q", conv, got, want)
		}
	}
}

func TestGroupStatsSubtractsCostAndFlipsBenchmarkForShorts(t *testing.T) {
	// A short that returned +1% in its own direction while the market's long
	// return was -0.8%: the short only beat the market by 1% - 0.2% - 0.8%.
	st := GroupStats(obs("short", 40, rep(0.01, 30), -0.008, true))
	g := st[GroupKey{Side: "short", Band: "35-50"}]
	if g.Samples != 30 {
		t.Fatalf("samples = %d, want 30", g.Samples)
	}
	if math.Abs(g.NetMean-0.008) > 1e-9 {
		t.Fatalf("net mean = %v, want 0.008 (0.01 - 0.002 cost)", g.NetMean)
	}
	if math.Abs(g.ExcessMean-0.0) > 1e-9 {
		t.Fatalf("excess mean = %v, want 0 (0.008 net minus the flipped -0.008 benchmark)", g.ExcessMean)
	}
	if g.WinRate != 1 {
		t.Fatalf("win rate = %v, want 1", g.WinRate)
	}
	if g.Thin {
		t.Fatal("30 samples must not be thin")
	}
}

func TestGroupStatsMarksThinHistoryAndIgnoresNonFinite(t *testing.T) {
	st := GroupStats(append(obs("long", 25, rep(0.03, 5), 0, false),
		GroupObservation{Side: "long", Conviction: 25, Return: math.NaN()},
		GroupObservation{Side: "long", Conviction: 25, Return: math.Inf(1)}))
	g := st[GroupKey{Side: "long", Band: "20-35"}]
	if g.Samples != 5 || !g.Thin {
		t.Fatalf("want 5 samples flagged thin, got samples=%d thin=%v", g.Samples, g.Thin)
	}
}

func TestGroupStatsLowerBoundIsBelowMeanAndPunishesVariance(t *testing.T) {
	steady := GroupStats(obs("short", 40, append(rep(0.012, 15), rep(0.008, 15)...), 0, false))[GroupKey{"short", "35-50"}]
	noisy := GroupStats(obs("short", 40, append(rep(0.08, 15), rep(-0.06, 15)...), 0, false))[GroupKey{"short", "35-50"}]
	if steady.NetLower >= steady.NetMean {
		t.Fatalf("lower bound %v must sit below the mean %v", steady.NetLower, steady.NetMean)
	}
	if math.Abs(steady.NetMean-noisy.NetMean) > 1e-9 {
		t.Fatalf("means should match for this fixture: %v vs %v", steady.NetMean, noisy.NetMean)
	}
	if noisy.NetLower >= steady.NetLower {
		t.Fatalf("the noisy group must get the weaker bound: noisy %v vs steady %v", noisy.NetLower, steady.NetLower)
	}
}

func TestRankCandidatesBlocksMeasuredNonPositiveGroups(t *testing.T) {
	// A well-sampled group that lost money after cost must not enter the pool.
	stats := GroupStats(obs("short", 40, rep(-0.005, 40), 0, false))
	got := RankCandidates([]GroupRankInput{{Symbol: "AAAUSDT", Side: "short", Conviction: 40}}, stats)
	if len(got) != 1 {
		t.Fatalf("want 1 ranked candidate, got %d", len(got))
	}
	if !got[0].Blocked {
		t.Fatalf("a measured negative group must be blocked, reason=%q rank=%v", got[0].Reason, got[0].Rank)
	}
	if got[0].Thin {
		t.Fatal("40 samples is not thin")
	}
}

func TestRankCandidatesKeepsThinGroupBelowMeasuredPositiveOne(t *testing.T) {
	// The user's choice: a side with too little history still gets through, but
	// ranked under any group with a measured positive bound.
	stats := GroupStats(obs("short", 40, rep(0.02, 40), 0, false))
	for k, v := range GroupStats(obs("long", 40, rep(0.05, 3), 0, false)) {
		stats[k] = v
	}
	got := RankCandidates([]GroupRankInput{
		{Symbol: "LONGUSDT", Side: "long", Conviction: 40},
		{Symbol: "SHORTUSDT", Side: "short", Conviction: 40},
	}, stats)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Symbol != "SHORTUSDT" {
		t.Fatalf("the measured group must rank first, got %q", got[0].Symbol)
	}
	if !got[1].Thin || got[1].Blocked {
		t.Fatalf("the thin long must be kept and flagged, got thin=%v blocked=%v", got[1].Thin, got[1].Blocked)
	}
	if got[1].Rank >= got[0].Rank {
		t.Fatalf("thin rank %v must be below the measured rank %v", got[1].Rank, got[0].Rank)
	}
}

func TestRankCandidatesBlocksOnNetButRanksOnExcess(t *testing.T) {
	// Two money-making short groups. "market" earned 3% but all of it was the
	// market (zero excess); "picker" earned less but beat its cohort. Neither
	// is blocked (both made money after cost), and the picker ranks first.
	stats := GroupStats(obs("short", 60, rep(0.03, 40), -0.028, true))
	for k, v := range GroupStats(obs("short", 40, rep(0.012, 40), -0.002, true)) {
		stats[k] = v
	}
	got := RankCandidates([]GroupRankInput{
		{Symbol: "MARKETUSDT", Side: "short", Conviction: 60},
		{Symbol: "PICKERUSDT", Side: "short", Conviction: 40},
	}, stats)
	if got[0].Blocked || got[1].Blocked {
		t.Fatalf("groups that made money after cost must not be blocked: %+v", got)
	}
	if got[0].Symbol != "PICKERUSDT" {
		t.Fatalf("the group that beat the market must rank first, got %q", got[0].Symbol)
	}
	if got[0].Reason != "net-and-excess-positive" || got[1].Reason != "net-positive-excess-unproven" {
		t.Fatalf("unexpected reasons: %q / %q", got[0].Reason, got[1].Reason)
	}
}

func TestRankCandidatesRanksOnNetWhenNoBenchmark(t *testing.T) {
	stats := GroupStats(obs("long", 40, rep(0.01, 40), 0, false))
	g := stats[GroupKey{"long", "35-50"}]
	if g.HasExcess {
		t.Fatal("no benchmarked sample must leave HasExcess false")
	}
	got := RankCandidates([]GroupRankInput{{Symbol: "AUSDT", Side: "long", Conviction: 40}}, stats)
	want := rankCenter + g.NetLower*100*rankPerPct
	if got[0].Blocked || got[0].Rank != want {
		t.Fatalf("rank = %v blocked=%v, want %v unblocked", got[0].Rank, got[0].Blocked, want)
	}
}

func TestRankCandidatesDropsBelowBandMinimum(t *testing.T) {
	got := RankCandidates([]GroupRankInput{{Symbol: "AAAUSDT", Side: "long", Conviction: 10}}, nil)
	if len(got) != 0 {
		t.Fatalf("a reading under the lowest band must not be ranked, got %+v", got)
	}
}

func TestMeasureBreadthAndVetoDirection(t *testing.T) {
	down := MeasureBreadth(append(rep(-0.02, 18), rep(0.01, 2)...)) // 10% up
	if !down.Valid || down.Up != 0.1 {
		t.Fatalf("want valid breadth 0.1, got %+v", down)
	}
	if down.Label() != "broad-down" {
		t.Fatalf("label = %q, want broad-down", down.Label())
	}
	if !down.BreadthVetoes("long") {
		t.Fatal("a long must be vetoed in a broadly falling market")
	}
	if down.BreadthVetoes("short") {
		t.Fatal("a short must NOT be vetoed in a falling market (veto is one-sided)")
	}
}

func TestBreadthMixedAndInvalidReadingsVetoNothing(t *testing.T) {
	mixed := MeasureBreadth(append(rep(0.01, 10), rep(-0.01, 10)...))
	if mixed.Label() != "mixed" {
		t.Fatalf("label = %q, want mixed", mixed.Label())
	}
	if mixed.BreadthVetoes("long") || mixed.BreadthVetoes("short") {
		t.Fatal("a mixed market must veto neither side")
	}
	thin := MeasureBreadth(rep(-0.05, 5))
	if thin.Valid {
		t.Fatal("5 symbols must not be a valid reading")
	}
	if thin.BreadthVetoes("long") || thin.BreadthVetoes("short") {
		t.Fatal("an invalid reading must veto nothing")
	}
}

func TestMeasureBreadthMedianIgnoresNonFinite(t *testing.T) {
	b := MeasureBreadth([]float64{math.NaN(), math.Inf(-1), -0.01, 0.03, 0.01})
	if b.Samples != 3 {
		t.Fatalf("samples = %d, want 3", b.Samples)
	}
	if math.Abs(b.Median-0.01) > 1e-9 {
		t.Fatalf("median = %v, want 0.01", b.Median)
	}
}
