package coinpool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// flatThenMove: a long range (about ±0.3% chop), then a sustained move of
// step per bar.
func flatThenMove(flat, move int, step float64) []float64 {
	out := []float64{}
	price := 100.0
	for i := 0; i < flat; i++ {
		out = append(out, price+0.3*math.Sin(float64(i)*1.7))
	}
	for i := 0; i < move; i++ {
		price += step
		out = append(out, price)
	}
	return out
}

func TestHornDetectsFreshBreakoutDirection(t *testing.T) {
	up := AnalyzeHorn(flatThenMove(100, 5, 0.8))
	if !up.Match || up.Direction != "long" || (up.Stage != "opening" && up.Stage != "starting") || up.Score < 60 {
		t.Fatalf("a fresh breakout up after a squeeze must be a long horn: %+v", up)
	}
	down := AnalyzeHorn(flatThenMove(100, 5, -0.8))
	if !down.Match || down.Direction != "short" || down.Score < 60 {
		t.Fatalf("a fresh breakdown after a squeeze must be a short horn: %+v", down)
	}
}

// The screen wants horns that are just forming: once a move has run for many
// bars it is no longer a candidate, and it ranks below a fresh one.
func TestHornRejectsHornsThatHaveAlreadyRun(t *testing.T) {
	fresh := AnalyzeHorn(flatThenMove(100, 5, 0.8))
	late := AnalyzeHorn(flatThenMove(100, 25, 0.8))
	if late.Match || late.Stage == "opening" || late.Stage == "starting" || late.Score >= fresh.Score {
		t.Fatalf("a horn 25 bars into its move must not match or outrank a fresh one: %+v vs %+v", late, fresh)
	}
}

func TestHornRejectsRangesAndSteadyTrends(t *testing.T) {
	flat := AnalyzeHorn(flatThenMove(120, 0, 0))
	if flat.Match || flat.Stage != "none" || flat.Score != 0 {
		t.Fatalf("a quiet range is not a horn: %+v", flat)
	}
	// A steady trend has wide but parallel bands: no squeeze, no horn.
	trend := make([]float64, 120)
	for i := range trend {
		trend[i] = 100 + float64(i)
	}
	if r := AnalyzeHorn(trend); r.Match {
		t.Fatalf("a steady trend without a squeeze is not a horn: %+v", r)
	}
	if r := AnalyzeHorn(flatThenMove(30, 0, 0)); r.Stage != "none" {
		t.Fatalf("too little history must not score: %+v", r)
	}
}

func TestHornClosingBandsScoreLower(t *testing.T) {
	series := flatThenMove(100, 5, 0.8)
	top := series[len(series)-1]
	for i := 0; i < 25; i++ { // the move stalls and the bands contract again
		series = append(series, top+0.05*math.Sin(float64(i)))
	}
	r := AnalyzeHorn(series)
	fresh := AnalyzeHorn(flatThenMove(100, 5, 0.8))
	if r.Stage == "opening" || r.Match || r.Score >= fresh.Score {
		t.Fatalf("a contracting horn must not rank like a fresh one: %+v vs %+v", r, fresh)
	}
}

func TestMatchedRunCountsConsecutiveMatchingCandles(t *testing.T) {
	series := flatThenMove(100, 5, 0.8)
	run := matchedRun(series)
	if run < 1 || run > 5 {
		t.Fatalf("a 5-bar breakout can only have matched on its own bars, got %d", run)
	}
	if matchedRun(series[:len(series)-run]) != 0 {
		t.Fatal("the candle before the run must not match")
	}
	if matchedRun(flatThenMove(120, 0, 0)) != 0 {
		t.Fatal("a quiet range never matches")
	}
}

func TestHornBoardScreensTopGainersOnly(t *testing.T) {
	closes := map[string][]float64{
		"UPUSDT":   flatThenMove(150, 5, 0.8),
		"FLATUSDT": flatThenMove(160, 0, 0),
	}
	klines := func(symbol string) string {
		rows := [][]any{}
		for i, c := range append(closes[symbol], closes[symbol][len(closes[symbol])-1]) {
			p := fmt.Sprint(c)
			rows = append(rows, []any{int64(i), p, p, p, p, "1"})
		}
		b, _ := json.Marshal(rows)
		return string(b)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			fmt.Fprint(w, `{"symbols":[`+
				`{"symbol":"UPUSDT","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"PERPETUAL"},`+
				`{"symbol":"FLATUSDT","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"PERPETUAL"},`+
				`{"symbol":"TSLAUSDT","quoteAsset":"USDT","marginAsset":"USDT","status":"TRADING","contractType":"TRADIFI_PERPETUAL"}]}`)
		case "/fapi/v1/ticker/24hr":
			fmt.Fprint(w, `[{"symbol":"FLATUSDT","lastPrice":"1","priceChangePercent":"3","quoteVolume":"10"},`+
				`{"symbol":"UPUSDT","lastPrice":"2","priceChangePercent":"12","quoteVolume":"20"},`+
				`{"symbol":"TSLAUSDT","lastPrice":"300","priceChangePercent":"40","quoteVolume":"30"}]`)
		case "/fapi/v1/klines":
			if r.URL.Query().Get("symbol") == "TSLAUSDT" {
				t.Error("non-crypto contracts are not screened")
			}
			fmt.Fprint(w, klines(r.URL.Query().Get("symbol")))
		default:
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	s := NewScreener()
	s.BaseURL = server.URL
	board, err := s.HornBoard(context.Background(), "1h", false)
	if err != nil {
		t.Fatal(err)
	}
	if board.Scanned != 2 || board.Matched != 1 || board.Rows[0].Symbol != "UPUSDT" || !board.Rows[0].Match {
		t.Fatalf("unexpected board %+v", board)
	}
	// Candle i opens at i ms in the fixture, so the run's first candle closes
	// one hour after its open.
	up := board.Rows[0]
	n := len(closes["UPUSDT"])
	if up.MatchedBars < 1 || up.MatchedSince == nil ||
		!up.MatchedSince.Equal(time.UnixMilli(int64(n-up.MatchedBars)).Add(time.Hour).UTC()) {
		t.Fatalf("matched run = %d since %v", up.MatchedBars, up.MatchedSince)
	}
	if board.Rows[1].MatchedSince != nil {
		t.Fatal("a non-matching row has no board time")
	}
	if _, err := s.HornBoard(context.Background(), "2h", false); err == nil {
		t.Fatal("unsupported interval must be refused")
	}
}
