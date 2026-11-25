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

// The exchange's own metadata decides the scope; the name list is only the
// fallback. Before this, a crypto-only scope let most Binance stock and
// commodity contracts through because they are not in the name list.
func TestTradFiResolverOverridesTheNameList(t *testing.T) {
	t.Cleanup(func() { TradFiResolver = nil })
	listing := map[string]bool{ // what the exchange says is TradFi
		"XAUUSDT": true, "COPPERUSDT": true, "SKHYUSDT": true, "MUUSDT": true,
		"BTCUSDT": false, "UNIUSDT": false,
	}
	TradFiResolver = func(symbol string) (bool, bool) {
		v, ok := listing[symbol]
		return v, ok
	}
	for sym, want := range listing {
		if got := IsTradFiAsset(sym); got != want {
			t.Errorf("%s: IsTradFiAsset = %v, want %v (exchange metadata)", sym, got, want)
		}
	}
	// A symbol the exchange does not list falls back to the name rules, so a
	// known ETF ticker and a Hyperliquid stock are still caught.
	for _, sym := range []string{"SQQQUSDT", "xyz:SNDK"} {
		if !IsTradFiAsset(sym) {
			t.Errorf("%s: unknown to the exchange must fall back to the name list", sym)
		}
	}
	if IsTradFiAsset("SOMENEWCOINUSDT") {
		t.Error("an unlisted, unknown symbol must not be called TradFi")
	}
}

// A resolver that cannot classify anything (metadata not loaded) must not turn
// every stock contract into crypto: the name list still applies.
func TestUnloadedResolverKeepsNameListBehaviour(t *testing.T) {
	t.Cleanup(func() { TradFiResolver = nil })
	TradFiResolver = func(string) (bool, bool) { return false, false }
	if !IsTradFiAsset("SQQQUSDT") {
		t.Error("name list must still catch SQQQ while metadata is unavailable")
	}
	if IsTradFiAsset("BTCUSDT") {
		t.Error("BTC must stay crypto")
	}
}
