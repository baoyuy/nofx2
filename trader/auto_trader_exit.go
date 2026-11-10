package trader

import (
	"fmt"
	"math"
	"strings"

	"nofx/kernel"
	"nofx/store"
)

func (at *AutoTrader) executeCloseLongWithRecord(d *kernel.Decision, record *store.DecisionAction) error {
	return at.executeCurrentPositionClose(d.Symbol, "long", record)
}

func (at *AutoTrader) executeCloseShortWithRecord(d *kernel.Decision, record *store.DecisionAction) error {
	return at.executeCurrentPositionClose(d.Symbol, "short", record)
}

// Execute exits without a candle/indicator dependency. All mutable position
// actions share this lock, and the position is re-read after acquiring it.
func (at *AutoTrader) executeCurrentPositionClose(symbol, side string, record *store.DecisionAction) error {
	unlockLifecycle := at.LockPositionLifecycle(symbol, side)
	defer unlockLifecycle()
	at.positionActionMu.Lock()
	defer at.positionActionMu.Unlock()
	return at.closeCurrentPositionLocked(symbol, side, record)
}

// Caller holds positionActionMu. This is also used by protective exits so a
// slow AI decision cannot close twice or use an obsolete database quantity.
func (at *AutoTrader) closeCurrentPositionLocked(symbol, side string, record *store.DecisionAction) error {
	if side != "long" && side != "short" {
		return fmt.Errorf("invalid close side %q", side)
	}
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("cannot verify current position before close: %w", err)
	}
	var quantity, entry, mark float64
	for _, pos := range positions {
		posSymbol, _ := pos["symbol"].(string)
		posSide, _ := pos["side"].(string)
		if !strings.EqualFold(at.normSymbol(posSymbol), at.normSymbol(symbol)) || !strings.EqualFold(posSide, side) {
			continue
		}
		amount, ok := pos["positionAmt"].(float64)
		if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) {
			return fmt.Errorf("invalid current position quantity for %s %s", symbol, side)
		}
		quantity = math.Abs(amount)
		entry, _ = pos["entryPrice"].(float64)
		mark, _ = pos["markPrice"].(float64)
		break
	}
	if quantity == 0 {
		return fmt.Errorf("%s %s is already closed or absent; no duplicate close submitted", symbol, side)
	}
	// A missing mark must not block risk reduction. The exchange submits a
	// close-all order and the existing confirmation path obtains actual fills.
	if math.IsNaN(mark) || math.IsInf(mark, 0) || mark < 0 {
		mark = 0
	}
	var order map[string]interface{}
	if side == "long" {
		order, err = at.trader.CloseLong(symbol, 0)
	} else {
		order, err = at.trader.CloseShort(symbol, 0)
	}
	if err != nil {
		return fmt.Errorf("failed to submit %s close for %s: %w", side, symbol, err)
	}
	if record != nil {
		record.Quantity, record.Price = quantity, mark
		if id, ok := order["orderId"].(int64); ok {
			record.OrderID = id
		}
	}
	at.recordAndConfirmOrder(order, symbol, "close_"+side, quantity, mark, 0, entry)
	at.logInfof("Close %s submitted for %s; fill confirmation/sync uses the exchange result", side, symbol)
	return nil
}
