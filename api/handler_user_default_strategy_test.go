package api

import (
	"testing"

	"github.com/google/uuid"
	"nofx/store"
)

func TestCreateDefaultStrategiesUsesOneReadyToRunFreePreset(t *testing.T) {
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s := &Server{store: st}
	userID := "user-us-stock-presets"
	if err := s.createDefaultStrategies(userID, "zh"); err != nil {
		t.Fatalf("createDefaultStrategies failed: %v", err)
	}

	strategies, err := st.Strategy().List(userID)
	if err != nil {
		t.Fatalf("List strategies failed: %v", err)
	}
	if len(strategies) != 1 {
		t.Fatalf("expected 1 default strategy, got %d", len(strategies))
	}

	byName := map[string]*store.Strategy{}
	activeCount := 0
	for _, strategy := range strategies {
		byName[strategy.Name] = strategy
		if strategy.IsActive {
			activeCount++
		}
		if strategy.Name == "Balanced Strategy" || strategy.Name == "Steady Strategy" || strategy.Name == "Aggressive Strategy" {
			t.Fatalf("legacy crypto-style default strategy still present: %s", strategy.Name)
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active strategy, got %d", activeCount)
	}

	defaultStrategy := byName["固定币种策略"]
	if defaultStrategy == nil || !defaultStrategy.IsActive {
		t.Fatalf("free fixed-coins strategy should exist and be active")
	}
	trendCfg, err := defaultStrategy.ParseConfig()
	if err != nil {
		t.Fatalf("default ParseConfig failed: %v", err)
	}
	if trendCfg.CoinSource.SourceType != "static" || len(trendCfg.CoinSource.StaticCoins) != 3 {
		t.Fatalf("default strategy should use the free static coin list, got %+v", trendCfg.CoinSource)
	}
	if trendCfg.CoinSource.UseAI500 || trendCfg.CoinSource.UseOITop || trendCfg.RiskControl.MaxPositions != store.AutopilotDefaultMaxPositions {
		t.Fatalf("default strategy must not use paid sources, got coin=%+v risk=%+v", trendCfg.CoinSource, trendCfg.RiskControl)
	}
	if trendCfg.RiskControl.BTCETHMaxLeverage != 10 || trendCfg.RiskControl.AltcoinMaxLeverage != 10 {
		t.Fatalf("default strategy should use 10x leverage, got risk=%+v", trendCfg.RiskControl)
	}
	if trendCfg.RiskControl.BTCETHMaxPositionValueRatio != store.AutopilotMaxPositionValueRatio ||
		trendCfg.RiskControl.AltcoinMaxPositionValueRatio != store.AutopilotMaxPositionValueRatio ||
		trendCfg.RiskControl.MaxMarginUsage != 1.0 {
		t.Fatalf("default strategy should enforce a 5x-equity hard cap per position, got risk=%+v", trendCfg.RiskControl)
	}
}

func TestCreateDefaultStrategiesMigratesExistingTwoPositionAutopilot(t *testing.T) {
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	userID := "user-legacy-autopilot"
	legacyConfig := store.GetDefaultStrategyConfig("en")
	// Legacy books were saved by the upstream Claw402 preset.
	legacyConfig.CoinSource.SourceType = "vergex_signal"
	legacyConfig.RiskControl.MaxPositions = 2
	legacyConfig.RiskControl.BTCETHMaxPositionValueRatio = 5
	legacyConfig.RiskControl.AltcoinMaxPositionValueRatio = 5
	legacy := &store.Strategy{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        "Fixed Coins Strategy",
		Description: "legacy two-position config",
		IsActive:    true,
	}
	if err := legacy.SetConfig(&legacyConfig); err != nil {
		t.Fatalf("legacy SetConfig failed: %v", err)
	}
	if err := st.Strategy().Create(legacy); err != nil {
		t.Fatalf("create legacy strategy failed: %v", err)
	}

	s := &Server{store: st}
	if err := s.createDefaultStrategies(userID, "en"); err != nil {
		t.Fatalf("createDefaultStrategies failed: %v", err)
	}

	migrated, err := st.Strategy().Get(userID, legacy.ID)
	if err != nil {
		t.Fatalf("get migrated strategy failed: %v", err)
	}
	migratedConfig, err := migrated.ParseConfig()
	if err != nil {
		t.Fatalf("parse migrated strategy failed: %v", err)
	}
	if migratedConfig.RiskControl.MaxPositions != store.AutopilotDefaultMaxPositions ||
		migratedConfig.RiskControl.BTCETHMaxPositionValueRatio != store.AutopilotMaxPositionValueRatio ||
		migratedConfig.RiskControl.AltcoinMaxPositionValueRatio != store.AutopilotMaxPositionValueRatio {
		t.Fatalf("legacy strategy was not migrated: %+v", migratedConfig.RiskControl)
	}
}

func TestCreateDefaultStrategiesMigratesLegacyPresetsWithoutOverridingActiveCustom(t *testing.T) {
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	userID := "user-existing-custom"
	legacyCfg := store.GetDefaultStrategyConfig("zh")
	legacy := &store.Strategy{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        "Balanced Strategy",
		Description: "legacy",
		IsActive:    false,
	}
	if err := legacy.SetConfig(&legacyCfg); err != nil {
		t.Fatalf("legacy SetConfig failed: %v", err)
	}
	if err := st.Strategy().Create(legacy); err != nil {
		t.Fatalf("create legacy failed: %v", err)
	}

	custom := &store.Strategy{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        "aa",
		Description: "user custom active strategy",
		IsActive:    true,
	}
	if err := custom.SetConfig(&legacyCfg); err != nil {
		t.Fatalf("custom SetConfig failed: %v", err)
	}
	if err := st.Strategy().Create(custom); err != nil {
		t.Fatalf("create custom failed: %v", err)
	}

	s := &Server{store: st}
	if err := s.createDefaultStrategies(userID, "zh"); err != nil {
		t.Fatalf("createDefaultStrategies failed: %v", err)
	}
	if err := s.createDefaultStrategies(userID, "zh"); err != nil {
		t.Fatalf("second createDefaultStrategies should be idempotent: %v", err)
	}

	strategies, err := st.Strategy().List(userID)
	if err != nil {
		t.Fatalf("List strategies failed: %v", err)
	}
	byName := map[string]int{}
	activeNames := []string{}
	for _, strategy := range strategies {
		byName[strategy.Name]++
		if strategy.IsActive {
			activeNames = append(activeNames, strategy.Name)
		}
	}
	if byName["Balanced Strategy"] != 0 {
		t.Fatalf("legacy preset should be removed, got names=%+v", byName)
	}
	if byName["固定币种策略"] != 1 {
		t.Fatalf("expected exactly one free fixed-coins strategy, got names=%+v", byName)
	}
	if len(activeNames) != 1 || activeNames[0] != "aa" {
		t.Fatalf("existing active custom strategy should stay the only active one, got %+v", activeNames)
	}
}
