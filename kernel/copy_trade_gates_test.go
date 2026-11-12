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

func TestCopyTradeSourceOptionsAreOffByDefault(t *testing.T) {
	prev := CopyTradeResolver
	defer func() { CopyTradeResolver = prev }()
	coin := func(symbol string, conf float64, openers int, volume float64, tradfi bool) CopyTradeCoin {
		return CopyTradeCoin{Symbol: symbol, Direction: "long", Confidence: conf, Volume24h: volume, TradFi: tradfi,
			Long:  CopyTradeSide{Traders: 3, Openers: openers},
			Fresh: CopyTradeFresh{Direction: "neutral"}}
	}
	view := CopyTradeView{Fresh: 5, Total: 5, Coins: []CopyTradeCoin{
		coin("THINUSDT", 80, 0, 3e6, false),
		coin("TSLAUSDT", 70, 0, 500e6, true),
		coin("SOLUSDT", 60, 2, 2000e6, false),
		coin("DEADUSDT", 50, 0, 1e6, false),
	}}
	// SOL: holdings lean long, but the last day's calls are short.
	view.Coins[2].Fresh = CopyTradeFresh{Direction: "short", Score: 55, ShortCalls: 2}
	CopyTradeResolver = func() CopyTradeView { return view }
	symbols := func(src store.CoinSourceConfig) string {
		src.SourceType, src.CopyTradeMinConfidence, src.CopyTradeLimit = "copy_trade", 40, 10
		var out []string
		for _, c := range copyTradeCandidates(src) {
			out = append(out, c.Symbol)
		}
		return strings.Join(out, ",")
	}
	// Nothing set: exactly the old behaviour, by confidence.
	if got := symbols(store.CoinSourceConfig{}); got != "THINUSDT,TSLAUSDT,SOLUSDT,DEADUSDT" {
		t.Fatalf("defaults changed the source: %s", got)
	}
	if got := symbols(store.CoinSourceConfig{CopyTradeCryptoOnly: true}); got != "THINUSDT,SOLUSDT,DEADUSDT" {
		t.Fatalf("crypto only: %s", got)
	}
	// The volume bar used to block the open only; now the coin takes no place.
	if got := symbols(store.CoinSourceConfig{CopyTradeMinVolumeM: 2}); got != "THINUSDT,TSLAUSDT,SOLUSDT" {
		t.Fatalf("volume filter: %s", got)
	}
	// Quality: liquid and freshly called first, thin and stale last.
	if got := symbols(store.CoinSourceConfig{CopyTradeRank: "quality"}); !strings.HasPrefix(got, "SOLUSDT,TSLAUSDT") {
		t.Fatalf("quality ranking: %s", got)
	}
	if got := symbols(store.CoinSourceConfig{CopyTradeSignal: "fresh"}); got != "SOLUSDT" {
		t.Fatalf("fresh signal: %s", got)
	}
}

func TestFreshSignalDrivesTheFollowRule(t *testing.T) {
	prev := CopyTradeResolver
	defer func() { CopyTradeResolver = prev }()
	CopyTradeResolver = func() CopyTradeView {
		return CopyTradeView{Fresh: 5, Total: 5, Coins: []CopyTradeCoin{{Symbol: "SOLUSDT", Direction: "long", Confidence: 60,
			Long: CopyTradeSide{Traders: 4}, Short: CopyTradeSide{Traders: 2, Openers: 2}, TradFi: false,
			Fresh: CopyTradeFresh{Direction: "short", Score: 55, ShortCalls: 2, LongExits: 1}}}}
	}
	run := func(signal, action string) string {
		cfg := store.GetDefaultStrategyConfig("en")
		cfg.CoinSource = store.CoinSourceConfig{SourceType: "copy_trade", CopyTradeFollow: "follow", CopyTradeSignal: signal}
		engine := NewStrategyEngine(&cfg)
		ctx := &Context{}
		enrichCopyTradeData(ctx, engine)
		return engine.OpenBlockReason(Decision{Symbol: "SOLUSDT", Action: action}, ctx)
	}
	if r := run("", "open_long"); r != "" {
		t.Fatalf("holdings lean long: %q", r)
	}
	if r := run("fresh", "open_long"); !strings.Contains(r, "lean short") {
		t.Fatalf("fresh calls are short, a long must be refused: %q", r)
	}
	if r := run("fresh", "open_short"); r != "" {
		t.Fatalf("fresh calls are short: %q", r)
	}
	block := &CopyTradeCoin{Symbol: "SOLUSDT", Direction: "long", Confidence: 60, Long: CopyTradeSide{Traders: 4},
		Fresh: CopyTradeFresh{Direction: "short", Score: 55, ShortCalls: 2, LongExits: 1, Burst: 2}}
	whole := &CopyTradeView{Fresh: 5, Total: 5, FreshSignal: true}
	if got := formatCopyTrade(block, whole, time.Now(), 0, ""); !strings.Contains(got, "Last 24h: 0 opened or added long, 2 short; 1 left longs, 0 left shorts → fresh calls lean SHORT, 55/100, 2 of them within 6h") {
		t.Fatalf("fresh line missing:\n%s", got)
	}
	// A strategy on the default signal reads the prompt it always did.
	whole.FreshSignal = false
	if got := formatCopyTrade(block, whole, time.Now(), 0, ""); strings.Contains(got, "Last 24h") {
		t.Fatalf("default prompt changed:\n%s", got)
	}
	def := store.GetDefaultStrategyConfig("en")
	def.CoinSource = store.CoinSourceConfig{SourceType: "copy_trade"}
	if strings.Contains(NewStrategyEngine(&def).BuildSystemPrompt(1000, ""), "Last 24h") {
		t.Fatal("default system prompt changed")
	}
	def.CoinSource.CopyTradeSignal = "fresh"
	if p := NewStrategyEngine(&def).BuildSystemPrompt(1000, ""); !strings.Contains(p, "Last 24h") || !strings.Contains(p, "fresh-call direction") {
		t.Fatal("fresh signal not explained in the system prompt")
	}
}

