package market

import "testing"

func TestNormalizeForKeepsBinanceStockContracts(t *testing.T) {
	cases := []struct{ in, exchange, want string }{
		{"SNDKUSDT", "binance", "SNDKUSDT"},
		{"xyz:SNDK", "binance_paper", "SNDKUSDT"},
		{"sndk", "binance", "SNDKUSDT"},
		{"BTC", "binance", "BTCUSDT"},
		{"ETHUSDT", "binance", "ETHUSDT"},
		// Elsewhere unchanged.
		{"BTCUSDT", "hyperliquid", Normalize("BTCUSDT")},
		{"SNDKUSDT", "hyperliquid", Normalize("SNDKUSDT")},
		{"SNDKUSDT", "", Normalize("SNDKUSDT")},
	}
	for _, c := range cases {
		if got := NormalizeFor(c.in, c.exchange); got != c.want {
			t.Errorf("NormalizeFor(%q, %q) = %q, want %q", c.in, c.exchange, got, c.want)
		}
	}
	if Normalize("SNDKUSDT") != "xyz:SNDK" {
		t.Fatalf("premise: SNDK is a Hyperliquid xyz asset by default, got %q", Normalize("SNDKUSDT"))
	}
	if usesXyzMarket("SNDKUSDT", "binance") || !usesXyzMarket("SNDKUSDT", "hyperliquid") {
		t.Fatal("Binance stock contracts use Binance data, Hyperliquid uses xyz")
	}
}
