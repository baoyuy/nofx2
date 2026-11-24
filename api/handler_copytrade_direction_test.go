package api

import (
	"nofx/store"
	"nofx/trader/binance"
	"testing"
)

func TestDirectionHistorySeparatesSidesAndDoesNotUseBTCExcess(t *testing.T) {
	pos, neg := .01, -.01
	ai := []store.CopyTradeScoreOutcome{
		{Symbol: "X", Bias: "short", AnalyzedAt: 1000, Done4h: true, Ret4h: &pos},
		{Symbol: "X", Bias: "short", AnalyzedAt: 2000, Done4h: true, Ret4h: &pos},
		{Symbol: "X", Bias: "long", AnalyzedAt: 1000, Done4h: true, Ret4h: &neg},
		{Symbol: "X", Bias: "short", AnalyzedAt: 99999999, Done4h: true, Ret4h: &pos},
	}
	signals := []store.CopyTradeSignal{{Direction: "short", Hour: 1, Done4h: true, Ret4h: &neg, Bench4h: &neg}, {Direction: "long", Hour: 1, Done4h: true, Ret4h: &neg}}
	a, b := directionHistory(ai, signals, "short", 20000000)
	if a.Samples != 1 || a.Hits != 1 || b.Samples != 1 || b.Hits != 1 {
		t.Fatalf("wrong evidence: %+v %+v", a, b)
	}
}
func TestDirectionTrendRejectsOpenCandlesGapsAndStaleData(t *testing.T) {
	bars := []binance.MarketKline{}
	for i := 0; i < 17; i++ {
		p := 100 - float64(i)
		bars = append(bars, binance.MarketKline{Time: int64(i) * 900, Close: p, High: p + 1, Low: p - 1})
	}
	now := int64(17 * 900)
	_, _, invalid, ok := directionTrend(bars, "short", now)
	if !ok || invalid != 100 {
		t.Fatalf("short confirmation: %v %v", invalid, ok)
	}
	if _, _, _, ok := directionTrend(bars, "long", now); ok {
		t.Fatal("long against trend")
	}
	if _, _, _, ok := directionTrend(bars, "short", now-1); ok {
		t.Fatal("used open candle")
	}
	if _, _, _, ok := directionTrend(bars, "short", now+1800); ok {
		t.Fatal("used stale data")
	}
	bars[8].Time++
	if _, _, _, ok := directionTrend(bars, "short", now); ok {
		t.Fatal("used gaps")
	}
}

func TestDirectionObservationTracksShortReboundAndRejectsMissingBars(t *testing.T) {
	bars := []binance.MarketKline{}
	for i := 0; i < 16; i++ {
		bars = append(bars, binance.MarketKline{Time: int64(i) * 900, High: 103, Low: 95, Close: 98})
	}
	o, ok := observeDirection(directionPick{Symbol: "X", Direction: "short", EntryPrice: 100}, 0, bars)
	if !ok || o.Return < .0199 || o.MaxAdverse < .0299 || o.NetReturn < .0179 {
		t.Fatalf("wrong path: %+v %v", o, ok)
	}
	if _, ok := observeDirection(directionPick{Direction: "short", EntryPrice: 100}, 0, bars[1:]); ok {
		t.Fatal("missing first bar accepted")
	}
	if _, ok := observeDirection(directionPick{Direction: "short", EntryPrice: 100}, 0, bars[:15]); ok {
		t.Fatal("missing last bar accepted")
	}
}