func TestCryptoOnlyRefusesStockContracts(t *testing.T) {
	prev := CopyTradeResolver
	defer func() { CopyTradeResolver = prev }()
	CopyTradeResolver = func() CopyTradeView {
		return CopyTradeView{Fresh: 5, Total: 5, Coins: []CopyTradeCoin{{Symbol: "TSLAUSDT", Direction: "long", Confidence: 60,
			Long: CopyTradeSide{Traders: 3}, TradFi: true}}}
	}
	for only, want := range map[bool]bool{false: false, true: true} {
		cfg := store.GetDefaultStrategyConfig("en")
		cfg.CoinSource = store.CoinSourceConfig{SourceType: "copy_trade", CopyTradeFollow: "follow", CopyTradeCryptoOnly: only}
		engine := NewStrategyEngine(&cfg)
		ctx := &Context{}
		enrichCopyTradeData(ctx, engine)
		r := engine.OpenBlockReason(Decision{Symbol: "TSLAUSDT", Action: "open_long"}, ctx)
		if strings.Contains(r, "stock or commodity") != want {
			t.Fatalf("crypto only %v: %q", only, r)
		}
	}
}

func TestExitAlertSurvivesAStaleTraderElsewhere(t *testing.T) {
	now := time.Now()
	coin := &CopyTradeCoin{Symbol: "SOLUSDT", Direction: "long", Confidence: 50, Long: CopyTradeSide{Traders: 1},
		Recent: []CopyTradeAction{{Trader: "a", Side: "long", Action: "close"}, {Trader: "b", Side: "long", Action: "close"}}}
	// 45 of 51 fresh; the stale six hold BTC, not SOL.
	cov := &CopyTradeView{Fresh: 45, Total: 51, StaleCoins: map[string]int{copyTradeKey("BTCUSDT"): 6}}
	if !cov.completeFor("SOLUSDT") || cov.completeFor("BTCUSDT") {
		t.Fatal("per-coin completeness")
	}
	got := formatCopyTradeFor(coin, cov, cov.completeFor("SOLUSDT"), now, 100, "long")
	if !strings.Contains(got, "closed or reduced recently") || strings.Contains(got, "⚠️ Lead traders: only") {
		t.Fatalf("exit alert lost to an unrelated stale trader:\n%s", got)
	}
	// On the coin they do hold, the old caution stands.
	btc := *coin
	btc.Symbol = "BTCUSDT"
	got = formatCopyTradeFor(&btc, cov, cov.completeFor("BTCUSDT"), now, 100, "long")
	if strings.Contains(got, "closed or reduced recently") || !strings.Contains(got, "only 45 of 51") {
		t.Fatalf("incomplete coin must not alert:\n%s", got)
	}
	// A coin nobody fresh holds, held by us: with the stale ones elsewhere it
	// is known that no one holds it.
	if got := formatCopyTradeFor(nil, cov, cov.completeFor("XRPUSDT"), now, 0, "long"); !strings.Contains(got, "none of the followed lead traders") {
		t.Fatalf("known-empty coin: %q", got)
	}
	// Without the per-coin map nothing is assumed.
	if (&CopyTradeView{Fresh: 45, Total: 51}).completeFor("SOLUSDT") {
		t.Fatal("unknown staleness treated as complete")
	}
}
