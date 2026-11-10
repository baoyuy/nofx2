package marketboard

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// candles builds hourly candles closing at the given prices; buyShare is the
// taker buy share of each candle's 1000 USDT volume.
func candles(closes []float64, buyShare float64) []Candle {
	out := make([]Candle, len(closes))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		out[i] = Candle{OpenTime: start.Add(time.Duration(i) * time.Hour).UnixMilli(), Open: c, High: c * 1.001, Low: c * 0.999,
			Close: c, QuoteVolume: 1000, TakerBuyQuote: 1000 * buyShare, Trades: 10}
	}
	return out
}

func ramp(n int, from, step float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = from + float64(i)*step
	}
	return out
}

func TestFactorsReadUptrendAsBullish(t *testing.T) {
	f := Factors(candles(ramp(60, 100, 0.5), 0.6), 0.05, true)
	for _, k := range []string{"trend", "momentum", "flow", "oi"} {
		if f[k] != bullish {
			t.Fatalf("%s = %s, want bullish (%v)", k, f[k], f)
		}
	}
	bias, score, bull, _, _ := Score(f)
	if bias != bullish || score != 100 || bull != 4 {
		t.Fatalf("Score = %s %.0f %d", bias, score, bull)
	}
}

func TestFactorsReadDowntrendAsBearishAndFlatAsNeutral(t *testing.T) {
	if bias, _, _, _, _ := Score(Factors(candles(ramp(60, 200, -0.5), 0.4), 0.05, true)); bias != bearish {
		t.Fatalf("downtrend bias = %s", bias)
	}
	// One bullish factor is not enough for a bias.
	f := Factors(candles(ramp(60, 100, 0), 0.6), 0, false)
	if bias, _, bull, _, _ := Score(f); bias != neutral || bull != 1 {
		t.Fatalf("flat bias = %s bull=%d (%v)", bias, bull, f)
	}
}

func TestHeatmapDropsLiquidationsAlreadyHit(t *testing.T) {
	// Price fell 30% and recovered: every long opened before the dip at 10x or
	// more has been liquidated, so only longs opened after it remain.
	closes := append(ramp(24, 100, 0), ramp(24, 70, 0)...)
	closes = append(closes, ramp(24, 100, 0)...)
	cs := candles(closes, 0.5)
	cs[24].Low = 69
	d := BuildHeatmap(cs, 0, time.UnixMilli(cs[len(cs)-1].OpenTime))
	var longLiq, longCost float64
	for _, b := range d.Bins {
		longLiq += b.LongLiq
		longCost += b.LongCost
	}
	if d.MarkPrice != 100 || longCost == 0 || longLiq == 0 {
		t.Fatalf("mark=%v cost=%v liq=%v", d.MarkPrice, longCost, longLiq)
	}
	// Surviving 10x longs come from the last 24 candles only: liquidation at
	// ~90.5 must not include the earlier, already-liquidated entries.
	fresh := 0.0
	for _, k := range cs[48:] {
		age := time.UnixMilli(cs[len(cs)-1].OpenTime).Sub(time.UnixMilli(k.OpenTime)).Hours()
		fresh += k.TakerBuyQuote * math.Pow(0.5, age/heatmapHalfLife)
	}
	if longLiq > fresh*1.0001 {
		t.Fatalf("long liquidations %v exceed surviving entries %v", longLiq, fresh)
	}
}

func TestHeatmapScalesToOpenInterest(t *testing.T) {
	cs := candles(ramp(48, 100, 0), 0.5)
	d := BuildHeatmap(cs, 5000, time.UnixMilli(cs[len(cs)-1].OpenTime))
	var cost float64
	for _, b := range d.Bins {
		cost += b.LongCost + b.ShortCost
	}
	if !d.ScaledToOI || cost < 9999 || cost > 10001 {
		t.Fatalf("scaled=%v cost=%v, want 10000", d.ScaledToOI, cost)
	}
}

