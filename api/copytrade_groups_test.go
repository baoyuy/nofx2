package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nofx/copytrade"
	"nofx/store"
	"nofx/trader/binance"
)

func fp(v float64) *float64 { return &v }

// outcomeRow builds a mature 4h outcome. ret is signed in the predicted
// direction, as stored.
func outcomeRow(sym, bias string, at int64, bull, bear, ret float64) store.CopyTradeScoreOutcome {
	return store.CopyTradeScoreOutcome{UserID: "alice", Symbol: sym, Bias: bias, AnalyzedAt: at,
		Bull: bull, Bear: bear, Ret4h: fp(ret), Done4h: true}
}

func TestBuildGroupSnapshotMeasuresGroupsFromStoredOutcomes(t *testing.T) {
	// 30 short readings, conviction 40, each +1.5% in the predicted direction,
	// every one in its own cohort so the cohort benchmark equals its own raw
	// return. Net-of-cost excess over its own cohort is then -cost: the group
	// made money but showed no selection, so it is kept and not credited.
	var rows []store.CopyTradeScoreOutcome
	for i := 0; i < 30; i++ {
		rows = append(rows, outcomeRow("C"+string(rune('A'+i))+"USDT", "short", int64(i)*outcomeDedupMs, 30, 70, 0.015))
	}
	snap := buildGroupSnapshot(rows, int64(40)*outcomeDedupMs)
	g, ok := snap.stats[copytrade.GroupKey{Side: "short", Band: "35-50"}]
	if !ok {
		t.Fatalf("group missing: %+v", snap.stats)
	}
	if g.Samples != 30 || g.Thin {
		t.Fatalf("want 30 measured samples, got %+v", g)
	}
	if g.NetMean <= 0 {
		t.Fatalf("absolute net mean should be positive (1.5%% - cost), got %v", g.NetMean)
	}
	if g.ExcessMean >= 0 {
		t.Fatalf("a call that only matched its own cohort has no excess after cost, got %v", g.ExcessMean)
	}
	if !g.HasExcess {
		t.Fatal("cohort benchmarks exist, so excess must be marked as measured")
	}
	got := copytrade.RankCandidates([]copytrade.GroupRankInput{{Symbol: "NEWUSDT", Side: "short", Conviction: 40}}, snap.stats)
	if len(got) != 1 || got[0].Blocked || got[0].Reason != "net-positive-excess-unproven" {
		t.Fatalf("a profitable market-only group is kept but not credited with selection: %+v", got)
	}
}

func TestBuildGroupSnapshotBlocksGroupThatLostAfterCost(t *testing.T) {
	var rows []store.CopyTradeScoreOutcome
	for i := 0; i < 30; i++ {
		rows = append(rows, outcomeRow("L"+string(rune('A'+i))+"USDT", "long", int64(i)*outcomeDedupMs, 70, 30, 0.001))
	}
	snap := buildGroupSnapshot(rows, int64(40)*outcomeDedupMs)
	got := copytrade.RankCandidates([]copytrade.GroupRankInput{{Symbol: "NEWUSDT", Side: "long", Conviction: 40}}, snap.stats)
	if len(got) != 1 || !got[0].Blocked {
		t.Fatalf("+0.1%% gross is a loss after the 0.2%% cost; the group must be blocked: %+v", got)
	}
}

func TestBreadthFromReadsTheLastCompletedWindowOncePerCoin(t *testing.T) {
	now := int64(100) * time.Hour.Milliseconds()
	inWindow := now - 5*time.Hour.Milliseconds() // analysed 5h ago: mature, inside window
	var rows []store.CopyTradeScoreOutcome
	// 24 coins: 20 fell (stored as winning shorts), 4 rose (stored as winning longs).
	for i := 0; i < 20; i++ {
		rows = append(rows, outcomeRow("D"+string(rune('A'+i))+"USDT", "short", inWindow, 20, 60, 0.02))
	}
	for i := 0; i < 4; i++ {
		rows = append(rows, outcomeRow("U"+string(rune('A'+i))+"USDT", "long", inWindow, 60, 20, 0.02))
	}
	// A second, later reading of a falling coin must not count it twice.
	rows = append(rows, outcomeRow("DAUSDT", "long", inWindow+time.Minute.Milliseconds(), 60, 20, -0.02))
	// A reading outside the window must be ignored.
	rows = append(rows, outcomeRow("OLDUSDT", "long", now-20*time.Hour.Milliseconds(), 60, 20, 0.05))

	b := breadthFrom(rows, now)
	if b.Samples != 24 {
		t.Fatalf("samples = %d, want 24 (one per coin, window only)", b.Samples)
	}
	if !b.Valid {
		t.Fatal("24 coins is a valid reading")
	}
	if want := 4.0 / 24.0; b.Up < want-1e-9 || b.Up > want+1e-9 {
		t.Fatalf("up share = %v, want %v", b.Up, want)
	}
	if !b.BreadthVetoes("long") || b.BreadthVetoes("short") {
		t.Fatalf("a broadly falling window must veto longs only: %+v", b)
	}
}

