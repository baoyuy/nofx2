package binance

import (
	"fmt"
	"nofx/logger"
	"nofx/trader/types"
	"sort"
	"strconv"
	"time"
)

const paperMaxPendingCloses = 200

func closedRecord(pos *paperPosition, qty, exit, pnl, fee float64, orderID, reason string, at time.Time) types.ClosedPnLRecord {
	return types.ClosedPnLRecord{
		Symbol: pos.Symbol, Side: pos.Side, EntryPrice: pos.EntryPrice, ExitPrice: exit,
		Quantity: qty, RealizedPnL: pnl, Fee: fee, Leverage: pos.Leverage,
		EntryTime: pos.OpenedAt, ExitTime: at, OrderID: orderID, CloseType: reason, ExchangeID: orderID,
	}
}

// monitorLoop fires stop-loss / take-profit / liquidation against live prices.
func (p *PaperTrader) monitorLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		p.checkTriggers()
	}
}

func (p *PaperTrader) checkTriggers() {
	marks := p.marks()
	if len(marks) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := p.matchExecLocked(marks)
	for _, pos := range p.state.Positions {
		mark, ok := marks[pos.Symbol]
		if !ok {
			continue
		}
		reason, fill := triggerFor(pos, mark)
		if reason == "" {
			continue
		}
		_, event := p.closeLocked(pos.Symbol, pos.Side, 0, fill, reason)
		p.state.PendingCloses = append(p.state.PendingCloses, event)
		if n := len(p.state.PendingCloses); n > paperMaxPendingCloses {
			p.state.PendingCloses = p.state.PendingCloses[n-paperMaxPendingCloses:]
		}
		changed = true
	}
	if changed {
		if err := p.saveLocked(); err != nil {
			logger.Warnf("⚠️ [Paper] Failed to save state: %v", err)
		}
	}
}

// triggerFor checks liquidation first, then stop-loss, then take-profit.
// Stops fill at the worse of mark and stop price (gaps are not forgiven).
func triggerFor(pos *paperPosition, mark float64) (string, float64) {
	liq := liquidationPrice(pos)
	if pos.Side == "long" {
		switch {
		case liq > 0 && mark <= liq:
			return "liquidation", liq
		case pos.StopLoss > 0 && mark <= pos.StopLoss:
			return "stop_loss", mark * (1 - paperSlippageRate)
		case pos.TakeProfit > 0 && mark >= pos.TakeProfit:
			return "take_profit", mark * (1 - paperSlippageRate)
		}
		return "", 0
	}
	switch {
	case liq > 0 && mark >= liq:
		return "liquidation", liq
	case pos.StopLoss > 0 && mark >= pos.StopLoss:
		return "stop_loss", mark * (1 + paperSlippageRate)
	case pos.TakeProfit > 0 && mark <= pos.TakeProfit:
		return "take_profit", mark * (1 + paperSlippageRate)
	}
	return "", 0
}

// DrainCloseEvents returns and clears simulator-triggered closes.
func (p *PaperTrader) DrainCloseEvents() []PaperCloseEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.state.PendingCloses) == 0 {
		return nil
	}
	events := p.state.PendingCloses
	p.state.PendingCloses = nil
	if err := p.saveLocked(); err != nil {
		logger.Warnf("⚠️ [Paper] Failed to save state: %v", err)
	}
	return events
}

func (p *PaperTrader) GetBalance() (map[string]interface{}, error) {
	marks := p.marks()
	p.mu.Lock()
	defer p.mu.Unlock()
	upnl, _ := p.usedMarginLocked(marks)
	available := p.availableLocked(marks)
	if available < 0 {
		available = 0
	}
	return map[string]interface{}{
		"totalWalletBalance":    p.state.WalletBalance,
		"availableBalance":      available,
		"totalUnrealizedProfit": upnl,
		"totalEquity":           p.state.WalletBalance + upnl,
		"paperTrading":          true,
	}, nil
}

func (p *PaperTrader) GetPositions() ([]map[string]interface{}, error) {
	marks := p.marks()
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(p.state.Positions))
	for k := range p.state.Positions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		pos := p.state.Positions[k]
		mark := marks[pos.Symbol]
		if mark <= 0 {
			mark = pos.EntryPrice
		}
		amt := pos.Quantity
		if pos.Side == "short" {
			amt = -amt // Binance hedge-mode convention
		}
		result = append(result, map[string]interface{}{
			"symbol":           pos.Symbol,
			"positionAmt":      amt,
			"entryPrice":       pos.EntryPrice,
			"markPrice":        mark,
			"unRealizedProfit": pnlFor(pos.Side, pos.Quantity, pos.EntryPrice, mark),
			"leverage":         float64(pos.Leverage),
			"liquidationPrice": liquidationPrice(pos),
			"side":             pos.Side,
		})
	}
	return result, nil
}