// fakeBinance serves exchangeInfo, 24h tickers, klines and open interest for
// an uptrending BTC and a downtrending ETH.
func fakeBinance(t *testing.T) *httptest.Server {
	t.Helper()
	trend := map[string]float64{"BTCUSDT": 0.5, "ETHUSDT": -0.5}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sym := r.URL.Query().Get("symbol")
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			var syms []map[string]string
			for _, s := range []string{"BTCUSDT", "ETHUSDT", "TSLAUSDT"} {
				u := "COIN"
				if s == "TSLAUSDT" {
					u = "EQUITY"
				}
				syms = append(syms, map[string]string{"symbol": s, "quoteAsset": "USDT", "marginAsset": "USDT",
					"status": "TRADING", "contractType": "PERPETUAL", "underlyingType": u})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"symbols": syms})
		case "/fapi/v1/ticker/24hr":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"symbol": "BTCUSDT", "lastPrice": "100", "priceChangePercent": "5", "quoteVolume": "9000"},
				{"symbol": "ETHUSDT", "lastPrice": "100", "priceChangePercent": "-5", "quoteVolume": "8000"},
				{"symbol": "TSLAUSDT", "lastPrice": "100", "priceChangePercent": "1", "quoteVolume": "99999"},
			})
		case "/fapi/v1/klines":
			if _, listed := trend[sym]; !listed {
				http.Error(w, `{"code":-1121,"msg":"Invalid symbol."}`, http.StatusBadRequest)
				return
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			share := 0.6
			if trend[sym] < 0 {
				share = 0.4
			}
			var rows [][]any
			for _, k := range candles(ramp(limit, 100, trend[sym]), share) {
				rows = append(rows, []any{k.OpenTime, fmtNum(k.Open), fmtNum(k.High), fmtNum(k.Low), fmtNum(k.Close), "1",
					k.OpenTime + 3599999, fmtNum(k.QuoteVolume), k.Trades, "1", fmtNum(k.TakerBuyQuote), "0"})
			}
			_ = json.NewEncoder(w).Encode(rows)
		case "/futures/data/openInterestHist":
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			var rows []map[string]string
			for i := 0; i < limit; i++ {
				rows = append(rows, map[string]string{"sumOpenInterestValue": fmt.Sprint(1000 + i*10)})
			}
			_ = json.NewEncoder(w).Encode(rows)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestBoardsFromBinance(t *testing.T) {
	srv := fakeBinance(t)
	defer srv.Close()
	c := New()
	c.BaseURL = srv.URL
	ctx := context.Background()

	flow, err := c.Flow(ctx, "1h", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Data.Inflow) != 1 || flow.Data.Inflow[0].Symbol != "BTC" || len(flow.Data.Outflow) != 1 || flow.Data.Outflow[0].Symbol != "ETH" {
		t.Fatalf("flow = %+v (stock contracts must be excluded)", flow.Data)
	}

	board, err := c.Direction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Items) != 2 || board.Items[0].Rank != 1 {
		t.Fatalf("board = %+v", board.Items)
	}
	bias := map[string]string{}
	for _, it := range board.Items {
		bias[it.Symbol] = it.Bias
	}
	if bias["BTC"] != bullish || bias["ETH"] != bearish {
		t.Fatalf("bias = %v", bias)
	}

	hm, err := c.Heatmap(ctx, "btc")
	if err != nil {
		t.Fatal(err)
	}
	if !hm.Data.Market.IsActive || hm.Data.Market.Symbol != "BTC" || len(hm.Data.Bins) != heatmapBins || !hm.Data.ScaledToOI {
		t.Fatalf("heatmap = %+v", hm.Data.Market)
	}
	un, err := c.Heatmap(ctx, "xyz:SP500")
	if err != nil || un.Data.Cost.State != "unsupported" || len(un.Data.Bins) != 0 {
		t.Fatalf("unsupported heatmap = %+v, %v", un.Data, err)
	}
}

// The dashboard asks for the trader's own coins; only those are scored, in
// any spelling, duplicates collapse and unlisted markets are left out.
func TestDirectionForScoresOnlyTheGivenMarkets(t *testing.T) {
	srv := fakeBinance(t)
	defer srv.Close()
	c := New()
	c.BaseURL = srv.URL

	board := c.DirectionFor(context.Background(), []string{"eth", "ETHUSDT", "NOPEUSDT"})
	if len(board.Items) != 1 || board.Items[0].Symbol != "ETH" || board.Items[0].Bias != bearish || board.Items[0].Rank != 1 {
		t.Fatalf("board = %+v", board.Items)
	}
	if empty := c.DirectionFor(context.Background(), nil); empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty board must be an empty list, got %#v", empty.Items)
	}
}
