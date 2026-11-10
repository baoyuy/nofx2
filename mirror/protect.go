package mirror

import (
	"fmt"
	"math"
	"strings"

	"nofx/store"
	"nofx/trader/types"
)

// protectOf reads the AI's stop loss and take profit of one side.
func protectOf(orders []Order, side string) (sl, tp float64) {
	want := strings.ToUpper(side)
	for _, o := range orders {
		if o.PositionSide != want {
			continue
		}
		price := o.StopPrice
		if price <= 0 {
			price = o.Price
		}
		t := strings.ToUpper(o.Type)
		switch {
		case strings.Contains(t, "TAKE_PROFIT"):
			tp = price
		case strings.Contains(t, "STOP"):
			sl = price
		}
	}
	return sl, tp
}

// Order is an open order (Trader.GetOpenOrders).
type Order = types.OpenOrder

func samePrice(a, b float64) bool {
	if a == 0 || b == 0 {
		return a == b
	}
	return math.Abs(a-b) <= 1e-6*math.Max(math.Abs(a), math.Abs(b))
}

// syncProtect gives every copied position the AI's stop loss and take
// profit. Cancelling a coin's stops removes both sides', so a coin whose
// stops changed has all of them placed again.
func (e *Engine) syncProtect(r *runner, tc *tickCtx) {
	states, err := e.store.Positions(tc.link.ID)
	if err != nil {
		r.setError(errText(err))
		return
	}
	bySymbol := map[string][]store.MirrorPosition{}
	for _, st := range states {
		if st.Skip == "" && st.FollowerQty > 0 {
			bySymbol[st.Symbol] = append(bySymbol[st.Symbol], st)
		}
	}
	for symbol, list := range bySymbol {
		raw, err := tc.leader.GetOpenOrders(symbol)
		if err != nil {
			e.failOnce(r, tc.link.ID, symbol, "", "read the AI's stops: "+errText(err))
			continue
		}
		orders := raw
		changed := false
		for i := range list {
			sl, tp := protectOf(orders, list[i].Side)
			if !samePrice(sl, list[i].StopLoss) || !samePrice(tp, list[i].TakeProfit) {
				changed = true
			}
			list[i].StopLoss, list[i].TakeProfit = sl, tp
		}
		if !changed {
			continue
		}
		if err := r.account.CancelStopOrders(symbol); err != nil {
			e.failOnce(r, tc.link.ID, symbol, "", "cancel stops: "+errText(err))
			continue
		}
		for _, st := range list {
			ps := strings.ToUpper(st.Side)
			var notes []string
			if st.StopLoss > 0 {
				if err := r.account.SetStopLoss(symbol, ps, st.FollowerQty, st.StopLoss); err != nil {
					e.failOnce(r, tc.link.ID, symbol, st.Side, "set stop loss: "+errText(err))
					st.StopLoss = 0
				} else {
					notes = append(notes, fmt.Sprintf("SL %g", st.StopLoss))
				}
			}
			if st.TakeProfit > 0 {
				if err := r.account.SetTakeProfit(symbol, ps, st.FollowerQty, st.TakeProfit); err != nil {
					e.failOnce(r, tc.link.ID, symbol, st.Side, "set take profit: "+errText(err))
					st.TakeProfit = 0
				} else {
					notes = append(notes, fmt.Sprintf("TP %g", st.TakeProfit))
				}
			}
			st.LinkID = tc.link.ID
			if err := e.store.SavePosition(&st); err != nil {
				r.setError(errText(err))
				continue
			}
			if len(notes) == 0 {
				notes = append(notes, "AI has none")
			}
			e.event(tc.link.ID, "protect", symbol, st.Side, 0, 0, strings.Join(notes, ", "))
		}
	}
}
