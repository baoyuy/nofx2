package binance

import "testing"

func TestPaperProfitProtectionTightensAndClosesOnce(t *testing.T) {
	pt, _ := newTestPaperTrader(t, t.TempDir())
	if _, err := pt.OpenLong("BTCUSDT", 0.01, 2); err != nil {
		t.Fatal(err)
	}
	expected, ok := pt.ProfitPosition("BTCUSDT", "long")
	if !ok {
		t.Fatal("missing position")
	}
	stop := PaperBreakevenStop(expected.EntryPrice, "long", 0)
	actual, closed, err := pt.ApplyProfitProtection(expected, expected.EntryPrice*1.03, stop, false)
	if err != nil || closed || actual != stop {
		t.Fatalf("set protected stop: stop=%v closed=%v err=%v", actual, closed, err)
	}
	looser := expected.EntryPrice * 0.99
	actual, closed, err = pt.ApplyProfitProtection(expected, expected.EntryPrice*1.02, looser, false)
	if err != nil || closed || actual != stop {
		t.Fatalf("looser floor replaced stop: %v %v %v", actual, closed, err)
	}
	actual, closed, err = pt.ApplyProfitProtection(expected, stop, 0, false)
	if err != nil || !closed {
		t.Fatalf("crossed stop should close atomically: closed=%v err=%v", closed, err)
	}
	_, closed, err = pt.ApplyProfitProtection(expected, stop, 0, true)
	if err == nil || closed {
		t.Fatal("duplicate/stale exit should be rejected")
	}
}

func TestPaperProfitProtectionFeeAdjustedFloorsBothSides(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		stop := PaperBreakevenStop(100, side, 0)
		// Fill at stop then pay close fee: net PnL must be nonnegative within roundoff.
		fill := stop * (1 - paperSlippageRate)
		if side == "short" {
			fill = stop * (1 + paperSlippageRate)
		}
		gross := pnlFor(side, 1, 100, fill)
		net := gross - 100*paperTakerFeeRate - fill*paperTakerFeeRate
		if net < -1e-9 {
			t.Fatalf("%s breakeven stop does not cover fees/slippage: stop %.8f net %.8f", side, stop, net)
		}
	}
}
