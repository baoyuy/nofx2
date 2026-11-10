package kernel

import (
	"strings"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

func TestPositionQuoteOverridesOnlyPriceAndUsesPositionSnapshot(t *testing.T) {
	ctx := &Context{Positions: []PositionInfo{{Symbol: "SOLUSDT", MarkPrice: 95.87, MarkObservedAt: time.Now().UnixMilli(), MarkPriceTime: time.Now().UnixMilli()}}, MarketDataMap: map[string]*market.Data{"SOLUSDT": {Symbol: "SOLUSDT", CurrentPrice: 96.48, IndicatorPrice: 96.48, CurrentEMA20: 94, IndicatorAsOf: time.Now(), IndicatorTimeframe: "1m", TimeframeData: map[string]*market.TimeframeSeriesData{"1m": {Timeframe: "1m", AsOf: time.Now(), Klines: []market.KlineBar{{Time: 1, Close: 96.48}}}}}}}
	original := ctx.MarketDataMap["SOLUSDT"]
	alignPositionQuotes(ctx, "binance_paper")
	d := ctx.MarketDataMap["SOLUSDT"]
	if d.CurrentPrice != 95.87 || d.Quote == nil || d.Quote.Price != 95.87 {
		t.Fatalf("position price mismatch: %+v", d)
	}
	if d.IndicatorPrice != 96.48 || d.CurrentEMA20 != 94 || d.TimeframeData["1m"].Klines[0].Close != 96.48 || original.CurrentPrice != 96.48 {
		t.Fatal("position mark mutated candle or indicator data")
	}
	cfg := store.GetDefaultStrategyConfig("en")
	prompt := NewStrategyEngine(&cfg).BuildUserPrompt(ctx)
	if !strings.Contains(prompt, "current_price = 95.8700") || !strings.Contains(prompt, "indicator_close = 96.4800") || !strings.Contains(prompt, "quote_source = account_position_mark") {
		t.Fatalf("price basis not explicit:\n%s", prompt)
	}
	if strings.Contains(prompt, "current_price = 96.4800") {
		t.Fatal("candle close mislabeled current price")
	}
}

func TestStalePositionStillManageableButOpeningGateFailsClosed(t *testing.T) {
	now := time.Now()
	ctx := &Context{Positions: []PositionInfo{{Symbol: "SOLUSDT", MarkPrice: 95.87, MarkObservedAt: now.Add(-time.Minute).UnixMilli(), MarkPriceTime: now.Add(-time.Minute).UnixMilli()}}, MarketDataMap: map[string]*market.Data{"SOLUSDT": {IndicatorPrice: 96, IndicatorAsOf: now.Add(-time.Hour), IndicatorTimeframe: "1m", IndicatorIssue: "stale candle timestamp"}}}
	alignPositionQuotes(ctx, "binance_paper")
	d := ctx.MarketDataMap["SOLUSDT"]
	if d.CurrentPrice != 95.87 || d.Quote.Fresh(now) || d.OpeningAllowed(now) {
		t.Fatalf("stale position quote misrepresented: %+v", d)
	}
	decisions := []Decision{{Symbol: "SOLUSDT", Action: "open_long"}, {Symbol: "SOLUSDT", Action: "close_long"}}
	blockUnsafeOpenings(decisions, ctx, "binance_paper", now)
	if decisions[0].Action != "wait" || decisions[1].Action != "close_long" {
		t.Fatalf("open/exit policy: %+v", decisions)
	}
}

func TestCandleFailureRetainsExistingPositionPrompt(t *testing.T) {
	ctx := &Context{Positions: []PositionInfo{{Symbol: "SOLUSDT", Side: "long", MarkPrice: 95.87, MarkObservedAt: time.Now().UnixMilli(), MarkPriceTime: time.Now().UnixMilli(), EntryPrice: 90, UnrealizedPnL: 5}}, MarketDataMap: map[string]*market.Data{}}
	alignPositionQuotes(ctx, "binance_paper")
	cfg := store.GetDefaultStrategyConfig("en")
	prompt := NewStrategyEngine(&cfg).BuildUserPrompt(ctx)
	if !strings.Contains(prompt, "Entry 90.0000 Current 95.8700") || !strings.Contains(prompt, "candles unavailable") {
		t.Fatalf("position disappeared without candles: %s", prompt)
	}
}
