package market

import "strings"

// TradFiResolver, when set, reports whether a symbol is a stock, ETF or
// commodity contract from the exchange's own listing metadata. The API layer
// sets it at startup to Binance's exchangeInfo classifier (contractType
// TRADIFI_PERPETUAL / underlyingType other than COIN). It lives here as a hook
// because the Binance package imports this one, so this one cannot import it.
//
// The second result says whether the resolver actually knows the symbol. When
// it does not (metadata not loaded yet, or a symbol from another venue), the
// built-in name list below is used instead of guessing "crypto".
var TradFiResolver func(symbol string) (tradfi bool, known bool)

// IsTradFiAsset classifies risk scope independently of Hyperliquid naming.
//
// The exchange's own metadata is authoritative. The name list below used to be
// the only source and covered 8 ETF tickers plus Hyperliquid xyz stocks, so on
// Binance it missed 198 of 249 TRADIFI_PERPETUAL contracts (gold, silver,
// copper, most single stocks and leveraged stock ETFs). A "crypto only" scope
// then let those through, and they also leaked into the market benchmark.
func IsTradFiAsset(symbol string) bool {
	if TradFiResolver != nil {
		if tradfi, known := TradFiResolver(symbol); known {
			return tradfi
		}
	}
	return isTradFiByName(symbol)
}

// isTradFiByName is the fallback used before exchange metadata is available or
// for venues that do not publish it. It is deliberately conservative: it only
// recognises names it is sure about and otherwise reports crypto.
func isTradFiByName(symbol string) bool {
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