func (p *PaperTrader) SetLeverage(symbol string, leverage int) error {
	if leverage < 1 || leverage > 125 {
		return fmt.Errorf("invalid leverage %d", leverage)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Leverage[symbol] = leverage
	return nil
}

func (p *PaperTrader) SetMarginMode(symbol string, isCrossMargin bool) error { return nil }

func (p *PaperTrader) setProtect(symbol, positionSide string, price float64, stop bool) error {
	side := "long"
	if positionSide == "SHORT" || positionSide == "short" {
		side = "short"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pos, ok := p.state.Positions[paperKey(symbol, side)]
	if !ok {
		return fmt.Errorf("no %s position for %s", side, symbol)
	}
	if stop {
		pos.StopLoss = price
	} else {
		pos.TakeProfit = price
	}
	return p.saveLocked()
}

func (p *PaperTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	logger.Infof("  [Paper] Stop-loss set: %s %s @ %.6f", symbol, positionSide, stopPrice)
	return p.setProtect(symbol, positionSide, stopPrice, true)
}

func (p *PaperTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	logger.Infof("  [Paper] Take-profit set: %s %s @ %.6f", symbol, positionSide, takeProfitPrice)
	return p.setProtect(symbol, positionSide, takeProfitPrice, false)
}

func (p *PaperTrader) clearProtect(symbol string, stop, take bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pos := range p.state.Positions {
		if pos.Symbol != symbol {
			continue
		}
		if stop {
			pos.StopLoss = 0
		}
		if take {
			pos.TakeProfit = 0
		}
	}
	return p.saveLocked()
}

func (p *PaperTrader) CancelStopLossOrders(symbol string) error {
	return p.clearProtect(symbol, true, false)
}
func (p *PaperTrader) CancelTakeProfitOrders(symbol string) error {
	return p.clearProtect(symbol, false, true)
}
func (p *PaperTrader) CancelAllOrders(symbol string) error  { return p.clearProtect(symbol, true, true) }
func (p *PaperTrader) CancelStopOrders(symbol string) error { return p.clearProtect(symbol, true, true) }

func (p *PaperTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	id, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid order ID: %s", orderID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.state.Orders) - 1; i >= 0; i-- {
		o := p.state.Orders[i]
		if o.OrderID == id {
			return map[string]interface{}{
				"orderId": o.OrderID, "symbol": o.Symbol, "status": "FILLED",
				"avgPrice": o.AvgPrice, "executedQty": o.Quantity, "side": o.Side,
				"type": o.Type, "time": o.Time.UnixMilli(), "updateTime": o.Time.UnixMilli(),
				"commission": o.Commission,
			}, nil
		}
	}
	return nil, fmt.Errorf("order %s not found", orderID)
}

func (p *PaperTrader) GetClosedPnL(startTime time.Time, limit int) ([]types.ClosedPnLRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []types.ClosedPnLRecord
	for _, r := range p.state.Closed {
		if !r.ExitTime.Before(startTime) {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (p *PaperTrader) GetOpenOrders(symbol string) ([]types.OpenOrder, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []types.OpenOrder
	for _, pos := range p.state.Positions {
		if pos.Symbol != symbol {
			continue
		}
		closeSide, posSide := "SELL", "LONG"
		if pos.Side == "short" {
			closeSide, posSide = "BUY", "SHORT"
		}
		if pos.StopLoss > 0 {
			out = append(out, types.OpenOrder{OrderID: "paper-sl-" + posSide, Symbol: symbol, Side: closeSide,
				PositionSide: posSide, Type: "STOP_MARKET", StopPrice: pos.StopLoss, Quantity: pos.Quantity, Status: "NEW"})
		}
		if pos.TakeProfit > 0 {
			out = append(out, types.OpenOrder{OrderID: "paper-tp-" + posSide, Symbol: symbol, Side: closeSide,
				PositionSide: posSide, Type: "TAKE_PROFIT_MARKET", StopPrice: pos.TakeProfit, Quantity: pos.Quantity, Status: "NEW"})
		}
	}
	return out, nil
}

var _ types.Trader = (*PaperTrader)(nil)
