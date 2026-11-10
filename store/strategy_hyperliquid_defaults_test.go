package store

import "testing"

func TestDefaultStrategyDoesNotEnablePaidData(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	assertFreeStaticDefault(t, cfg)
	ind := cfg.Indicators
	if ind.NofxOSAPIKey != "" {
		t.Fatalf("default should not include a NofxOS API key")
	}
	if ind.EnableQuantData || ind.EnableQuantOI || ind.EnableQuantNetflow || ind.EnableOIRanking || ind.EnableNetFlowRanking || ind.EnablePriceRanking {
		t.Fatalf("default strategy must not enable paid NofxOS datasets: %+v", ind)
	}
	if !ind.EnableRawKlines {
		t.Fatalf("raw klines must stay enabled")
	}
}

func TestStaticDefaultSurvivesClampAndNormalize(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource.UseAI500 = true
	cfg.ClampLimits()
	assertFreeStaticDefault(t, cfg)
	if cfg.CoinSource.UseAI500 {
		t.Fatalf("static strategy must clear stale AI500 flag: %+v", cfg.CoinSource)
	}
}

func TestEmptyCoinSourceInfersStaticNotPaidSource(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource = CoinSourceConfig{}
	cfg.NormalizeProductSchema()
	if cfg.CoinSource.SourceType != "static" {
		t.Fatalf("empty coin source should infer static, got %+v", cfg.CoinSource)
	}
}

func TestUnknownCoinSourceFallsBackToStatic(t *testing.T) {
	cfg := GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "something_unknown"
	cfg.NormalizeProductSchema()
	if cfg.CoinSource.SourceType != "static" {
		t.Fatalf("unknown coin source should fall back to static, got %q", cfg.CoinSource.SourceType)
	}
}

func assertFreeStaticDefault(t *testing.T, cfg StrategyConfig) {
	t.Helper()
	if cfg.CoinSource.SourceType != "static" || len(cfg.CoinSource.StaticCoins) != 3 || cfg.CoinSource.StaticCoins[0] != "BTCUSDT" {
		t.Fatalf("coin source = %+v, want free static BTC/ETH/SOL list", cfg.CoinSource)
	}
}
