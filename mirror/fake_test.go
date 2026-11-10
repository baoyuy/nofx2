package mirror

import (
	"fmt"
	"sync"
	"time"

	"nofx/trader/types"
)

// fakeAccount is an exchange account with one price per coin.
type fakeAccount struct {
	mu        sync.Mutex
	prices    map[string]float64
	positions map[string]*Position
	wallet    float64
	stops     map[string]float64 // key(symbol, side) → stop loss
	takes     map[string]float64
	orders    []string
	failOpen  error
	failClose error
}

func newFake(wallet float64) *fakeAccount {
	return &fakeAccount{prices: map[string]float64{}, positions: map[string]*Position{}, wallet: wallet,
		stops: map[string]float64{}, takes: map[string]float64{}}
}

func (f *fakeAccount) GetBalance() (map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	upnl, used := 0.0, 0.0
	for _, p := range f.positions {
		mark := f.prices[p.Symbol]
		d := (mark - p.Entry) * p.Qty
		if p.Side == "short" {
			d = -d
		}
		upnl += d
		used += p.Qty * p.Entry / float64(p.Leverage)
	}
	return map[string]interface{}{"totalWalletBalance": f.wallet, "totalUnrealizedProfit": upnl,
		"availableBalance": f.wallet + upnl - used}, nil
}

func (f *fakeAccount) GetPositions() ([]map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]interface{}
	for _, p := range f.positions {
		amt := p.Qty
		if p.Side == "short" {
			amt = -amt
		}
		out = append(out, map[string]interface{}{"symbol": p.Symbol, "side": p.Side, "positionAmt": amt,
			"entryPrice": p.Entry, "markPrice": f.prices[p.Symbol], "leverage": float64(p.Leverage)})
	}
	return out, nil
}

func (f *fakeAccount) open(symbol, side string, qty float64, lev int) (map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOpen != nil {
		return nil, f.failOpen
	}
	k := key(symbol, side)
	price := f.prices[symbol]
	if p := f.positions[k]; p != nil {
		p.Entry = (p.Entry*p.Qty + price*qty) / (p.Qty + qty)
		p.Qty += qty
	} else {
		f.positions[k] = &Position{Symbol: symbol, Side: side, Qty: qty, Entry: price, Leverage: lev}
	}
	f.orders = append(f.orders, fmt.Sprintf("open %s %s %.4f %dx", symbol, side, qty, lev))
	return map[string]interface{}{"orderId": int64(1)}, nil
}

func (f *fakeAccount) close(symbol, side string, qty float64) (map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failClose != nil {
		return nil, f.failClose
	}
	k := key(symbol, side)
	p := f.positions[k]
	if p == nil {
		return nil, fmt.Errorf("no %s position", side)
	}
	if qty == 0 || qty >= p.Qty-1e-12 {
		qty = p.Qty
	}
	d := (f.prices[symbol] - p.Entry) * qty
	if side == "short" {
		d = -d
	}
	f.wallet += d
	p.Qty -= qty
	if p.Qty <= 1e-12 {
		delete(f.positions, k)
	}
	f.orders = append(f.orders, fmt.Sprintf("close %s %s %.4f", symbol, side, qty))
	return map[string]interface{}{"orderId": int64(2)}, nil
}

func (f *fakeAccount) OpenLong(s string, q float64, l int) (map[string]interface{}, error) {
	return f.open(s, "long", q, l)
}
func (f *fakeAccount) OpenShort(s string, q float64, l int) (map[string]interface{}, error) {
	return f.open(s, "short", q, l)
}
func (f *fakeAccount) CloseLong(s string, q float64) (map[string]interface{}, error) {
	return f.close(s, "long", q)
}
func (f *fakeAccount) CloseShort(s string, q float64) (map[string]interface{}, error) {
	return f.close(s, "short", q)
}
func (f *fakeAccount) SetLeverage(string, int) error    { return nil }
func (f *fakeAccount) SetMarginMode(string, bool) error { return nil }
func (f *fakeAccount) GetMarketPrice(s string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prices[s], nil
}
func (f *fakeAccount) SetStopLoss(s, ps string, _ float64, p float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops[key(s, sideOf(ps))] = p
	return nil
}
func (f *fakeAccount) SetTakeProfit(s, ps string, _ float64, p float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.takes[key(s, sideOf(ps))] = p
	return nil
}
func (f *fakeAccount) CancelStopOrders(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, side := range []string{"long", "short"} {
		delete(f.stops, key(s, side))
		delete(f.takes, key(s, side))
	}
	return nil
}
func (f *fakeAccount) CancelStopLossOrders(s string) error   { return f.CancelStopOrders(s) }
func (f *fakeAccount) CancelTakeProfitOrders(s string) error { return f.CancelStopOrders(s) }
func (f *fakeAccount) CancelAllOrders(s string) error        { return f.CancelStopOrders(s) }
func (f *fakeAccount) FormatQuantity(_ string, q float64) (string, error) {
	return fmt.Sprintf("%.4f", q), nil
}
func (f *fakeAccount) GetOrderStatus(string, string) (map[string]interface{}, error) { return nil, nil }
func (f *fakeAccount) GetClosedPnL(time.Time, int) ([]types.ClosedPnLRecord, error) {
	return nil, nil
}
func (f *fakeAccount) GetOpenOrders(s string) ([]types.OpenOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []types.OpenOrder
	for _, side := range []string{"long", "short"} {
		ps := "LONG"
		if side == "short" {
			ps = "SHORT"
		}
		if p := f.stops[key(s, side)]; p > 0 {
			out = append(out, types.OpenOrder{Symbol: s, PositionSide: ps, Type: "STOP_MARKET", StopPrice: p})
		}
		if p := f.takes[key(s, side)]; p > 0 {
			out = append(out, types.OpenOrder{Symbol: s, PositionSide: ps, Type: "TAKE_PROFIT_MARKET", StopPrice: p})
		}
	}
	return out, nil
}

func sideOf(ps string) string {
	if ps == "SHORT" {
		return "short"
	}
	return "long"
}

func (f *fakeAccount) qty(symbol, side string) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.positions[key(symbol, side)]; p != nil {
		return p.Qty
	}
	return 0
}

func (f *fakeAccount) setPrice(symbol string, p float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prices[symbol] = p
}

var _ types.Trader = (*fakeAccount)(nil)
