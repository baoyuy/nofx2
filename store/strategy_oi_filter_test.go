package store

import (
	"encoding/json"
	"testing"
)

func TestMinOIValueMillionsDefaultsAndClamp(t *testing.T) {
	// Saved strategies without the field keep the historical 15M filter.
	var old StrategyConfig
	if err := json.Unmarshal([]byte(`{"strategy_type":"ai_trading","ai_config":{"risk_control":{"min_confidence":78}}}`), &old); err != nil {
		t.Fatal(err)
	}
	if got := old.RiskControl.MinOIValueMillionsOrDefault(); got != DefaultMinOIValueMillions {
		t.Fatalf("missing field = %v, want %v", got, DefaultMinOIValueMillions)
	}

	// A saved value is read back, and an explicit 0 survives a save/load round trip.
	var saved StrategyConfig
	if err := json.Unmarshal([]byte(`{"strategy_type":"ai_trading","ai_config":{"risk_control":{"min_oi_value_millions":2}}}`), &saved); err != nil {
		t.Fatal(err)
	}
	if got := saved.RiskControl.MinOIValueMillionsOrDefault(); got != 2 {
		t.Fatalf("saved value = %v, want 2", got)
	}
	zero := 0.0
	saved.RiskControl.MinOIValueMillions = &zero
	raw, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded StrategyConfig
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.RiskControl.MinOIValueMillionsOrDefault(); got != 0 {
		t.Fatalf("round-tripped 0 = %v, want 0 (filter off)", got)
	}

	for _, tt := range []struct {
		in, want float64
	}{
		{2, 2},
		{0, 0},
		{-5, 0},
		{5000, MaxMinOIValueMillions},
	} {
		v := tt.in
		cfg := GetDefaultStrategyConfig("zh")
		cfg.RiskControl.MinOIValueMillions = &v
		cfg.ClampLimits()
		if got := cfg.RiskControl.MinOIValueMillionsOrDefault(); got != tt.want {
			t.Fatalf("clamp(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
