package binance

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"nofx/logger"
	"nofx/trader/execution"
	"strconv"
	"time"
)

// Frontier execution on the paper account. It implements execution.Trader
// so approved plans run against the simulator exactly like a real Binance
// account: resting limit entries, market entries, reduce-only closes and
// stop / take-profit trigger orders, matched against live Binance prices.

const (
	paperMakerFeeRate  = 0.0002 // 0.02% for resting limit fills
	paperRoutePrefix   = "paper"
	paperPollQuietTime = 30 * time.Second
)

type paperExecOrder struct {
	Order    execution.Order `json:"order"`
	Leverage int             `json:"leverage"`
	Reserved float64         `json:"reserved"` // margin held while a limit entry rests
	Created  time.Time       `json:"created"`
	Polled   time.Time       `json:"polled"`
}

func (o *paperExecOrder) open() bool {
	return o.Order.Status == "NEW" || o.Order.Status == "PARTIALLY_FILLED"
}

func (p *PaperTrader) reservedMarginLocked() float64 {
	var total float64
	for _, o := range p.state.ExecOrders {
		if o.open() && o.Order.Role == "entry" {
			total += o.Reserved
		}
	}
	return total
}

func paperDecimal(v float64) execution.Decimal {
	return execution.Decimal(strconv.FormatFloat(v, 'f', 8, 64))
}

func paperFloat(d execution.Decimal) float64 {
	v, _ := strconv.ParseFloat(string(d), 64)
	return v
}

// Venue reports the market the simulator prices against, so plans use
// Binance instruments, precision and books.
func (p *PaperTrader) Venue() string { return "binance" }

func (p *PaperTrader) RouteClientID(id string) (string, error) {
	if raw, e := hex.DecodeString(id); e != nil || len(raw) != 16 {
		return "", errors.New("invalid execution client ID")
	}
	return paperRoutePrefix + id, nil
}

func (p *PaperTrader) AccountSnapshot(_ context.Context, symbol string) (execution.Account, error) {
	marks := p.marks()
	p.mu.Lock()
	defer p.mu.Unlock()
	upnl, _ := p.usedMarginLocked(marks)
	available := p.availableLocked(marks)
	if available < 0 {
		available = 0
	}
	var position float64
	openOrders := 0
	for _, pos := range p.state.Positions {
		if pos.Symbol != symbol {
			continue
		}
		position += pos.Quantity
		if pos.StopLoss > 0 {
			openOrders++
		}
		if pos.TakeProfit > 0 {
			openOrders++
		}
	}
	for _, o := range p.state.ExecOrders {
		if o.Order.Symbol == symbol && o.open() {
			openOrders++
		}
	}
	return execution.Account{
		Equity:     paperDecimal(p.state.WalletBalance + upnl),
		Available:  paperDecimal(available),
		FeeBPS:     paperDecimal(paperTakerFeeRate * 10000),
		Position:   paperDecimal(position),
		OpenOrders: openOrders,
	}, nil
}

func (p *PaperTrader) PrepareExecution(ctx context.Context, symbol string) error {
	return p.PrepareExecutionLeverage(ctx, symbol, 1)
}

