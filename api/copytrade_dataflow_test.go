package api

import (
	"strings"
	"testing"
	"time"

	"nofx/copytrade"
	"nofx/kernel"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
)

type copyTradeCaptureAI struct {
	mcp.AIClient
	system, user string
}

func (c *copyTradeCaptureAI) CallWithMessages(system, user string) (string, error) {
	c.system, c.user = system, user
	return `[{"symbol":"ETHUSDT","action":"hold","reasoning":"fixture"}]`, nil
}

// Exercise the actual model-call exit, not just an intermediate JSON field.
func TestCopyTradeDataReachesFinalAIMessages(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	h := copytrade.Holder{Nickname: "fixture", Position: copytrade.Position{
		Symbol: "ETHUSDT", Side: "short", EntryPrice: 100, FirstEntry: 95, EntryValue: 100, Source: "public",
		Health: copytrade.Health{ReturnPct: -9.4, PctOfMargin: true, Priced: true, HistoricalEntryWarning: true,
			FirstEntryReturnPct: -115, FirstEntryPctOfMargin: true, FirstEntryThresholdPct: 50},
	}, PositionsUpdatedAt: now, FillsUpdatedAt: now.Add(-time.Minute), ProfileUpdatedAt: now.Add(-time.Hour)}
	holders := copyTradeHolders([]copytrade.Holder{h}, nil)
	previous := kernel.CopyTradeResolver
	kernel.CopyTradeResolver = func() kernel.CopyTradeView {
		return kernel.CopyTradeView{Fresh: 1, Total: 10, Reason: "fixture history incomplete", Coins: []kernel.CopyTradeCoin{
			{Symbol: "ETHUSDT", Direction: "short", Confidence: 80, Short: kernel.CopyTradeSide{Traders: 1}, Holders: holders},
		}}
	}
	t.Cleanup(func() { kernel.CopyTradeResolver = previous })
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "static", StaticCoins: []string{"ETHUSDT"}, CopyTradeContext: true}
	engine := kernel.NewStrategyEngine(&cfg)
	ctx := &kernel.Context{Account: kernel.AccountInfo{TotalEquity: 1000, AvailableBalance: 1000},
		CandidateCoins: []kernel.CandidateCoin{{Symbol: "ETHUSDT"}},
		MarketDataMap:  map[string]*market.Data{"ETHUSDT": {Symbol: "ETHUSDT", CurrentPrice: 101}},
		OITopDataMap:   map[string]*kernel.OITopData{"ETHUSDT": {}},
	}
	client := &copyTradeCaptureAI{}
	if _, err := kernel.GetFullDecisionWithStrategy(ctx, client, engine, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"only 1 of 10 followed traders have fresh data", "fixture history incomplete",
		"-9.4% of margin", "Historical entry deviation", "-115.0% (reference scaled by current leverage)",
		"adverse threshold 50.0%", "Not current PnL", "Data timestamps (UTC)", now.Format(time.RFC3339), "public position"} {
		if !strings.Contains(client.user, want) {
			t.Fatalf("AI message missing %q:\n%s", want, client.user)
		}
	}
	if strings.Contains(client.user, "-9.4% price") || holders[0].Trapped {
		t.Fatal("reference warning changed current-loss semantics")
	}
	if !strings.Contains(client.system, "Historical first-entry prices never set this flag") {
		t.Fatal("AI system explanation diverged from health rules")
	}
}

func TestCopyTradeViewReportsHistoryMissingAfterLegacyLoad(t *testing.T) {
	s, _ := copyTradeServer(t)
	f := store.CopyTradeFollow{PortfolioID: "legacy", FillsUntil: time.Now().UnixMilli(), ProfileAt: time.Now().UnixMilli()}
	if err := s.store.CopyTrade().SaveFollow(&f); err != nil {
		t.Fatal(err)
	}
	s.copyTrade.Load()
	view := copyTradeView(s.copyTrade, time.Now())
	if view.Fresh != 0 || view.Total != 1 || view.FillsFresh != 0 || !strings.Contains(view.Reason, "complete recent fills 0/1") {
		t.Fatalf("legacy partial data advertised as complete: %+v", view)
	}
}
