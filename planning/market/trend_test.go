package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"nofx/planning/plugins"
	"strings"
	"testing"
	"time"
)

func series(n int, start, step float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = start + step*float64(i)
	}
	return out
}

func TestTrendMetricsKeepsOnlyUptrends(t *testing.T) {
	flat := series(99, 0, 0)
	if _, _, _, ok := trendMetrics(series(99, 100, 1), flat); ok {
		t.Fatal("zero volume history must not qualify")
	}
	volumes := series(99, 1000, 0)
	ret, slope, vol, ok := trendMetrics(series(99, 100, 1), volumes)
	if !ok || ret <= 0 || slope <= 0 || vol != 1 {
		t.Fatal("rising market rejected", ret, slope, vol, ok)
	}
	if _, _, _, ok := trendMetrics(series(99, 200, -1), volumes); ok {
		t.Fatal("falling market accepted")
	}
	if _, _, _, ok := trendMetrics(series(40, 100, 1), series(40, 1000, 0)); ok {
		t.Fatal("short history accepted")
	}
}

func TestRankTrendOrdersByCombinedPercentile(t *testing.T) {
	coins := rankTrend([]TrendCoin{
		{Pair: "AUSDT", Return7D: 0.01, EMASlope: 0.01, VolumeRatio: 1},
		{Pair: "BUSDT", Return7D: 0.30, EMASlope: 0.20, VolumeRatio: 3},
		{Pair: "CUSDT", Return7D: 0.10, EMASlope: 0.05, VolumeRatio: 2},
	})
	if coins[0].Pair != "BUSDT" || coins[0].Score != 100 || coins[2].Pair != "AUSDT" || coins[2].Score != 0 {
		t.Fatalf("unexpected ranking %+v", coins)
	}
}

func TestTrendBoardScansPublicCandlesOnly(t *testing.T) {
	klines := func(rising bool) string {
		rows := [][]any{}
		for i := 0; i < trendCandles; i++ {
			price := 100.0 + float64(i)
			if !rising {
				price = 300 - float64(i)
			}
			p := fmt.Sprint(price)
			rows = append(rows, []any{1700000000000 + int64(i)*14400000, p, p, p, p, "1000"})
		}
		b, _ := json.Marshal(rows)
		return string(b)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Payment") != "" {
			t.Error("credentials sent to public data")
		}
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			fmt.Fprint(w, `{"symbols":[`+
				`{"symbol":"UPUSDT","baseAsset":"UP","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"PERPETUAL"},`+
				`{"symbol":"DOWNUSDT","baseAsset":"DOWN","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"PERPETUAL"},`+
				`{"symbol":"TSLAUSDT","baseAsset":"TSLA","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"TRADIFI_PERPETUAL","underlyingType":"EQUITY"}]}`)
		case "/fapi/v1/ticker/24hr":
			fmt.Fprint(w, `[{"symbol":"UPUSDT","quoteVolume":"5000000","lastPrice":"199"},{"symbol":"DOWNUSDT","quoteVolume":"9000000","lastPrice":"201"},{"symbol":"TSLAUSDT","quoteVolume":"9999999","lastPrice":"300"}]`)
		case "/fapi/v1/klines":
			symbol := r.URL.Query().Get("symbol")
			if symbol == "TSLAUSDT" {
				t.Error("stock contracts are not part of the crypto trend board")
			}
			fmt.Fprint(w, klines(symbol == "UPUSDT"))
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	c := New()
	c.BinanceFutures = server.URL
	board, err := c.TrendBoard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if board.Scanned != 2 || len(board.Coins) != 1 || board.Coins[0].Pair != "UPUSDT" {
		t.Fatalf("unexpected board %+v", board)
	}
}

func TestPositioningPluginsUseFreeBinanceData(t *testing.T) {
	now := time.Now().UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/futures/data/globalLongShortAccountRatio":
			fmt.Fprintf(w, `[{"symbol":"BTCUSDT","longShortRatio":"2.7078","longAccount":"0.7303","timestamp":%d}]`, now)
		case "/futures/data/topLongShortPositionRatio":
			fmt.Fprintf(w, `[{"symbol":"BTCUSDT","longShortRatio":"1.7525","longAccount":"0.6367","timestamp":%d}]`, now)
		case "/futures/data/takerlongshortRatio":
			fmt.Fprintf(w, `[{"buySellRatio":"1.5","buyVol":"300","sellVol":"200","timestamp":%d},{"buySellRatio":"0.33","buyVol":"100","sellVol":"300","timestamp":%d}]`, now, now)
		case "/futures/data/openInterestHist":
			rows := []string{}
			for i := 0; i < 25; i++ {
				rows = append(rows, fmt.Sprintf(`{"symbol":"BTCUSDT","sumOpenInterest":"1","sumOpenInterestValue":"%d","timestamp":%d}`, 100+i, now))
			}
			fmt.Fprint(w, "["+strings.Join(rows, ",")+"]")
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	c := New()
	c.BinanceFutures = server.URL
	registry, err := plugins.Open("", plugins.Builtins(c)...)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{PluginLongShort: "account_ratio", PluginTakerFlow: "taker_ratio_24h", PluginOpenInterest: "oi_change_24h"}
	for id, key := range want {
		r, err := registry.Read(context.Background(), id, plugins.Request{Venue: "binance", Symbol: "BTCUSDT"})
		if err != nil {
			t.Fatal(id, err)
		}
		if r.Mode != "public" {
			t.Fatal(id, "not public data")
		}
		found := false
		for _, m := range r.Metrics {
			if m.Key == key {
				found = true
				if id == PluginTakerFlow && m.Value != "0.800000" {
					t.Fatal("24h taker ratio", m.Value)
				}
				if id == PluginOpenInterest && m.Value != "0.240000" {
					t.Fatal("24h OI change", m.Value)
				}
			}
		}
		if !found {
			t.Fatal(id, "missing", key)
		}
	}
	if _, err := registry.Read(context.Background(), PluginLongShort, plugins.Request{Venue: "okx", Symbol: "BTC-USDT-SWAP"}); err == nil {
		t.Fatal("Binance-only plugin served another venue")
	}
}
