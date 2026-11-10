package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The market page must read USDT-M perpetuals, the contracts strategies
// trade, not spot pairs.
func TestPublicMarketReadsUSDTPerpetuals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			fmt.Fprint(w, `{"symbols":[`+
				`{"symbol":"BTCUSDT","status":"TRADING","quoteAsset":"USDT","contractType":"PERPETUAL"},`+
				`{"symbol":"BTCUSDT_261225","status":"TRADING","quoteAsset":"USDT","contractType":"CURRENT_QUARTER"},`+
				`{"symbol":"TSLAUSDT","status":"TRADING","quoteAsset":"USDT","contractType":"TRADIFI_PERPETUAL"},`+
				`{"symbol":"OLDUSDT","status":"SETTLING","quoteAsset":"USDT","contractType":"PERPETUAL"}]}`)
		case "/fapi/v1/ticker/24hr":
			if r.URL.Query().Get("symbol") == "BTCUSDT" {
				fmt.Fprint(w, `{"symbol":"BTCUSDT","lastPrice":"78000","priceChangePercent":"1.5","quoteVolume":"9"}`)
				return
			}
			fmt.Fprint(w, `[{"symbol":"BTCUSDT","lastPrice":"78000","priceChangePercent":"1.5","quoteVolume":"9"},`+
				`{"symbol":"TSLAUSDT","lastPrice":"300","priceChangePercent":"40","quoteVolume":"1"}]`)
		case "/fapi/v1/premiumIndex":
			fmt.Fprint(w, `{"symbol":"BTCUSDT","markPrice":"78010","lastFundingRate":"0.0001","nextFundingTime":1793376000000}`)
		case "/fapi/v1/klines":
			fmt.Fprint(w, `[[1793340000000,"1","2","0.5","1.5","10"]]`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	previous := publicBaseURL
	publicBaseURL = server.URL
	t.Cleanup(func() {
		publicBaseURL = previous
		symbolsMu.Lock()
		symbolsCache, symbolsCachedAt = nil, time.Time{}
		symbolsMu.Unlock()
		tickersMu.Lock()
		tickersCache, tickersCachedAt = nil, time.Time{}
		tickersMu.Unlock()
	})
	symbolsMu.Lock()
	symbolsCache = nil
	symbolsMu.Unlock()
	tickersMu.Lock()
	tickersCache = nil
	tickersMu.Unlock()

	ctx := context.Background()
	symbols, err := GetPublicSymbols(ctx)
	if err != nil || len(symbols) != 1 || symbols[0] != "BTCUSDT" {
		t.Fatalf("only trading crypto perpetuals: %v %v", symbols, err)
	}
	tickers, err := GetPublicTickers(ctx)
	if err != nil || len(tickers) != 1 || tickers[0].Symbol != "BTCUSDT" {
		t.Fatalf("gainers must come from perpetuals only: %+v %v", tickers, err)
	}
	ticker, err := GetPublicTicker(ctx, "BTCUSDT")
	if err != nil || ticker.MarkPrice != 78010 || ticker.FundingRate != 0.0001 || ticker.NextFundingTime == 0 {
		t.Fatalf("ticker must carry the real mark price and funding: %+v %v", ticker, err)
	}
	klines, err := GetPublicKlines(ctx, "BTCUSDT", "1h", 1)
	if err != nil || len(klines) != 1 || klines[0].Close != 1.5 {
		t.Fatalf("candles must come from the futures API: %+v %v", klines, err)
	}
}
