package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"
)

func saveTestScore(t *testing.T, s *Server, user, symbol, bias string, edge, smoothed float64) {
	t.Helper()
	conv := edge
	if conv < 0 {
		conv = -conv
	}
	res := coinScoreResult{Symbol: symbol, AnalyzedAt: time.Now().UnixMilli(),
		Analysis: &coinScoreAnalysis{Bias: bias, Edge: edge, SmoothedEdge: smoothed, Conviction: conv}}
	raw, _ := json.Marshal(res)
	if err := s.store.CopyTrade().SaveCoinScore(&store.CopyTradeCoinScore{UserID: user, Symbol: symbol,
		AnalyzedAt: res.AnalyzedAt, Payload: string(raw)}); err != nil {
		t.Fatal(err)
	}
}

// The AI pool is an ordinary coin pool consumed through a strategy. Its ID must
// survive rebuilds (including an empty one), and an empty pool must stop the
// strategy from trading instead of reviving the snapshot saved with it.
func TestAICoinPoolFeedsStrategyWithStableID(t *testing.T) {
	s, router := coinPoolServer(t)
	saveTestScore(t, s, "alice", "SOLUSDT", "long", 40, 40)
	saveTestScore(t, s, "alice", "DOGEUSDT", "short", -35, -35)
	// Latest reading says long but the smoothed edge is still short-side:
	// it must not enter as a strong long.
	saveTestScore(t, s, "alice", "XRPUSDT", "long", 30, -25)

	pool, err := s.buildCoinPool("alice")
	if err != nil || pool.ID == "" || len(pool.Coins) != 2 {
		t.Fatalf("first build: %+v %v", pool, err)
	}
	for _, c := range pool.Coins {
		if c.Symbol == "XRPUSDT" {
			t.Fatalf("a pick whose smoothed edge disagrees with its side must be skipped: %+v", pool.Coins)
		}
	}
	poolID := pool.ID

	// The strategy picks the AI pool like any other saved pool.
	body := `{"name":"ai pool strategy","config":{"ai_config":{"coin_source":{"source_type":"coin_pool","coin_pool_id":"` + poolID + `"}}}}`
	if w := call(router, http.MethodPost, "/strategies", "alice", body); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	list, _ := s.store.Strategy().List("alice")
	var cfg *store.StrategyConfig
	for _, item := range list {
		if item.Name == "ai pool strategy" {
			cfg, _ = item.ParseConfig()
		}
	}
	if cfg == nil || cfg.CoinSource.CoinPoolID != poolID || len(cfg.CoinSource.StaticCoins) != 2 {
		t.Fatalf("strategy must reference the AI pool: %+v", cfg)
	}

	prevSym, prevDetail := kernel.CoinPoolResolver, kernel.CoinPoolDetailResolver
	t.Cleanup(func() { kernel.CoinPoolResolver, kernel.CoinPoolDetailResolver = prevSym, prevDetail })
	kernel.CoinPoolResolver = s.store.CoinPool().Symbols
	kernel.CoinPoolDetailResolver = s.store.CoinPool().CoinsByID

	coins, err := kernel.NewStrategyEngine(cfg).GetCandidateCoins()
	if err != nil || len(coins) != 2 {
		t.Fatalf("strategy must trade the AI pool: %+v %v", coins, err)
	}
	dirs := map[string]string{}
	for _, c := range coins {
		dirs[c.Symbol] = c.Direction
	}
	if dirs["SOLUSDT"] != "long" || dirs["DOGEUSDT"] != "short" {
		t.Fatalf("pool direction must reach the candidates: %+v", coins)
	}

	// Scores go stale: the rebuild empties the pool but keeps its ID.
	stale := time.Now().Add(-24 * time.Hour).UnixMilli()
	rows, _ := s.store.CopyTrade().ListCoinScores("alice")
	for i := range rows {
		rows[i].AnalyzedAt = stale
		if err := s.store.CopyTrade().SaveCoinScore(&rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	empty, err := s.buildCoinPool("alice")
	if err != nil || empty.ID != poolID || len(empty.Coins) != 0 {
		t.Fatalf("empty rebuild must keep the pool ID: %+v %v", empty, err)
	}
	if got, err := s.store.CoinPool().Get("alice", poolID); err != nil || len(got.Coins) != 0 {
		t.Fatalf("pool must still exist, empty: %+v %v", got, err)
	}
	coins, _ = kernel.NewStrategyEngine(cfg).GetCandidateCoins()
	if len(coins) != 0 {
		t.Fatalf("an empty AI pool must not trade the old snapshot: %+v", coins)
	}

	// Fresh scores again: same pool ID, strategy trades again without edits.
	saveTestScore(t, s, "alice", "ETHUSDT", "long", 50, 50)
	again, err := s.buildCoinPool("alice")
	if err != nil || again.ID != poolID || len(again.Coins) != 1 {
		t.Fatalf("rebuild must reuse the pool ID: %+v %v", again, err)
	}
	coins, _ = kernel.NewStrategyEngine(cfg).GetCandidateCoins()
	if len(coins) != 1 || coins[0].Symbol != "ETHUSDT" || coins[0].Direction != "long" {
		t.Fatalf("strategy must follow the rebuilt pool: %+v", coins)
	}

	// All picks expired (pool not refreshed in time): nothing to open.
	expired, _ := s.store.CoinPool().Get("alice", poolID)
	expired.Coins[0].ExpiryAt = time.Now().Add(-time.Minute).UnixMilli()
	if err := s.store.CoinPool().SaveAuto("alice", expired); err != nil {
		t.Fatal(err)
	}
	coins, _ = kernel.NewStrategyEngine(cfg).GetCandidateCoins()
	if len(coins) != 0 {
		t.Fatalf("expired AI picks must not trade: %+v", coins)
	}

	pools, _ := s.store.CoinPool().List("alice")
	n := 0
	for _, p := range pools {
		if p.Screener == poolScreener {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("exactly one AI pool per user, got %d", n)
	}
}

// With no scores and no pool yet, a build must not create an empty pool.
func TestAICoinPoolNotCreatedWhenEmpty(t *testing.T) {
	s, _ := coinPoolServer(t)
	pool, err := s.buildCoinPool("bob")
	if err != nil || pool.ID != "" || len(pool.Coins) != 0 {
		t.Fatalf("%+v %v", pool, err)
	}
	if _, err := s.store.CoinPool().GetAuto("bob", poolScreener); err == nil {
		t.Fatal("no pool should be stored")
	}
}

// The scheduler scores (and builds the pool) for the account that picked the
// scoring model, not for whichever account touched a model last.
func TestScheduledScoringUsesModelOwner(t *testing.T) {
	s, _ := coinPoolServer(t)
	if err := s.store.AIModel().Create("alice", "alice_ds", "DeepSeek", "deepseek", true, "sk-alice", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.AIModel().Create("bob", "bob_ds", "DeepSeek", "deepseek", true, "sk-bob", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetSystemConfig(copytradeScoreModelKey, "alice_ds"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetSystemConfig(copytradeScoreOwnerKey, "alice"); err != nil {
		t.Fatal(err)
	}
	m, uid, err := s.resolveScoreModelAny("")
	if err != nil || uid != "alice" || m.ID != "alice_ds" {
		t.Fatalf("got %v %q %v", m, uid, err)
	}

	// Older installs saved only the model ID: use that model's owner.
	if err := s.store.SetSystemConfig(copytradeScoreOwnerKey, ""); err != nil {
		t.Fatal(err)
	}
	m, uid, err = s.resolveScoreModelAny("")
	if err != nil || uid != "alice" || m.ID != "alice_ds" {
		t.Fatalf("saved model owner: got %v %q %v", m, uid, err)
	}
}
