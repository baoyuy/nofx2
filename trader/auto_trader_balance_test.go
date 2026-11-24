package trader

import "testing"

func TestExtractInitialBalancePrefersExplicitTotalEquity(t *testing.T) {
	account := map[string]interface{}{
		"totalEquity":           307.571392,
		"totalWalletBalance":    305.729922,
		"totalUnrealizedProfit": 1.84147,
	}

	got := extractInitialBalance(account)
	if got != 307.571392 {
		t.Fatalf("expected mark-to-market total equity, got %.6f", got)
	}
}

func TestExtractInitialBalanceSupportsLegacyExchangeFields(t *testing.T) {
	account := map[string]interface{}{
		"totalWalletBalance": 100.5,
	}

	got := extractInitialBalance(account)
	if got != 100.5 {
		t.Fatalf("expected legacy wallet balance fallback, got %.6f", got)
	}
}

func TestPositionROIUsesEntryCostWithoutChangingCurrentMargin(t *testing.T) {
	positions := []map[string]interface{}{{"symbol": "SOLUSDT", "side": "long", "entryPrice": 100.0, "markPrice": 110.0, "positionAmt": 2.0, "leverage": 10.0, "unRealizedProfit": 20.0}}
	row := FormatPositions(positions)[0]
	if row["unrealized_pnl_pct"] != 100.0 {
		t.Fatalf("ROI should be 100%%: %v", row)
	}
	if row["margin_used"] != 22.0 {
		t.Fatalf("current margin must remain 22: %v", row)
	}
}