func (p *PaperTrader) PrepareExecutionLeverage(_ context.Context, symbol string, leverage int) error {
	if leverage < 1 || leverage > 20 {
		return errors.New("unsupported planning leverage")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Leverage[symbol] = leverage
	return p.saveLocked()
}
func (p *PaperTrader) findExecLocked(clientID string) *paperExecOrder {
	for i := len(p.state.ExecOrders) - 1; i >= 0; i-- {
		if p.state.ExecOrders[i].Order.ClientID == clientID {
			return p.state.ExecOrders[i]
		}
	}
	return nil
}

func opposite(side string) string {
	if side == "long" {
		return "short"
	}
	return "long"
}

// takerFill is the price a market order in direction side receives.
func takerFill(side string, mark float64) float64 {
	if side == "long" {
		return mark * (1 + paperSlippageRate)
	}
	return mark * (1 - paperSlippageRate)
}

// SubmitOrder never returns an error for a deterministic simulator outcome:
// insufficient margin, a crossing post-only order or a missing position come
// back as REJECTED / EXPIRED receipts, as Binance would report them.
func (p *PaperTrader) SubmitOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	if o.Type != "" && (o.Type != "market" || o.Role != "entry" || o.PostOnly) {
		return o, errors.New("unsupported execution order type")
	}
	if route, e := p.RouteClientID(o.ClientID); e != nil || o.VenueClientID != route {
		return o, errors.New("Broker route must be persisted before submitting a new order")
	}
	if !execution.ValidOrderPrice(o) || !execution.Positive(o.Quantity) || (o.Side != "long" && o.Side != "short") ||
		(o.Role != "entry" && o.Role != "sl" && o.Role != "tp") || (o.ReduceOnly && o.Type != "market") {
		return o, errors.New("invalid locked order")
	}
	mark, priceErr := p.price(o.Symbol, true)
	marks := p.marks()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.findExecLocked(o.ClientID) != nil {
		return o, errors.New("duplicate client order ID; reconcile the existing order")
	}
	p.state.NextOrderID++
	o.OrderID = strconv.FormatInt(p.state.NextOrderID, 10)
	o.Status, o.Filled, o.Average, o.Error = "NEW", "0", "", ""
	leverage := p.state.Leverage[o.Symbol]
	if leverage < 1 {
		leverage = 1
	}
	now := time.Now().UTC()
	rec := &paperExecOrder{Order: o, Leverage: leverage, Created: now, Polled: now}
	reject := func(message string) {
		rec.Order.Status, rec.Order.Error = "REJECTED", message
	}
	price := paperFloat(o.Price)
	switch {
	case priceErr != nil || mark <= 0:
		reject("live Binance price unavailable")
	case o.Role == "entry" && o.ReduceOnly:
		p.closeExecLocked(rec, opposite(o.Side), takerFill(o.Side, mark), "manual")
	case o.Role == "entry" && o.Type == "market":
		p.fillEntryLocked(rec, takerFill(o.Side, mark), paperTakerFeeRate, marks, false)
	case o.Role == "entry":
		crosses := (o.Side == "long" && mark <= price) || (o.Side == "short" && mark >= price)
		if crosses && o.PostOnly {
			rec.Order.Status = "EXPIRED" // GTX would take liquidity
		} else if crosses {
			fill := takerFill(o.Side, mark)
			if (o.Side == "long" && fill > price) || (o.Side == "short" && fill < price) {
				fill = price
			}
			p.fillEntryLocked(rec, fill, paperTakerFeeRate, marks, false)
		} else {
			notional := paperFloat(o.Quantity) * price
			rec.Reserved = notional/float64(leverage) + notional*paperMakerFeeRate
			if rec.Reserved > p.availableLocked(marks) {
				reject(fmt.Sprintf("insufficient virtual margin: need %.2f USDT", rec.Reserved))
				rec.Reserved = 0
			}
		}
	default: // sl / tp trigger orders
		if triggerReached(o.Role, o.Side, mark, price) {
			reject("order would immediately trigger")
		}
	}
	p.state.ExecOrders = append(p.state.ExecOrders, rec)
	if err := p.saveLocked(); err != nil {
		logger.Warnf("⚠️ [Paper] Failed to save state: %v", err)
	}
	return p.receipt(o, rec), nil
}

// receipt returns the request with the simulator's state, keeping the
// caller's own fields (route, parent) unchanged.
func (p *PaperTrader) receipt(request execution.Order, rec *paperExecOrder) execution.Order {
	request.OrderID = rec.Order.OrderID
	request.Status = rec.Order.Status
	request.Filled = rec.Order.Filled
	request.Average = rec.Order.Average
	request.Error = rec.Order.Error
	return request
}

func (p *PaperTrader) fillEntryLocked(rec *paperExecOrder, fill, feeRate float64, marks map[string]float64, rested bool) {
	o := &rec.Order
	qty := paperFloat(o.Quantity)
	notional := qty * fill
	fee := notional * feeRate
	rec.Reserved = 0
	if !rested {
		if need := notional/float64(rec.Leverage) + fee; need > p.availableLocked(marks) {
			o.Status, o.Error = "REJECTED", fmt.Sprintf("insufficient virtual margin: need %.2f USDT", need)
			return
		}
	}
	p.addPositionLocked(o.Symbol, o.Side, qty, fill, rec.Leverage, fee)
	orderSide := "BUY"
	if o.Side == "short" {
		orderSide = "SELL"
	}
	p.recordOrderLocked(o.Symbol, orderSide, fill, qty, fee)
	o.Status, o.Filled, o.Average = "FILLED", o.Quantity, paperDecimal(fill)
	logger.Infof("📝 [Paper] Frontier %s entry %s qty=%s @ %.6f", o.Side, o.Symbol, o.Quantity, fill)
}

