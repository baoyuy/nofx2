package market

import "strings"

// IsTradFiAsset classifies risk scope independently of Hyperliquid naming.
// Some Binance equity ETFs are not listed on the XYZ dex; treating their
// USDT suffix as proof of a crypto underlying would bypass crypto-only pools.
func IsTradFiAsset(symbol string) bool {
	if IsXyzDexAsset(symbol) {
		return true
	}
	base := strings.ToUpper(strings.TrimSpace(symbol))
	for _, suffix := range []string{"-USDT-SWAP", "_USDT", "-USDT", "USDT", "-USDC", "USDC", "USD"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			break
		}
	}
	switch base {
	case "SQQQ", "SOXS", "TQQQ", "SOXL", "QQQ", "SPY", "IWM", "DIA":
		return true
	}
	return false
}
