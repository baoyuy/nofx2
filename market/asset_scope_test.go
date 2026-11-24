package market

import "testing"

func TestAssetScopeIncludesETFsWithoutChangingExchangeSymbols(t *testing.T) {
	for _, s := range []string{"SQQQUSDT", "SOXSUSDT", "sqqq_usdt", "SOXS-USDT-SWAP", "CLUSDT", "xyz:SNDK"} {
		if !IsTradFiAsset(s) {
			t.Errorf("missed TradFi asset %s", s)
		}
	}
	for _, s := range []string{"BTCUSDT", "ADAUSDT", "HYPEUSDT", "1000PEPEUSDT", "UNIUSDT"} {
		if IsTradFiAsset(s) {
			t.Errorf("misclassified crypto %s", s)
		}
	}
	if NormalizeFor("SQQQUSDT", "binance_paper") != "SQQQUSDT" {
		t.Fatal("risk classification changed order symbol")
	}
}