// closeExecLocked reduces the posSide position by the order quantity. A
// missing position expires the order; a smaller one fills partially and
// expires the rest, like a Binance reduce-only order.
func (p *PaperTrader) closeExecLocked(rec *paperExecOrder, posSide string, fill float64, reason string) {
	o := &rec.Order
	pos := p.state.Positions[paperKey(o.Symbol, posSide)]
	if pos == nil {
		o.Status = "EXPIRED"
		return
	}
	want := paperFloat(o.Quantity)
	qty := want
	if pos.Quantity < qty {
		qty = pos.Quantity
	}
	p.closeLocked(o.Symbol, posSide, qty, fill, reason)
	o.Average = paperDecimal(fill)
	if qty >= want-1e-12 {
		o.Status, o.Filled = "FILLED", o.Quantity
	} else {
		o.Status, o.Filled = "EXPIRED", paperDecimal(qty)
	}
	if p.state.Positions[paperKey(o.Symbol, posSide)] == nil {
		// The position is flat: remaining protection can never fill.
		for _, other := range p.state.ExecOrders {
			if other != rec && other.open() && other.Order.Role != "entry" &&
				other.Order.Symbol == o.Symbol && other.Order.Side == posSide {
				other.Order.Status = "EXPIRED"
			}
		}
	}
}

// matchOneLocked fills a resting order the mark has reached.
func (p *PaperTrader) matchOneLocked(rec *paperExecOrder, mark float64, marks map[string]float64) bool {
	if !rec.open() || mark <= 0 {
		return false
	}
	o := rec.Order
	price := paperFloat(o.Price)
	long := o.Side == "long"
	switch o.Role {
	case "entry":
		if (long && mark <= price) || (!long && mark >= price) {
			p.fillEntryLocked(rec, price, paperMakerFeeRate, marks, true)
			return true
		}
	case "sl", "tp":
		if triggerReached(o.Role, o.Side, mark, price) {
			reason := "stop_loss"
			if o.Role == "tp" {
				reason = "take_profit"
			}
			p.closeExecLocked(rec, o.Side, takerFill(opposite(o.Side), mark), reason)
			return true
		}
	}
	return false
}

// triggerReached: a long's stop fires at or below its price and its target
// at or above; a short's the other way round.
func triggerReached(role, side string, mark, price float64) bool {
	down := (role == "sl") == (side == "long")
	if down {
		return mark <= price
	}
	return mark >= price
}

// matchExecLocked is the background pass. Orders the Frontier controller is
// actively polling are matched by LookupOrder instead, so a fill never lands
// between the controller's order and position reads.
func (p *PaperTrader) matchExecLocked(marks map[string]float64) bool {
	changed := false
	now := time.Now()
	for _, rec := range p.state.ExecOrders {
		if !rec.open() || now.Sub(rec.Polled) < paperPollQuietTime {
			continue
		}
		if mark, ok := marks[rec.Order.Symbol]; ok && p.matchOneLocked(rec, mark, marks) {
			changed = true
		}
	}
	return changed
}

func (p *PaperTrader) LookupOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	mark, priceErr := p.price(o.Symbol, false)
	marks := p.marks()
	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.findExecLocked(o.ClientID)
	if rec == nil {
		return o, errors.New("Paper order lookup is unconfirmed; missing does not mean filled")
	}
	if rec.Order.Symbol != o.Symbol || rec.Order.Role != o.Role || rec.Order.Side != o.Side ||
		!sameNativeDecimal(string(rec.Order.Quantity), string(o.Quantity)) ||
		!sameNativeDecimal(string(rec.Order.Price), string(o.Price)) {
		return o, errors.New("order identity mismatch")
	}
	rec.Polled = time.Now()
	if priceErr == nil && p.matchOneLocked(rec, mark, marks) {
		if err := p.saveLocked(); err != nil {
			logger.Warnf("⚠️ [Paper] Failed to save state: %v", err)
		}
	}
	return p.receipt(o, rec), nil
}

func (p *PaperTrader) CancelExecutionOrder(_ context.Context, o execution.Order) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.findExecLocked(o.ClientID)
	if rec == nil {
		return errors.New("Paper order cancellation is unconfirmed: order not found")
	}
	if !rec.open() {
		return fmt.Errorf("Paper order is already %s", rec.Order.Status)
	}
	rec.Order.Status = "CANCELED"
	rec.Reserved = 0
	return p.saveLocked()
}

var _ execution.Trader = (*PaperTrader)(nil)

// trimExecLocked keeps every open order and the newest finished ones.
func (p *PaperTrader) trimExecLocked() {
	finished := 0
	for _, o := range p.state.ExecOrders {
		if !o.open() {
			finished++
		}
	}
	if finished <= paperMaxHistory {
		return
	}
	drop := finished - paperMaxHistory
	kept := p.state.ExecOrders[:0]
	for _, o := range p.state.ExecOrders {
		if !o.open() && drop > 0 {
			drop--
			continue
		}
		kept = append(kept, o)
	}
	p.state.ExecOrders = kept
}
