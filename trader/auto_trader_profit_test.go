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
