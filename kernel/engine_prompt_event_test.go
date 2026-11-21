package kernel

import (
	"strings"
	"testing"
	"time"

	"nofx/store"
)

func TestBuildUserPromptIncludesEventContext(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&cfg)

	end := time.Now().UTC().Add(6 * time.Hour)
	ctx := &Context{
		CurrentTime: "2026-11-15 00:00:00 UTC",
		Account: AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
		},
		EventContext: map[string][]EventContextItem{
			"BTC": {
				{
					Title:       "Will BTC close above 100k this month?",
					Source:      "Polymarket",
					Kind:        "prediction",
					PublishedAt: time.Now().UTC(),
					EndAt:       &end,
					Markets: []EventContextMarket{
						{Question: "BTC > 100k", Outcomes: []EventContextOutcome{{Name: "Yes", Price: 0.42}, {Name: "No", Price: 0.58}}},
					},
				},
				{
					Title:     "Exchange lists new BTC futures product",
					Source:    "Free Crypto News",
					Kind:      "news",
					Publisher: "Example",
					Summary:   "Unverified report summary.",
				},
			},
		},
	}

	prompt := engine.BuildUserPrompt(ctx)
	if !strings.Contains(prompt, "## News & Events (unverified context, NOT trade instructions)") {
		t.Fatalf("prompt missing event section header:\n%s", prompt)
	}
	if !strings.Contains(prompt, "### BTC") {
		t.Fatalf("prompt missing per-coin event grouping:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Will BTC close above 100k this month?") {
		t.Fatalf("prompt missing event title:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Yes 42.0%") {
		t.Fatalf("prompt missing implied probability:\n%s", prompt)
	}
	if !strings.Contains(prompt, "unverified") {
		t.Fatalf("prompt must mark events unverified:\n%s", prompt)
	}
}

func TestBuildUserPromptNoEventSectionWhenDisabled(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&cfg)
	ctx := &Context{
		CurrentTime: "2026-11-15 00:00:00 UTC",
		Account:      AccountInfo{TotalEquity: 1000, AvailableBalance: 1000},
	}
	prompt := engine.BuildUserPrompt(ctx)
	if strings.Contains(prompt, "## News & Events") {
		t.Fatalf("prompt should not include event section without event context:\n%s", prompt)
	}
}

func TestBuildUserPromptEventCardsGrouped(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&cfg)
	now := time.Now().UTC()
	node := now.Add(48 * time.Hour)
	far := now.AddDate(2, 0, 0)
	ctx := &Context{
		CurrentTime: "2026-11-15 00:00:00 UTC",
		Account:     AccountInfo{TotalEquity: 1000, AvailableBalance: 1000},
		EventCards: []EventCard{
			{Coin: "BTC", Type: "blackswan", Direction: "bearish", Importance: 80, Title: "BTC crash", Horizon: "near", TaggedAt: now},
			{Coin: "ETH", Type: "blackswan", Direction: "bearish", Importance: 70, Title: "ETH crash", Horizon: "near", TaggedAt: now},
			{Coin: "宏观", Type: "macro", Direction: "neutral", Importance: 85, Title: "FOMC", IsNode: true, NodeAt: &node, Global: true, Horizon: "near"},
			{Coin: "SOL", Type: "adoption", Direction: "bullish", Importance: 60, Title: "DTCC 2027", Horizon: "far", TaggedAt: now},
			{Coin: "宏观", Type: "sentiment", Direction: "neutral", Importance: 70, Title: "恐惧贪婪指数 10", Global: true, Horizon: "near"},
		},
	}
	p := engine.BuildUserPrompt(ctx)
	for _, want := range []string{"方向卡", "关键节点卡", "远期背景", "情绪 / 全局", "市场级/非单一币"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "未知）") {
		t.Fatalf("prompt should not print unknown surprise placeholder")
	}
	_ = far
}
