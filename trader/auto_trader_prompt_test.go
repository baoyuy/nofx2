package trader

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"nofx/kernel"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
)

type promptCaptureClient struct {
	mcp.AIClient // Unused methods must never be reached by these offline tests.
	mu           sync.Mutex
	system, user string
}

func (m *promptCaptureClient) CallWithMessages(system, user string) (string, error) {
	m.mu.Lock()
	m.system, m.user = system, user
	m.mu.Unlock()
	return `<reasoning>Wait for a valid entry.</reasoning><decision>[{"symbol":"BTCUSDT","action":"wait","reasoning":"No entry"}]</decision>`, nil
}

func promptTestConfig() store.StrategyConfig {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "static", StaticCoins: []string{"BTCUSDT"}}
	cfg.Indicators = store.IndicatorConfig{}
	cfg.CustomPrompt = "STRATEGY_CUSTOM_RULE"
	cfg.PromptSections = store.PromptSectionsConfig{RoleDefinition: "STRATEGY_ROLE", TradingFrequency: "STRATEGY_FREQUENCY", EntryStandards: "STRATEGY_ENTRY", DecisionProcess: "STRATEGY_PROCESS"}
	return cfg
}
func promptTestContext() *kernel.Context {
	return &kernel.Context{Account: kernel.AccountInfo{TotalEquity: 1000, AvailableBalance: 1000}, CandidateCoins: []kernel.CandidateCoin{{Symbol: "BTCUSDT"}}, MarketDataMap: map[string]*market.Data{"BTCUSDT": {Symbol: "BTCUSDT", CurrentPrice: 100}}}
}
func promptTestTrader(cfg *store.StrategyConfig) (*AutoTrader, *promptCaptureClient) {
	client := &promptCaptureClient{}
	return &AutoTrader{config: AutoTraderConfig{StrategyConfig: cfg}, strategyEngine: kernel.NewStrategyEngine(cfg), mcpClient: client}, client
}
func promptDecision(t *testing.T, at *AutoTrader) *kernel.FullDecision {
	t.Helper()
	d, err := at.requestAIDecision(promptTestContext())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTraderPromptFinalMessagesAppendOverrideClearAndIsolation(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	cfg := promptTestConfig()
	cfg.RiskControl.MaxPositions = 999 // Analysis must clamp privately, not mutate cfg.
	before, _ := json.Marshal(cfg)
	a, client := promptTestTrader(&cfg)
	b, _ := promptTestTrader(&cfg)
	if err := a.SetCustomPrompt("只在确认突破后入场 TRADER_A"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetOverrideBasePrompt(false); err != nil {
		t.Fatal(err)
	}
	d := promptDecision(t, a)
	for _, phrase := range []string{"STRATEGY_CUSTOM_RULE", "STRATEGY_ROLE", "只在确认突破后入场 TRADER_A", "Hard Constraints (Risk Control)", "<decision>", "<reasoning>"} {
		if !strings.Contains(d.SystemPrompt, phrase) {
			t.Fatalf("final AI message missing %q", phrase)
		}
	}
	if client.system != d.SystemPrompt || client.user != d.UserPrompt || !strings.Contains(client.user, "BTCUSDT") {
		t.Fatal("did not capture actual final AI messages")
	}
	if strings.Contains(promptDecision(t, b).SystemPrompt, "TRADER_A") {
		t.Fatal("trader rules leaked through shared strategy")
	}
	if err := a.SetPromptSettings("TRADER_OVERRIDE", true); err != nil {
		t.Fatal(err)
	}
	d = promptDecision(t, a)
	for _, phrase := range []string{"STRATEGY_CUSTOM_RULE", "STRATEGY_ROLE", "STRATEGY_FREQUENCY", "STRATEGY_ENTRY", "STRATEGY_PROCESS"} {
		if strings.Contains(d.SystemPrompt, phrase) {
			t.Fatalf("override retained editable section %q", phrase)
		}
	}
	for _, phrase := range []string{"TRADER_OVERRIDE", "Data Dictionary", "Hard Constraints (Risk Control)", "Max Positions:", "<decision>", "<reasoning>", "cannot be overridden"} {
		if !strings.Contains(d.SystemPrompt, phrase) {
			t.Fatalf("override lost required contract %q", phrase)
		}
	}
	if err := a.SetCustomPrompt(""); err != nil {
		t.Fatal(err)
	}
	d = promptDecision(t, a)
	if !strings.Contains(d.SystemPrompt, "STRATEGY_CUSTOM_RULE") || strings.Contains(d.SystemPrompt, "TRADER_OVERRIDE") {
		t.Fatal("empty prompt with override flag must restore strategy, not erase rules")
	}
	if err := a.SetPromptSettings("", false); err != nil {
		t.Fatal(err)
	}
	if got := a.GetSystemPromptTemplate(); got != "custom" {
		t.Fatalf("template=%s", got)
	}
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Fatal("shared strategy was mutated by analysis or trader overlay")
	}
}

func TestTraderPromptConcurrentAtomicSettingsAndMessages(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	cfg := promptTestConfig()
	at, _ := promptTestTrader(&cfg)
	if err := at.SetPromptSettings("APPEND_RULE", false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 80; i++ {
			if err := at.SetPromptSettings("OVERRIDE_RULE", true); err != nil {
				errs <- err
				return
			}
			if err := at.SetPromptSettings("APPEND_RULE", false); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 80; i++ {
			d, err := at.requestAIDecision(promptTestContext())
			if err != nil {
				errs <- err
				return
			}
			hasBase := strings.Contains(d.SystemPrompt, "STRATEGY_CUSTOM_RULE")
			if strings.Contains(d.SystemPrompt, "OVERRIDE_RULE") == hasBase {
				errs <- fmt.Errorf("mixed prompt/override snapshot")
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestTraderPromptSurvivesStrategyReloadAndBlocksLiveTypeSwitch(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	st, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := promptTestConfig()
	raw, _ := json.Marshal(cfg)
	strategy := &store.Strategy{ID: "strategy", UserID: "alice", Name: "rules", Config: string(raw)}
	if err := st.Strategy().Create(strategy); err != nil {
		t.Fatal(err)
	}
	at, _ := promptTestTrader(&cfg)
	at.store = st
	at.userID = "alice"
	at.config.StrategyID = strategy.ID
	at.config.StrategyConfigRaw = string(raw)
	if err := at.SetPromptSettings("TRADER_LOCAL", false); err != nil {
		t.Fatal(err)
	}
	cfg.CustomPrompt = "RELOADED_STRATEGY"
	raw, _ = json.Marshal(cfg)
	strategy.Config = string(raw)
	if err := st.Strategy().Update(strategy); err != nil {
		t.Fatal(err)
	}
	if err := at.reloadStrategyConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	d := promptDecision(t, at)
	if !strings.Contains(d.SystemPrompt, "RELOADED_STRATEGY") || !strings.Contains(d.SystemPrompt, "TRADER_LOCAL") {
		t.Fatal("reload lost strategy/trader rules")
	}
	if err := at.SetPromptSettings("TRADER_LOCAL", true); err != nil {
		t.Fatal(err)
	}
	cfg.CustomPrompt = "SECOND_RELOAD"
	raw, _ = json.Marshal(cfg)
	strategy.Config = string(raw)
	if err := st.Strategy().Update(strategy); err != nil {
		t.Fatal(err)
	}
	if err := at.reloadStrategyConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	d = promptDecision(t, at)
	if !strings.Contains(d.SystemPrompt, "TRADER_LOCAL") || strings.Contains(d.SystemPrompt, "SECOND_RELOAD") {
		t.Fatal("reload lost override mode")
	}
	cfg.StrategyType = "grid_trading"
	cfg.GridConfig = &store.GridStrategyConfig{Symbol: "BTCUSDT"}
	raw, _ = json.Marshal(cfg)
	strategy.Config = string(raw)
	if err := st.Strategy().Update(strategy); err != nil {
		t.Fatal(err)
	}
	at.isRunning = true
	if err := at.reloadStrategyConfigIfChanged(); err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("live type switch must fail: %v", err)
	}
	if at.GetStrategyConfig().StrategyType == "grid_trading" {
		t.Fatal("rejected switch changed runtime")
	}
}

func TestTraderPromptGridCapabilityRejectedBeforeSideEffects(t *testing.T) {
	cfg := store.StrategyConfig{StrategyType: "grid_trading", GridConfig: &store.GridStrategyConfig{Symbol: "BTCUSDT"}}
	at, _ := promptTestTrader(&cfg)
	if err := at.SetCustomPrompt("ignored rule"); err == nil {
		t.Fatal("grid custom prompt accepted")
	}
	if err := at.SetOverrideBasePrompt(true); err == nil {
		t.Fatal("grid override accepted")
	}
	if err := at.SetPromptSettings("", false); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Trader().Create(&store.Trader{ID: "grid", UserID: "alice", Name: "grid", CustomPrompt: "legacy ignored rule"}); err != nil {
		t.Fatal(err)
	}
	_, err = NewAutoTrader(AutoTraderConfig{ID: "grid", StrategyConfig: &cfg}, st, "alice")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("must reject persisted unsupported prompt before constructing exchange clients: %v", err)
	}
}

func TestTraderPromptRestoredFromPersistedSettings(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	st, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Trader().Create(&store.Trader{ID: "ai", UserID: "alice", Name: "AI", CustomPrompt: "PERSISTED_RULE", OverrideBasePrompt: true}); err != nil {
		t.Fatal(err)
	}
	cfg := promptTestConfig()
	// Mirror the existing manager's store -> compatibility setter load path.
	load := func() *AutoTrader {
		t.Helper()
		persisted, err := st.Trader().GetByID("ai")
		if err != nil {
			t.Fatal(err)
		}
		at, _ := promptTestTrader(&cfg)
		if err := at.SetCustomPrompt(persisted.CustomPrompt); err != nil {
			t.Fatal(err)
		}
		if err := at.SetOverrideBasePrompt(persisted.OverrideBasePrompt); err != nil {
			t.Fatal(err)
		}
		return at
	}
	d := promptDecision(t, load())
	if !strings.Contains(d.SystemPrompt, "PERSISTED_RULE") || strings.Contains(d.SystemPrompt, "STRATEGY_CUSTOM_RULE") {
		t.Fatal("persisted override was not restored into final messages")
	}
	if err := st.Trader().UpdateCustomPrompt("alice", "ai", "", false); err != nil {
		t.Fatal(err)
	}
	d = promptDecision(t, load())
	if strings.Contains(d.SystemPrompt, "PERSISTED_RULE") || !strings.Contains(d.SystemPrompt, "STRATEGY_CUSTOM_RULE") {
		t.Fatal("persisted clear was not restored")
	}
}

func TestTraderPromptConcurrentSharedStrategyIsolation(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	cfg := promptTestConfig()
	cfg.RiskControl.MaxPositions = 999
	a, _ := promptTestTrader(&cfg)
	b, _ := promptTestTrader(&cfg)
	if err := a.SetPromptSettings("UNIQUE_A", false); err != nil {
		t.Fatal(err)
	}
	if err := b.SetPromptSettings("UNIQUE_B", true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, at := range []*AutoTrader{a, b} {
		wg.Add(1)
		go func(at *AutoTrader) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				d, err := at.requestAIDecision(promptTestContext())
				if err != nil {
					errs <- err
					return
				}
				want, other := "UNIQUE_A", "UNIQUE_B"
				if at == b {
					want, other = other, want
				}
				if !strings.Contains(d.SystemPrompt, want) || strings.Contains(d.SystemPrompt, other) {
					errs <- fmt.Errorf("cross-trader rule leak")
					return
				}
			}
		}(at)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if cfg.RiskControl.MaxPositions != 999 {
		t.Fatal("shared strategy mutated")
	}
}

func TestTraderPromptTokenOverflowBlocksBeforeModelCall(t *testing.T) {
	t.Setenv("CLAW402_WALLET_KEY", "")
	cfg := promptTestConfig()
	at, client := promptTestTrader(&cfg)
	if err := at.SetPromptSettings(strings.Repeat("x", 300000), false); err != nil {
		t.Fatal(err)
	}
	decision, err := at.requestAIDecision(promptTestContext())
	if err == nil || !strings.Contains(err.Error(), "context limit") || decision != nil {
		t.Fatalf("expected pre-call token rejection: %v", err)
	}
	if client.system != "" || client.user != "" {
		t.Fatal("oversized trader rules reached model")
	}
}