func TestBreadthFromIsUnknownWithoutRecentMatureReadings(t *testing.T) {
	now := int64(100) * time.Hour.Milliseconds()
	rows := []store.CopyTradeScoreOutcome{outcomeRow("AUSDT", "short", now-30*time.Hour.Milliseconds(), 20, 60, 0.02)}
	b := breadthFrom(rows, now)
	if b.Valid || b.BreadthVetoes("long") || b.BreadthVetoes("short") {
		t.Fatalf("stale history must give an unknown reading that vetoes nothing: %+v", b)
	}
}

func TestSmoothEdgeFavoursNewReadingAndDropsStaleMemory(t *testing.T) {
	now := int64(10) * time.Hour.Milliseconds()
	if got := smoothEdge(40, nil, now); got != 40 {
		t.Fatalf("no memory: got %v, want 40", got)
	}
	recent := &store.CopyTradeScoreMemory{LastAt: now - time.Hour.Milliseconds(), SmoothedEdge: -40}
	if got := smoothEdge(40, recent, now); got != 0.7*40+0.3*-40 {
		t.Fatalf("recent memory: got %v, want %v (newest reading dominates)", got, 0.7*40+0.3*-40)
	}
	if got := smoothEdge(40, recent, now); got <= 0 {
		t.Fatalf("a fresh opposite reading must win the sign, got %v", got)
	}
	stale := &store.CopyTradeScoreMemory{LastAt: now - 5*time.Hour.Milliseconds(), SmoothedEdge: -80}
	if got := smoothEdge(40, stale, now); got != 40 {
		t.Fatalf("memory older than one 4h horizon must be ignored, got %v", got)
	}
}

func TestCoinScorePromptDropsCrowdConsensusAndPreviousAnchor(t *testing.T) {
	k := make([]binance.MarketKline, 30)
	for i := range k {
		k[i] = binance.MarketKline{Time: int64(i) * 3600, Open: 1, High: 1.1, Low: 0.9, Close: 1, Volume: 10}
	}
	tfs := []scoreTF{{Interval: "1h", Klines: k, Ind: computeIndicators(k)}}
	sum := &copytrade.SymbolSummary{Symbol: "SOLUSDT"}
	sum.Long.Traders, sum.Long.Openers = 12, 3
	sum.Short.Openers = 1
	sum.Confidence.Score = 88
	holders := []copytrade.Holder{{Nickname: "whale-nick"}}
	prev := &store.CopyTradeScoreMemory{Bias: "short", Bull: 20, Bear: 70, LastAt: 1, Invalidation: "reclaim 150"}
	rel := relStrength{Coin1h: 1.2, BTC1h: 0.2, Coin4h: -0.5, BTC4h: -2.0, Coin24h: 3, BTC24h: 1, Has1h: true, Has4h: true}

	system, user := buildCoinScorePrompt("zh", "SOLUSDT", tfs, nil, binance.DerivativeInfo{}, "", sum, holders, prev, rel)

	for _, banned := range []string{"Confidence long", "Rank ", "Top holders", "whale-nick", "agreement", "reclaim 150"} {
		if strings.Contains(user, banned) {
			t.Fatalf("user prompt must not contain %q:\n%s", banned, user)
		}
	}
	if strings.Contains(system, "otherwise keep it") || strings.Contains(system, "change only on new evidence") {
		t.Fatal("system prompt must not tell the model to keep its previous call")
	}
	for _, want := range []string{"1h: coin +1.20% vs BTC +0.20% => +1.00 pts", "4h: coin -0.50% vs BTC -2.00% => +1.50 pts",
		"fresh long openers 3, fresh short openers 1", "PREVIOUS READING (context only"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user prompt missing %q:\n%s", want, user)
		}
	}
	if !strings.Contains(system, "NEXT 4 HOURS RELATIVE TO THE REST OF THE MARKET") {
		t.Fatal("system prompt must frame the call as relative to the market over 4h")
	}
	if !strings.Contains(system, "Simplified Chinese") {
		t.Fatal("language must still be honoured")
	}
}

