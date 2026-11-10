package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"nofx/store"
)

func createUpdateSafetyStrategy(t *testing.T, s *Server, kind string) *store.Strategy {
	t.Helper()
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "static", StaticCoins: []string{"BTCUSDT"}}
	cfg.StrategyType = kind
	if kind == "grid_trading" {
		cfg.GridConfig = &store.GridStrategyConfig{Symbol: "BTCUSDT", Leverage: 2}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	strategy := &store.Strategy{ID: "strategy", UserID: "alice", Name: "original", Description: "original description", Config: string(raw), IsPublic: false, ConfigVisible: false}
	if err := s.store.Strategy().Create(strategy); err != nil {
		t.Fatal(err)
	}
	saved, err := s.store.Strategy().Get("alice", strategy.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func assertStrategyUnchanged(t *testing.T, s *Server, before *store.Strategy) {
	t.Helper()
	after, err := s.store.Strategy().Get("alice", before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected update changed stored strategy: before=%+v after=%+v", before, after)
	}
}

func TestStrategyUpdateTokenRejectionHasNoStoreSideEffects(t *testing.T) {
	s, router := coinPoolServer(t)
	router.PUT("/strategies/:id", s.handleUpdateStrategy)
	original := createUpdateSafetyStrategy(t, s, "ai_trading")
	maxLimit := 0
	for _, limit := range store.ModelContextLimits {
		if limit > maxLimit {
			maxLimit = limit
		}
	}
	body, _ := json.Marshal(map[string]any{"name": "must not persist", "description": "must not persist", "is_public": true, "config_visible": true,
		"config": map[string]any{"ai_config": map[string]any{"custom_prompt": strings.Repeat("x", (maxLimit+10000)*4)}}})
	w := call(router, "PUT", "/strategies/strategy", "alice", string(body))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "exceeds all known model") {
		t.Fatalf("expected token rejection: %d %s", w.Code, w.Body.String())
	}
	assertStrategyUnchanged(t, s, original)
	// A subsequent normal update must still persist, including merged rules.
	w = call(router, "PUT", "/strategies/strategy", "alice", `{"name":"accepted","config":{"ai_config":{"custom_prompt":"Only trade a confirmed breakout."}}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("normal update: %d %s", w.Code, w.Body.String())
	}
	saved, _ := s.store.Strategy().Get("alice", "strategy")
	cfg, _ := saved.ParseConfig()
	if saved.Name != "accepted" || cfg.CustomPrompt != "Only trade a confirmed breakout." || len(cfg.CoinSource.StaticCoins) != 1 {
		t.Fatal("accepted partial update not merged/persisted")
	}
}

func TestStrategyUpdateRejectsRunningGridAndLiveTypeSwitch(t *testing.T) {
	cases := []struct{ name, kind, body string }{
		{"grid leverage", "grid_trading", `{"name":"changed","is_public":true,"config":{"grid_config":{"leverage":8}}}`},
		{"grid metadata", "grid_trading", `{"name":"changed","is_public":true,"config_visible":true}`},
		{"grid to AI", "grid_trading", `{"config":{"strategy_type":"ai_trading"}}`},
		{"AI to grid", "ai_trading", `{"config":{"strategy_type":"grid_trading","grid_config":{"symbol":"BTCUSDT","leverage":3}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, router := coinPoolServer(t)
			router.PUT("/strategies/:id", s.handleUpdateStrategy)
			original := createUpdateSafetyStrategy(t, s, tc.kind)
			trader := &store.Trader{ID: "linked", UserID: "alice", Name: "linked trader", StrategyID: original.ID, IsRunning: true}
			if err := s.store.Trader().Create(trader); err != nil {
				t.Fatal(err)
			}
			beforeTrader, _ := s.store.Trader().GetByID(trader.ID)
			w := call(router, "PUT", "/strategies/strategy", "alice", tc.body)
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "stop trader") {
				t.Fatalf("must explicitly reject before save: %d %s", w.Code, w.Body.String())
			}
			assertStrategyUnchanged(t, s, original)
			afterTrader, _ := s.store.Trader().GetByID(trader.ID)
			if !reflect.DeepEqual(beforeTrader, afterTrader) {
				t.Fatal("update must not stop/restart or mutate associated trader")
			}
			if err := s.store.GormDB().Model(&store.Trader{}).Where("id = ?", trader.ID).Update("is_running", false).Error; err != nil {
				t.Fatal(err)
			}
			w = call(router, "PUT", "/strategies/strategy", "alice", tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("stopped trader update should succeed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestStrategyUpdateRunningAIAndUnrelatedGridRemainEditable(t *testing.T) {
	s, router := coinPoolServer(t)
	router.PUT("/strategies/:id", s.handleUpdateStrategy)
	original := createUpdateSafetyStrategy(t, s, "ai_trading")
	for _, cfg := range []*store.Trader{
		{ID: "ai", UserID: "alice", Name: "AI", StrategyID: original.ID, IsRunning: true},
		{ID: "unrelated", UserID: "alice", Name: "other grid", StrategyID: "other-strategy", IsRunning: true},
	} {
		if err := s.store.Trader().Create(cfg); err != nil {
			t.Fatal(err)
		}
	}
	w := call(router, "PUT", "/strategies/strategy", "alice", `{"config":{"ai_config":{"custom_prompt":"Updated AI rule"}}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("AI rule-only update refused: %d %s", w.Code, w.Body.String())
	}
}

func TestTraderPromptHandlerRejectsGridAndForeignWritesBeforePersistence(t *testing.T) {
	s, router := coinPoolServer(t)
	router.PUT("/traders/:id/prompt", s.handleUpdateTraderPrompt)
	strategy := createUpdateSafetyStrategy(t, s, "grid_trading")
	cfg := &store.Trader{ID: "grid", UserID: "alice", Name: "grid", StrategyID: strategy.ID, CustomPrompt: "legacy rule", OverrideBasePrompt: true}
	if err := s.store.Trader().Create(cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := s.store.Trader().GetByID("grid")
	for _, body := range []string{`{"custom_prompt":"new rule"}`, `{"override_base_prompt":true}`} {
		w := call(router, "PUT", "/traders/grid/prompt", "alice", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not supported") {
			t.Fatalf("unsupported prompt should fail: %d %s", w.Code, w.Body.String())
		}
		after, _ := s.store.Trader().GetByID("grid")
		if !reflect.DeepEqual(before, after) {
			t.Fatal("rejected grid prompt changed DB")
		}
	}
	if w := call(router, "PUT", "/traders/grid/prompt", "bob", `{"custom_prompt":"foreign"}`); w.Code != http.StatusNotFound {
		t.Fatalf("foreign write status=%d", w.Code)
	}
	if w := call(router, "PUT", "/traders/missing/prompt", "alice", `{}`); w.Code != http.StatusNotFound {
		t.Fatalf("missing trader falsely succeeded: %d", w.Code)
	}
	w := call(router, "PUT", "/traders/grid/prompt", "alice", `{"custom_prompt":"","override_base_prompt":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("clear legacy grid settings: %d %s", w.Code, w.Body.String())
	}
	after, _ := s.store.Trader().GetByID("grid")
	if after.CustomPrompt != "" || after.OverrideBasePrompt {
		t.Fatal("clear did not persist")
	}
}

func TestStrategyCannotSwitchToGridWithUnconsumableTraderRules(t *testing.T) {
	s, router := coinPoolServer(t)
	router.PUT("/strategies/:id", s.handleUpdateStrategy)
	original := createUpdateSafetyStrategy(t, s, "ai_trading")
	if err := s.store.Trader().Create(&store.Trader{ID: "ai", UserID: "alice", Name: "AI", StrategyID: original.ID, CustomPrompt: "local rules"}); err != nil {
		t.Fatal(err)
	}
	w := call(router, "PUT", "/strategies/strategy", "alice", `{"config":{"strategy_type":"grid_trading","grid_config":{"symbol":"BTCUSDT"}}}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "not supported") {
		t.Fatalf("unconsumable trader rules must reject grid switch: %d %s", w.Code, w.Body.String())
	}
	assertStrategyUnchanged(t, s, original)
}

func TestTraderPromptHandlerPersistsAIAppendOverrideClearAndIsolation(t *testing.T) {
	s, router := coinPoolServer(t)
	router.PUT("/traders/:id/prompt", s.handleUpdateTraderPrompt)
	strategy := createUpdateSafetyStrategy(t, s, "ai_trading")
	for _, id := range []string{"a", "b"} {
		if err := s.store.Trader().Create(&store.Trader{ID: id, UserID: "alice", Name: id, StrategyID: strategy.ID}); err != nil {
			t.Fatal(err)
		}
	}
	otherBefore, _ := s.store.Trader().GetByID("b")
	for _, tc := range []struct {
		prompt   string
		override bool
	}{{"只在突破后交易", false}, {"Override trader A", true}, {"", false}} {
		body, _ := json.Marshal(map[string]any{"custom_prompt": tc.prompt, "override_base_prompt": tc.override})
		w := call(router, "PUT", "/traders/a/prompt", "alice", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("AI prompt save: %d %s", w.Code, w.Body.String())
		}
		got, _ := s.store.Trader().GetByID("a")
		if got.CustomPrompt != tc.prompt || got.OverrideBasePrompt != tc.override {
			t.Fatalf("prompt not persisted: %+v", got)
		}
		otherAfter, _ := s.store.Trader().GetByID("b")
		if !reflect.DeepEqual(otherBefore, otherAfter) {
			t.Fatal("other trader changed")
		}
		assertStrategyUnchanged(t, s, strategy)
	}
}
