package kernel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"nofx/market"
	"nofx/mcp"
	"nofx/store"
)

type failingDecisionClient struct{ mcp.AIClient }

func (failingDecisionClient) CallWithMessages(string, string) (string, error) {
	time.Sleep(2 * time.Millisecond)
	return "", errors.New("upstream request deadline exceeded")
}

func TestFailedAIRequestRetainsTimingAndPrompts(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.Indicators.EnableOI = false
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "static"}
	ctx := &Context{Account: AccountInfo{TotalEquity: 1000}, MarketDataMap: map[string]*market.Data{"BTCUSDT": {Symbol: "BTCUSDT"}}}
	d, err := GetFullDecisionWithStrategy(ctx, failingDecisionClient{}, NewStrategyEngine(&cfg), "")
	if err == nil || d == nil || d.AIRequestDurationMs < 1 || d.SystemPrompt == "" || d.UserPrompt == "" || len(d.Decisions) != 0 {
		t.Fatalf("failed request lost diagnostics or produced actions: %+v %v", d, err)
	}
}

func TestCompactSeriesPreservesCandlesLatestIndicatorsAndSource(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.Indicators.EnableEMA = true
	data := &market.TimeframeSeriesData{ATR14: 2.5}
	for i := 0; i < 30; i++ {
		data.Klines = append(data.Klines, market.KlineBar{Time: time.Date(2026, 11, 23, i/2, i%2*30, 0, 0, time.UTC).UnixMilli(), Close: float64(i + 1)})
		data.EMA20Values = append(data.EMA20Values, float64(100+i))
	}
	var sb strings.Builder
	NewStrategyEngine(&cfg).formatTimeframeSeriesData(&sb, data, cfg.Indicators)
	text := sb.String()
	if strings.Count(text, "11-23 ") != 30 || !strings.Contains(text, "129.000") || strings.Contains(text, "100.000") {
		t.Fatalf("incorrect candle/indicator compression: %s", text)
	}
	if len(data.EMA20Values) != 30 || data.EMA20Values[0] != 100 {
		t.Fatal("prompt compression mutated analysis data")
	}
}

func TestPoolCandidatesExcludeProhibitedAssetsBeforeAnalysis(t *testing.T) {
	old, details := CoinPoolResolver, CoinPoolDetailResolver
	defer func() { CoinPoolResolver, CoinPoolDetailResolver = old, details }()
	CoinPoolResolver = func(string) ([]string, error) {
		return []string{"CLUSDT", "SQQQUSDT", "SOXSUSDT", "BTCUSDT", "ETHUSDT"}, nil
	}
	CoinPoolDetailResolver = func(string) ([]store.CoinPoolCoin, error) {
		return []store.CoinPoolCoin{{Symbol: "CLUSDT"}, {Symbol: "SQQQUSDT"}, {Symbol: "SOXSUSDT"}, {Symbol: "BTCUSDT"}, {Symbol: "ETHUSDT"}}, nil
	}
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "coin_pool", CoinPoolID: "p", AssetScope: "crypto", AllowedSymbols: []string{"CLUSDT", "SQQQUSDT", "SOXSUSDT", "BTCUSDT"}}
	e := NewStrategyEngine(&cfg, "binance_paper")
	coins, err := e.GetCandidateCoins()
	if err != nil || len(coins) != 1 || coins[0].Symbol != "BTCUSDT" {
		t.Fatalf("prohibited candidates sent for analysis: %+v %v", coins, err)
	}
	for _, sym := range []string{"SQQQUSDT", "SOXSUSDT"} {
		if reason := e.OpenBlockReason(Decision{Symbol: sym, Action: "open_long"}, &Context{CandidateCoins: []CandidateCoin{{Symbol: sym}}}); reason != "coin pool asset scope: crypto only" {
			t.Fatalf("execution gate missed ETF %s: %s", sym, reason)
		}
	}
}

func TestEntryRestrictionIsVisibleBeforeModelDecision(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	ctx := &Context{Account: AccountInfo{TotalEquity: 1000}, CandidateCoins: []CandidateCoin{{Symbol: "HYPEUSDT"}}, OpeningBlocks: map[string]string{"HYPEUSDT": "re-entry cooldown until next hour"}, MarketDataMap: map[string]*market.Data{"HYPEUSDT": {Symbol: "HYPEUSDT"}}}
	text := NewStrategyEngine(&cfg).BuildUserPrompt(ctx)
	if !strings.Contains(text, "ENTRY BLOCKED this cycle: re-entry cooldown") || !strings.Contains(text, "existing position exits remain allowed") {
		t.Fatal("model cannot see entry preflight restrictions")
	}
}
