package kernel

import (
	"strings"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

// gateCoin leans long on SNDK with two fresh calls.
var gateCoin = CopyTradeCoin{Symbol: "SNDKUSDT", Direction: "long", Confidence: 55,
	Long: CopyTradeSide{Traders: 4, Value: 4000, AvgEntry: 1100, Fresh: 3, Openers: 2}}

func gateEngine(t *testing.T, view CopyTradeView, mutate func(*store.CoinSourceConfig)) (*StrategyEngine, *Context) {
	t.Helper()
	withCopyTradeView(t, view)
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "copy_trade"}
	if mutate != nil {
		mutate(&cfg.CoinSource)
	}
	cfg.Indicators.Klines.PrimaryTimeframe = "1h"
	engine := NewStrategyEngine(&cfg)
	engine.SetExchange("binance_paper")
	ctx := &Context{MarketDataMap: map[string]*market.Data{"SNDKUSDT": {CurrentPrice: 1100}}}
	enrichCopyTradeData(ctx, engine)
	return engine, ctx
}

func openLong(e *StrategyEngine, ctx *Context) string {
	return e.OpenBlockReason(Decision{Symbol: "SNDKUSDT", Action: "open_long"}, ctx)
}

func TestCandidatesNeedFreshOpeners(t *testing.T) {
	stale := gateCoin
	stale.Symbol, stale.Long.Openers = "ETHUSDT", 0
	withCopyTradeView(t, CopyTradeView{Coins: []CopyTradeCoin{gateCoin, stale}, Fresh: 1, Total: 1})
	src := store.CoinSourceConfig{SourceType: "copy_trade"}
	if got := symbols(copyTradeCandidates(src)); len(got) != 2 {
		t.Fatalf("off by default: %v", got)
	}
	src.CopyTradeMinOpeners = 2
	if got := symbols(copyTradeCandidates(src)); strings.Join(got, ",") != "SNDKUSDT" {
		t.Fatalf("2 fresh openers: %v", got)
	}
}

func TestOpenGateOpeners(t *testing.T) {
	view := CopyTradeView{Coins: []CopyTradeCoin{gateCoin}, Fresh: 1, Total: 1}
	e, ctx := gateEngine(t, view, func(s *store.CoinSourceConfig) { s.CopyTradeMinOpeners = 3 })
	if r := openLong(e, ctx); !strings.Contains(r, "only 2 lead traders opened") {
		t.Fatalf("3 needed, 2 opened: %q", r)
	}
	e.config.CoinSource.CopyTradeMinOpeners = 2
	if r := openLong(e, ctx); r != "" {
		t.Fatalf("2 needed: %q", r)
	}
}

func TestOpenGateHitRate(t *testing.T) {
	rates := []CopyTradeHitRate{{Band: "mid", Direction: "long", Samples: 40, Rate: 0.44}}
	view := CopyTradeView{Coins: []CopyTradeCoin{gateCoin}, Fresh: 1, Total: 1, HitRates: rates}
	e, ctx := gateEngine(t, view, func(s *store.CoinSourceConfig) { s.CopyTradeMinHitRate = 55 })
	if r := openLong(e, ctx); !strings.Contains(r, "right 44% of 40 times") {
		t.Fatalf("44%% < 55%%: %q", r)
	}
	// Fading a 44% signal wins 56% of the time.
	e.config.CoinSource.CopyTradeFollow = "fade"
	if r := e.OpenBlockReason(Decision{Symbol: "SNDKUSDT", Action: "open_short"}, ctx); r != "" {
		t.Fatalf("fade a losing signal: %q", r)
	}
	// Too few samples to trust anything.
	e.config.CoinSource.CopyTradeFollow = "follow"
	ctx.CopyTradeCoverage.HitRates[0].Samples = 10
	if r := openLong(e, ctx); !strings.Contains(r, "only 10 past mid-confidence long signals") {
		t.Fatalf("too few samples: %q", r)
	}
	ctx.CopyTradeCoverage.HitRates[0].Samples, ctx.CopyTradeCoverage.HitRates[0].Rate = 40, 0.6
	if r := openLong(e, ctx); r != "" {
		t.Fatalf("60%% passes: %q", r)
	}
	block := formatCopyTrade(copyTradeFor(ctx, "SNDKUSDT"), ctx.CopyTradeCoverage, time.Now(), 1100, "")
	if !strings.Contains(block, "Track record: past mid-confidence long signals moved the called way 60% of 40 times after 24h") ||
		!strings.Contains(block, "2 fresh calls") {
		t.Fatalf("prompt block:\n%s", block)
	}
	if sp := e.BuildSystemPrompt(1000, ""); !strings.Contains(sp, "must be at least 55% over 30+ signals") {
		t.Fatal("system prompt states the hit-rate rule")
	}
}

func TestOpenGateTrendAndVolume(t *testing.T) {
	view := CopyTradeView{Coins: []CopyTradeCoin{gateCoin}, Fresh: 1, Total: 1}
	e, ctx := gateEngine(t, view, func(s *store.CoinSourceConfig) { s.CopyTradeTrendFilter = true })
	// 4h EMA20 under EMA50: a downtrend. 1h bars: 24 × 10 × 1100 = 0.26M USDT.
	bars := make([]market.KlineBar, 30)
	for i := range bars {
		bars[i] = market.KlineBar{Close: 1100, Volume: 10}
	}
	ctx.MarketDataMap["SNDKUSDT"].TimeframeData = map[string]*market.TimeframeSeriesData{
		"4h": {EMA20Values: []float64{1000}, EMA50Values: []float64{1100}},
		"1h": {Klines: bars},
	}
	if r := openLong(e, ctx); !strings.Contains(r, "4h trend on SNDKUSDT is down") {
		t.Fatalf("long in a downtrend: %q", r)
	}
	ctx.MarketDataMap["SNDKUSDT"].TimeframeData["4h"].EMA20Values = []float64{1200}
	if r := openLong(e, ctx); r != "" {
		t.Fatalf("long in an uptrend: %q", r)
	}
	e.config.CoinSource.CopyTradeMinVolumeM = 1
	if r := openLong(e, ctx); !strings.Contains(r, "traded 0.3M USDT in 24h") {
		t.Fatalf("thin coin: %q", r)
	}
	if v, ok := volume24h(ctx.MarketDataMap["SNDKUSDT"]); !ok || v != 24*10*1100 {
		t.Fatalf("24h volume = %v", v)
	}
}
