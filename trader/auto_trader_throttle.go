package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"strings"
	"time"
)

// Re-entry cooldown limits churn without preventing risk reduction. A model's
// close decision must never be held hostage by an age or small-PnL window.
const autopilotReentryCooldown = 4 * time.Hour

func isOpenAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "open_long", "open_short":
		return true
	default:
		return false
	}
}

func isCloseAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "close_long", "close_short":
		return true
	default:
		return false
	}
}

func closeActionSide(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "close_long":
		return "long"
	case "close_short":
		return "short"
	default:
		return ""
	}
}

func openActionSide(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "open_long":
		return "long"
	case "open_short":
		return "short"
	default:
		return ""
	}
}

func normalizedDecisionSymbol(symbol string) string {
	return market.Normalize(strings.TrimSpace(symbol))
}

func (at *AutoTrader) tradeThrottleReason(decision kernel.Decision, ctx *kernel.Context) string {
	if ctx == nil {
		return ""
	}

	switch {
	case isOpenAction(decision.Action):
		return at.openThrottleReason(decision, ctx)
	default:
		return ""
	}
}

func (at *AutoTrader) openThrottleReason(decision kernel.Decision, ctx *kernel.Context) string {
	symbol := normalizedDecisionSymbol(decision.Symbol)
	if symbol == "" {
		return ""
	}

	if pos := findAnyContextPosition(ctx, symbol); pos != nil {
		return fmt.Sprintf("trade throttle: %s already has an open %s position; manage or close it before opening another side", symbol, pos.Side)
	}

	if !at.usesVergexSignalPolicy() {
		if order := at.findRecentCloseOrder(symbol, time.Now().Add(-autopilotReentryCooldown)); order != nil {
			age := time.Since(time.UnixMilli(order.CreatedAt))
			remaining := autopilotReentryCooldown - age
			if remaining < 0 {
				remaining = 0
			}
			return fmt.Sprintf("trade throttle: %s was closed %s ago; wait %s before re-entry", symbol, roundDuration(age), roundDuration(remaining))
		}
	}

	return ""
}

func findContextPosition(ctx *kernel.Context, symbol string, side string) *kernel.PositionInfo {
	if ctx == nil {
		return nil
	}
	for i := range ctx.Positions {
		pos := &ctx.Positions[i]
		if normalizedDecisionSymbol(pos.Symbol) == symbol && strings.EqualFold(pos.Side, side) {
			return pos
		}
	}
	return nil
}

func findAnyContextPosition(ctx *kernel.Context, symbol string) *kernel.PositionInfo {
	if ctx == nil {
		return nil
	}
	for i := range ctx.Positions {
		pos := &ctx.Positions[i]
		if normalizedDecisionSymbol(pos.Symbol) == symbol {
			return pos
		}
	}
	return nil
}

func (at *AutoTrader) recentOrders(limit int) ([]*store.TraderOrder, error) {
	if at == nil || at.store == nil {
		return nil, nil
	}
	return at.store.Order().GetTraderOrders(at.id, limit)
}

func (at *AutoTrader) findRecentCloseOrder(symbol string, since time.Time) *store.TraderOrder {
	orders, err := at.recentOrders(100)
	if err != nil {
		at.logWarnf("⚠️ Trade throttle could not read recent close orders: %v", err)
		return nil
	}
	sinceMs := since.UTC().UnixMilli()
	for _, order := range orders {
		if order == nil || order.CreatedAt < sinceMs || isCanceledOrder(order) {
			continue
		}
		if normalizedDecisionSymbol(order.Symbol) == symbol && isCloseAction(order.OrderAction) {
			return order
		}
	}
	return nil
}

func isCanceledOrder(order *store.TraderOrder) bool {
	status := strings.ToUpper(strings.TrimSpace(order.Status))
	return status == "CANCELED" || status == "CANCELLED" || status == "REJECTED" || status == "EXPIRED"
}

func roundDuration(d time.Duration) string {
	if d < time.Minute {
		return "0m"
	}
	return d.Round(time.Minute).String()
}
