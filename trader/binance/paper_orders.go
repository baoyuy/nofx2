package binance

import (
	"fmt"
	"nofx/logger"
	"strconv"
	"time"
)

func (p *PaperTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return p.open(symbol, "long", quantity, leverage)
}

func (p *PaperTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return p.open(symbol, "short", quantity, leverage)
}

func (p *PaperTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return p.closeManual(symbol, "long", quantity)
}

func (p *PaperTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return p.closeManual(symbol, "short", quantity)
}

// usedMarginLocked returns (unrealized PnL, initial margin) of all positions.
func (p *PaperTrader) usedMarginLocked(marks map[string]float64) (float64, float64) {
	var upnl, margin float64
	for _, pos := range p.state.Positions {
		mark := marks[pos.Symbol]
		if mark <= 0 {
			mark = pos.EntryPrice
		}
		upnl += pnlFor(pos.Side, pos.Quantity, pos.EntryPrice, mark)
		if pos.Leverage > 0 {
			margin += pos.Quantity * pos.EntryPrice / float64(pos.Leverage)
		}
	}
	return upnl, margin
}

// availableLocked is free margin: wallet + uPnL - position margin - margin
// reserved by resting entry orders. Caller holds p.mu.
func (p *PaperTrader) availableLocked(marks map[string]float64) float64 {
	upnl, used := p.usedMarginLocked(marks)
	return p.state.WalletBalance + upnl - used - p.reservedMarginLocked()
}

// addPositionLocked opens or adds to a position at fill and charges fee.
// Caller holds p.mu and has checked margin.
func (p *PaperTrader) addPositionLocked(symbol, side string, qty, fill float64, leverage int, fee float64) *paperPosition {
	key := paperKey(symbol, side)
	pos, ok := p.state.Positions[key]
	if !ok {
		pos = &paperPosition{Symbol: symbol, Side: side, OpenedAt: time.Now().UTC()}
		p.state.Positions[key] = pos
	}
	// Weighted average entry when adding to an existing position.
	pos.EntryPrice = (pos.EntryPrice*pos.Quantity + fill*qty) / (pos.Quantity + qty)
	pos.Quantity += qty
	pos.Leverage = leverage
	p.state.Leverage[symbol] = leverage
	p.state.WalletBalance -= fee
	return pos
}

func (p *PaperTrader) open(symbol, side string, quantity float64, leverage int) (map[string]interface{}, error) {
	if leverage <= 0 {
		leverage = 1
	}
	qtyStr, _ := p.FormatQuantity(symbol, quantity)
	qty, err := strconv.ParseFloat(qtyStr, 64)
	if err != nil || qty <= 0 {
		return nil, fmt.Errorf("position size too small, rounded to 0 (original: %.8f → formatted: %s)", quantity, qtyStr)
	}
	mark, err := p.price(symbol, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get market price: %w", err)
	}
	fill := mark * (1 + paperSlippageRate)
	if side == "short" {
		fill = mark * (1 - paperSlippageRate)
	}
	notional := qty * fill
	if notional < 10 {
		return nil, fmt.Errorf("order amount %.2f USDT is below minimum requirement 10.00 USDT", notional)
	}
	fee := notional * paperTakerFeeRate
	marks := p.marks()

	p.mu.Lock()
	defer p.mu.Unlock()
	if need, available := notional/float64(leverage)+fee, p.availableLocked(marks); need > available {
		return nil, fmt.Errorf("insufficient virtual balance: need %.2f USDT margin, available %.2f USDT", need, available)
	}

	pos := p.addPositionLocked(symbol, side, qty, fill, leverage, fee)
	// Real OpenLong/OpenShort cancel existing protective orders first.
	pos.StopLoss, pos.TakeProfit = 0, 0

	orderSide := "BUY"
	if side == "short" {
		orderSide = "SELL"
	}
	order := p.recordOrderLocked(symbol, orderSide, fill, qty, fee)
	if err := p.saveLocked(); err != nil {
		logger.Warnf("⚠️ [Paper] Failed to save state: %v", err)
	}
	logger.Infof("📝 [Paper] Opened %s %s qty=%s @ %.6f (fee %.4f USDT)", side, symbol, qtyStr, fill, fee)
	return map[string]interface{}{"orderId": order.OrderID, "symbol": symbol, "status": "FILLED"}, nil
}

func (p *PaperTrader) closeManual(symbol, side string, quantity float64) (map[string]interface{}, error) {
	mark, err := p.price(symbol, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get market price: %w", err)
	}
	fill := mark * (1 - paperSlippageRate)
	if side == "short" {
		fill = mark * (1 + paperSlippageRate)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.state.Positions[paperKey(symbol, side)]; !ok {
		return nil, fmt.Errorf("no %s position found for %s", side, symbol)
	}
	order, _ := p.closeLocked(symbol, side, quantity, fill, "manual")
	if err := p.saveLocked(); err != nil {
		logger.Warnf("⚠️ [Paper] Failed to save state: %v", err)
	}
	return map[string]interface{}{"orderId": order.OrderID, "symbol": symbol, "status": "FILLED"}, nil
}

// closeLocked closes quantity (0 = all) at fill price. Caller holds p.mu and
// has verified the position exists.
func (p *PaperTrader) closeLocked(symbol, side string, quantity, fill float64, reason string) (paperOrder, PaperCloseEvent) {
	key := paperKey(symbol, side)
	pos := p.state.Positions[key]
	qty := quantity
	if qty <= 0 || qty > pos.Quantity {
		qty = pos.Quantity
	}
	pnl := pnlFor(side, qty, pos.EntryPrice, fill)
	fee := qty * fill * paperTakerFeeRate
	p.state.WalletBalance += pnl - fee
	if p.state.WalletBalance < 0 {
		p.state.WalletBalance = 0
	}

	pos.Quantity -= qty
	if pos.Quantity <= 1e-12 {
		delete(p.state.Positions, key)
	}

	orderSide := "SELL"
	if side == "short" {
		orderSide = "BUY"
	}
	order := p.recordOrderLocked(symbol, orderSide, fill, qty, fee)
	now := time.Now().UTC()
	orderID := strconv.FormatInt(order.OrderID, 10)
	p.state.Closed = append(p.state.Closed, closedRecord(pos, qty, fill, pnl, fee, orderID, reason, now))
	logger.Infof("📝 [Paper] Closed %s %s qty=%.6f @ %.6f pnl=%.4f fee=%.4f (%s)", side, symbol, qty, fill, pnl, fee, reason)

	return order, PaperCloseEvent{
		OrderID: orderID, Symbol: symbol, Side: side, Quantity: qty,
		EntryPrice: pos.EntryPrice, ExitPrice: fill, Fee: fee,
		Leverage: pos.Leverage, Reason: reason, Time: now,
	}
}

func (p *PaperTrader) recordOrderLocked(symbol, side string, price, qty, fee float64) paperOrder {
	p.state.NextOrderID++
	order := paperOrder{
		OrderID: p.state.NextOrderID, Symbol: symbol, Side: side, Type: "MARKET",
		AvgPrice: price, Quantity: qty, Commission: fee, Time: time.Now().UTC(),
	}
	p.state.Orders = append(p.state.Orders, order)
	return order
}