func TestCoinScorePromptMarksMissingRelativeWindows(t *testing.T) {
	_, user := buildCoinScorePrompt("", "SOLUSDT", nil, nil, binance.DerivativeInfo{}, "", nil, nil, nil, relStrength{Coin24h: 1, BTC24h: 2})
	if !strings.Contains(user, "1h: unavailable") || !strings.Contains(user, "4h: unavailable") {
		t.Fatalf("missing windows must be labelled unavailable, not zero:\n%s", user)
	}
	if !strings.Contains(user, "none recently.") {
		t.Fatalf("no copy-trade summary must be stated:\n%s", user)
	}
}

func TestRelativeStrengthWithoutHourlyCandlesMakesNoRequest(t *testing.T) {
	// No 1h timeframe: the function must return before touching the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := relativeStrength(ctx, []scoreTF{{Interval: "15m"}}, 2, 1)
	if r.Has1h || r.Has4h || r.Coin24h != 2 || r.BTC24h != 1 {
		t.Fatalf("unexpected: %+v", r)
	}
}

// End to end through buildCoinPool: with measured history, a group that lost
// money after cost is blocked even though its conviction is high, and a thin
// group is let through with an explicit note.
func TestMeasuredPoolBlocksMarketOnlyGroupAndFlagsThinGroup(t *testing.T) {
	s, _ := coinPoolServer(t)
	resetGroupCache()
	base := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	for i := 0; i < 30; i++ {
		o := outcomeRow("H"+string(rune('A'+i))+"USDT", "short", base+int64(i)*outcomeDedupMs, 20, 80, -0.01)
		if err := s.store.CopyTrade().SaveScoreOutcome(&o); err != nil {
			t.Fatal(err)
		}
	}
	saveTestScore(t, s, "alice", "SHORTUSDT", "short", -60, -60) // group short/50+: measured, lost after cost
	saveTestScore(t, s, "alice", "LONGUSDT", "long", 40, 40)     // group long/35-50: no history

	pool, err := s.buildCoinPool("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Coins) != 1 || pool.Coins[0].Symbol != "LONGUSDT" {
		t.Fatalf("want only the thin long, got %+v", pool.Coins)
	}
	if !strings.Contains(pool.Coins[0].Note, "样本不足") {
		t.Fatalf("thin pick must be flagged, note=%q", pool.Coins[0].Note)
	}

	// Switching back to conviction ranking restores the old behaviour.
	cfg := s.loadCoinScoreAuto()
	cfg.PoolRankMode = poolRankConviction
	saveCoinScoreAutoForTest(t, s, cfg)
	pool, err = s.buildCoinPool("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Coins) != 2 {
		t.Fatalf("conviction mode should admit both, got %+v", pool.Coins)
	}
}

func TestPoolRankModeDefaultsToMeasured(t *testing.T) {
	if (coinScoreAutoConfig{}).rankMode() != poolRankMeasured {
		t.Fatal("unset mode (settings saved before the option existed) must mean measured")
	}
	if (coinScoreAutoConfig{PoolRankMode: "bogus"}).rankMode() != poolRankMeasured {
		t.Fatal("unknown mode must mean measured")
	}
	if (coinScoreAutoConfig{PoolRankMode: poolRankConviction}).rankMode() != poolRankConviction {
		t.Fatal("conviction must be honoured")
	}
}

func saveCoinScoreAutoForTest(t *testing.T, s *Server, cfg coinScoreAutoConfig) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetSystemConfig(coinScoreAutoKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	resetGroupCache()
}
