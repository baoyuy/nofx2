package trader

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"nofx/events"
	"nofx/kernel"
	"nofx/logger"
)

// envInt reads a positive integer env var, else the default.
func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// Event-context limits keep the AI prompt bounded. Events are unverified
// context, never orders: the feed is read-only and this path never trades.
const (
	maxEventWatchCoins       = 20
	maxEventMarketsPerItem   = 2
	maxEventSummaryRunes     = 240
	maxEventMarketRunes      = 160
	eventContextFetchTimeout = 60 * time.Second
)

// Tunable via env. More items per coin = more coverage but more model cost.
var maxEventItemsPerCoin = envInt("EVENT_MAX_ITEMS_PER_COIN", 20)

// buildEventContext fetches recent news/prediction items for the given symbols
// (candidate + held coins) and groups them per normalized coin. It returns nil
// when the strategy does not enable event context, so callers can skip the work.
// Network/model work is bounded and non-fatal: any failure yields no context.
func (at *AutoTrader) buildEventContext(symbols []string) map[string][]kernel.EventContextItem {
	if at == nil || at.eventFeed == nil || at.config.StrategyConfig == nil {
		return nil
	}
	cfg := at.config.StrategyConfig
	if !cfg.Indicators.EnableEventContext || strings.EqualFold(strings.TrimSpace(cfg.StrategyType), "grid_trading") {
		return nil
	}

	watches := make([]events.Watch, 0, len(symbols))
	seen := make(map[string]bool, len(symbols))
	for _, raw := range symbols {
		sym := strings.TrimSpace(raw)
		if sym == "" {
			continue
		}
		coin := events.Coin(sym)
		if coin == "" || seen[coin] {
			continue
		}
		seen[coin] = true
		watches = append(watches, events.Watch{Symbol: sym})
		if len(watches) >= maxEventWatchCoins {
			break
		}
	}
	if len(watches) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), eventContextFetchTimeout)
	defer cancel()
	board := at.eventFeed.Build(ctx, watches, 1, nil)
	if len(board.Events) == 0 {
		return nil
	}

	result := make(map[string][]kernel.EventContextItem)
	for _, e := range board.Events {
		item := convertEventContextItem(e)
		for _, coin := range e.Coins {
			if len(result[coin]) >= maxEventItemsPerCoin {
				continue
			}
			result[coin] = append(result[coin], item)
		}
	}
	if len(result) == 0 {
		return nil
	}
	logger.Infof("📰 [%s] Event context: %d coins, %d items (unverified, read-only)", at.name, len(result), len(board.Events))
	return result
}

func convertEventContextItem(e events.Event) kernel.EventContextItem {
	item := kernel.EventContextItem{
		ID:                e.ID,
		Title:             truncateEventText(e.Title, maxEventSummaryRunes),
		Source:            e.Source,
		Kind:              e.Kind,
		Publisher:         e.Publisher,
		Summary:           truncateEventText(e.Summary, maxEventSummaryRunes),
		PublishedAt:       e.PublishedAt,
		EndAt:             e.EndAt,
		SignificantChange: e.SignificantChange,
		EndingSoon:        e.EndingSoon,
	}
	for i, m := range e.Markets {
		if i >= maxEventMarketsPerItem {
			break
		}
		em := kernel.EventContextMarket{Question: truncateEventText(m.Question, maxEventMarketRunes)}
		for _, o := range m.Outcomes {
			em.Outcomes = append(em.Outcomes, kernel.EventContextOutcome{Name: o.Name, Price: o.Price})
		}
		item.Markets = append(item.Markets, em)
	}
	return item
}

func truncateEventText(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}
