package trader

import (
	"nofx/store"
	"testing"
)

func TestProfitProtectionActivationAndPermanentArming(t *testing.T) {
	state := &store.PositionProfitState{EntryPrice: 100, Side: "long", Eligible: true, ConfigJSON: `{"enabled":true,"breakeven_activation_pct":2,"breakeven_lock_pct":0,"trailing_activation_pct":5,"trailing_giveback_pct":40}`}
	if advanceProfitProtection(state, 2.1, true) {
		t.Fatal("unexpected legacy close")
	}
	if !state.BreakevenArmed || state.StopPrice <= 100 {
		t.Fatalf("breakeven did not arm net of costs: %+v", state)
	}
	floor := state.StopPrice
	advanceProfitProtection(state, 1, true)
	if state.StopPrice < floor {
		t.Fatalf("stop loosened below activation: %v < %v", state.StopPrice, floor)
	}
	state.PeakPricePct = 10
	advanceProfitProtection(state, 5, true)
	if !state.TrailingArmed || state.StopPrice < 106 {
		t.Fatalf("trailing floor missing after peak 10%%: %+v", state)
	}
	advanceProfitProtection(state, 5, true)
	if !shouldDrawdownClose(state.PeakPricePct, (state.PeakPricePct-5)/state.PeakPricePct*100) {
		t.Fatal("historical legacy peak failed to arm")
	}
}

func TestProfitProtectionShortAndDisabledIsolation(t *testing.T) {
	short := &store.PositionProfitState{EntryPrice: 100, Side: "short", Eligible: true, ConfigJSON: `{"enabled":true,"breakeven_activation_pct":2,"trailing_activation_pct":5,"trailing_giveback_pct":40}`}
	advanceProfitProtection(short, 5, true)
	if !short.TrailingArmed || short.StopPrice >= 100 {
		t.Fatalf("short stop not below entry: %+v", short)
	}
	before := *short
	short.ConfigJSON = `{"enabled":false}`
	short.StopPrice = 0
	advanceProfitProtection(short, 0, false)
	if short.BreakevenArmed != before.BreakevenArmed || short.TrailingArmed != before.TrailingArmed {
		t.Fatal("disabled protection should not newly alter arms")
	}
	old := short.StopPrice
	if got := tighterProfitStop("short", old, 99); got != 99 {
		t.Fatalf("short stop should tighten downward; got %v", got)
	}
	if got := tighterProfitStop("long", 101, 100); got != 101 {
		t.Fatalf("long stop was loosened: %v", got)
	}
}

func TestRProtectionUsesImmutableInitialRisk(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		state := &store.PositionProfitState{EntryPrice: 100, Side: side, Eligible: true, InitialRiskPct: 2, ConfigJSON: `{"enabled":true,"mode":"r","breakeven_activation_r":1,"trailing_activation_r":2,"trailing_giveback_pct":40}`}
		advanceProfitProtection(state, 1.9, true)
		if state.BreakevenArmed {
			t.Fatal("armed before 1R")
		}
		advanceProfitProtection(state, 2, true)
		if !state.BreakevenArmed || state.TrailingArmed {
			t.Fatal("wrong 1R state")
		}
		advanceProfitProtection(state, 4, true)
		if !state.TrailingArmed {
			t.Fatal("not armed at 2R")
		}
		stop := state.StopPrice
		advanceProfitProtection(state, 0, true)
		if state.StopPrice != stop || state.InitialRiskPct != 2 {
			t.Fatal("risk or stop reset on giveback")
		}
	}
	unknown := &store.PositionProfitState{EntryPrice: 100, Side: "long", Eligible: true, ConfigJSON: `{"enabled":true,"mode":"r"}`}
	advanceProfitProtection(unknown, 4, true)
	if unknown.BreakevenArmed || unknown.TrailingArmed {
		t.Fatal("invented initial risk")
	}
}

// Deterministic illustration of a 28.69% leveraged peak at 10x followed by a reversal.
// This verifies the mechanism, not a forecast or historical execution performance.
func TestRProtectionPreservesFloorBelowLegacyActivation(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		state := &store.PositionProfitState{EntryPrice: 100, Side: side, Eligible: true, InitialRiskPct: 1, ConfigJSON: `{"enabled":true,"mode":"r","breakeven_activation_r":1,"trailing_activation_r":2,"trailing_giveback_pct":30}`}
		for _, move := range []float64{0.3, 1.2, 2.869, 1.5, -0.8} {
			advanceProfitProtection(state, move, true)
		}
		if state.LegacyArmed {
			t.Fatal("legacy price threshold unexpectedly armed")
		}
		if !state.BreakevenArmed || !state.TrailingArmed {
			t.Fatal("R protection failed to arm")
		}
		if side == "long" && state.StopPrice < 102 {
			t.Fatalf("long profit floor missing: %f", state.StopPrice)
		}
		if side == "short" && state.StopPrice > 98 {
			t.Fatalf("short profit floor missing: %f", state.StopPrice)
		}
	}
}
