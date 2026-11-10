package market

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nofx/internal/apischeduler"
	"strconv"
	"strings"
	"time"
)

// Binance lists stocks and commodities (SNDK, MU, AAPL, BZ…) as ordinary
// USDT-margined perpetuals (contract type TRADIFI_PERPETUAL). Normalize maps
// those tickers to Hyperliquid's xyz: market; on Binance they must stay
// SNDKUSDT and use Binance's own candles.

// binanceFamily tells whether exchange trades Binance USDT-M symbols.
func binanceFamily(exchange string) bool {
	switch strings.ToLower(strings.TrimSpace(exchange)) {
	case "binance", "binance_paper":
		return true
	}
	return false
}

// NormalizeFor is Normalize for one exchange: on Binance every contract,
// stocks included, is BASEUSDT; elsewhere it is Normalize.
func NormalizeFor(symbol, exchange string) string {
	if !binanceFamily(exchange) {
		return Normalize(symbol)
	}
	s := strings.ToUpper(strings.TrimSpace(symbol))
	s = strings.TrimPrefix(s, "XYZ:")
	for _, r := range []string{"_", "-SWAP", "-USDC", "-"} {
		s = strings.ReplaceAll(s, r, "")
	}
	if strings.HasSuffix(s, "USDC") {
		s = strings.TrimSuffix(s, "USDC")
	}
	if !strings.HasSuffix(s, "USDT") {
		s += "USDT"
	}
	return s
}

// usesXyzMarket tells whether symbol's data comes from Hyperliquid's xyz:
// market for this exchange.
func usesXyzMarket(symbol, exchange string) bool {
	return !binanceFamily(exchange) && IsXyzDexAsset(symbol)
}

var binanceKlineClient = &http.Client{Timeout: 15 * time.Second}

// getKlinesFromBinance reads USDT-M futures candles straight from Binance.
// Used for the stock and commodity contracts that CoinAnk does not carry.
func getKlinesFromBinance(symbol, interval string, limit int) ([]Kline, error) {
	if limit <= 0 || limit > 1500 {
		limit = 500
	}
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/klines?symbol=%s&interval=%s&limit=%d", symbol, interval, limit)
	resp, err := apischeduler.Get(binanceKlineClient, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance klines %s: HTTP %d: %s", symbol, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rows [][]json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	num := func(r json.RawMessage) float64 {
		var s string
		if json.Unmarshal(r, &s) == nil {
			v, _ := strconv.ParseFloat(s, 64)
			return v
		}
		var v float64
		_ = json.Unmarshal(r, &v)
		return v
	}
	out := make([]Kline, 0, len(rows))
	for _, r := range rows {
		if len(r) < 7 {
			continue
		}
		out = append(out, Kline{OpenTime: int64(num(r[0])), Open: num(r[1]), High: num(r[2]), Low: num(r[3]),
			Close: num(r[4]), Volume: num(r[5]), CloseTime: int64(num(r[6]))})
	}
	return out, nil
}

// getKlinesFor picks the candle source for symbol on exchange: Hyperliquid
// for its xyz: market, Binance itself for Binance stock contracts, CoinAnk
// otherwise.
func getKlinesFor(symbol, interval, exchange string, limit int) ([]Kline, error) {
	if usesXyzMarket(symbol, exchange) {
		return getKlinesFromHyperliquid(symbol, interval, limit)
	}
	if binanceFamily(exchange) && IsXyzDexAsset(symbol) {
		return getKlinesFromBinance(symbol, interval, limit)
	}
	// Multi-timeframe data has always come from Binance via CoinAnk.
	return getKlinesFromCoinAnk(symbol, interval, "binance", limit)
}
