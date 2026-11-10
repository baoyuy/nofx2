package mirror

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"

	"nofx/store"
	"nofx/trader/binance"
)

// tickCtx is what one tick knows of both accounts.
type tickCtx struct {
	link     store.MirrorLink
	leader   Account
	follower map[string]Position
	// available is the follower's free margin, lowered by each open.
	available      float64
	followerEquity float64
	leaderEquity   float64 // read on the first ratio open; -1 = failed
	now            int64
	// touched symbols had an order: their stops are compared again.
	touched map[string]bool
}

// tick runs one pass of a link. It returns true when the link stopped.
func (e *Engine) tick(ctx context.Context, r *runner) bool {
	link, err := e.store.LinkByID(r.link)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	if err != nil {
		r.setError(errText(err))
		return false
	}
	if !link.Running {
		return true
	}
	if why := e.deps.Busy(link); why != "" {
		e.stopped(&link, "busy", why)
		return true
	}
	if r.account == nil {
		if r.account, err = e.deps.Follower(link.UserID, link.ExchangeID); err != nil {
			r.setError("follower account: " + errText(err))
			return false
		}
	}
	leader, err := e.deps.Leader(link.LeaderID)
	if err != nil {
		r.setError("AI trader: " + errText(err))
		return false
	}
	invalidate(leader)
	invalidate(r.account)
	// A paper follower queues its stop fills for an AI trader to record; no
	// AI trader runs on it, and the link reads the result from positions.
	if p, ok := r.account.(interface {
		DrainCloseEvents() []binance.PaperCloseEvent
	}); ok {
		p.DrainCloseEvents()
	}
	rawLeader, err := leader.GetPositions()
	if err != nil {
		r.setError("AI positions: " + errText(err))
		return false
	}
	rawFollower, err := r.account.GetPositions()
	if err != nil {
		r.setError("follower positions: " + errText(err))
		return false
	}
	balance, err := r.account.GetBalance()
	if err != nil {
		r.setError("follower balance: " + errText(err))
		return false
	}
	equity, available := equityOf(balance)
	if link.StartEquity <= 0 && equity > 0 {
		link.StartEquity = equity
		if err := e.store.SaveLink(&link); err != nil {
			r.setError(errText(err))
			return false
		}
	}
	pnl := equity - link.StartEquity
	follower := parsePositions(rawFollower)
	if link.MaxLoss > 0 && link.StartEquity > 0 && pnl <= -link.MaxLoss {
		e.closeAll(link, r.account)
		e.stopped(&link, "max_loss", fmt.Sprintf("lost %.2f USDT, cap %.2f", -pnl, link.MaxLoss))
		return true
	}
	states, err := e.store.Positions(link.ID)
	if err != nil {
		r.setError(errText(err))
		return false
	}
	tc := &tickCtx{link: link, leader: leader, follower: follower, available: available,
		followerEquity: equity, now: time.Now().UnixMilli(), touched: map[string]bool{}}
	for _, a := range plan(parsePositions(rawLeader), follower, states, r.first, tc.now) {
		if ctx.Err() != nil {
			return false
		}
		e.exec(r, tc, a)
	}
	r.first = false
	if link.FollowProtect && (len(tc.touched) > 0 || time.Since(r.protectAt) >= protectInterval) {
		e.syncProtect(r, tc)
		r.protectAt = time.Now()
	}
	r.mu.Lock()
	r.status = Status{Equity: equity, Available: available, PnL: pnl, UpdatedAt: time.Now(),
		Positions: holdings(follower, states)}
	r.mu.Unlock()
	return false
}

// holdings lists the follower positions the link copied.
func holdings(follower map[string]Position, states []store.MirrorPosition) []FollowerHolding {
	out := []FollowerHolding{}
	for _, st := range states {
		f, ok := follower[key(st.Symbol, st.Side)]
		if !ok || st.Skip != "" {
			continue
		}
		out = append(out, FollowerHolding{Symbol: f.Symbol, Side: f.Side, Qty: f.Qty, Entry: f.Entry,
			Mark: f.Mark, Leverage: f.Leverage, PnL: f.PnL})
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i].Symbol, out[i].Side) < key(out[j].Symbol, out[j].Side) })
	return out
}

// marginBuffer keeps room for fees and the price moving before the fill.
const marginBuffer = 0.95

func (e *Engine) exec(r *runner, tc *tickCtx, a Action) {
	id := tc.link.ID
	save := func(st store.MirrorPosition) bool {
		st.LinkID = id
		if err := e.store.SavePosition(&st); err != nil {
			r.setError(errText(err))
			return false
		}
		return true
	}
	switch a.Kind {
	case ActTrack:
		if a.State.Skip == SkipBeforeStart && a.State.OpenedAt == tc.now {
			e.event(id, "skip", a.Symbol, a.Side, a.Leader.Qty, a.Leader.Mark, "open before the link started")
		}
		save(a.State)
	case ActForget:
		if err := e.store.DeletePosition(id, a.Symbol, a.Side); err != nil {
			r.setError(errText(err))
		}
	case ActGone:
		if save(a.State) {
			e.event(id, "follower_closed", a.Symbol, a.Side, 0, 0, "the copy was closed outside the link; not reopened")
		}
	case ActOpen:
		e.open(r, tc, a, save)
	case ActAdd:
		e.add(r, tc, a, save)
	case ActReduce, ActClose:
		e.reduce(r, tc, a, save)
	}
}

// openPrice is the price to size an order at.
func openPrice(acc Account, l Position) float64 {
	if p, err := acc.GetMarketPrice(l.Symbol); err == nil && p > 0 {
		return p
	}
	return l.Mark
}

func (e *Engine) placeOpen(acc Account, symbol, side string, qty float64, lev int) (float64, error) {
	if s, err := acc.FormatQuantity(symbol, qty); err == nil {
		fmt.Sscan(s, &qty)
	}
	if qty <= 0 {
		return 0, fmt.Errorf("quantity rounds to 0")
	}
	if err := acc.SetMarginMode(symbol, true); err != nil {
		// Already set, or an open position keeps the mode; the order decides.
		_ = err
	}
	var err error
	if side == "long" {
		_, err = acc.OpenLong(symbol, qty, lev)
	} else {
		_, err = acc.OpenShort(symbol, qty, lev)
	}
	return qty, err
}

func (e *Engine) open(r *runner, tc *tickCtx, a Action, save func(store.MirrorPosition) bool) {
	st := a.State
	skip := func(why string) {
		st.Skip = SkipOpenFailed
		if save(st) {
			e.event(tc.link.ID, "skip", a.Symbol, a.Side, a.Leader.Qty, a.Leader.Mark, why)
		}
	}
	if tc.link.SizeMode == "ratio" && tc.leaderEquity == 0 {
		tc.leaderEquity = -1
		if b, err := tc.leader.GetBalance(); err == nil {
			if eq, _ := equityOf(b); eq > 0 {
				tc.leaderEquity = eq
			}
		}
	}
	lev := leverageFor(tc.link, a.Leader)
	margin, err := marginFor(tc.link, a.Leader, tc.leaderEquity, tc.followerEquity)
	if err != nil {
		skip(err.Error())
		return
	}
	if free := tc.available * marginBuffer; margin > free {
		if free <= 0 {
			skip(fmt.Sprintf("no free margin (need %.2f USDT)", margin))
			return
		}
		margin = free
	}
	price := openPrice(r.account, a.Leader)
	if price <= 0 {
		skip("no price")
		return
	}
	qty, err := e.placeOpen(r.account, a.Symbol, a.Side, margin*float64(lev)/price, lev)
	if err != nil {
		skip(errText(err))
		return
	}
	st.FollowerQty, st.LeaderQty, st.OrderAt = qty, a.Leader.Qty, tc.now
	st.Skip, st.StopLoss, st.TakeProfit = "", 0, 0
	if save(st) {
		tc.available -= margin
		tc.touched[a.Symbol] = true
		e.event(tc.link.ID, "open", a.Symbol, a.Side, qty, price,
			fmt.Sprintf("margin %.2f USDT, %dx (%s)", margin, lev, describe(tc.link)))
	}
}

func (e *Engine) add(r *runner, tc *tickCtx, a Action, save func(store.MirrorPosition) bool) {
	st := a.State
	f := tc.follower[key(a.Symbol, a.Side)]
	lev := max(f.Leverage, 1)
	price := openPrice(r.account, a.Leader)
	qty := a.Qty
	if tc.link.MaxMargin > 0 && price > 0 {
		room := tc.link.MaxMargin*float64(lev)/price - st.FollowerQty
		qty = math.Min(qty, room)
	}
	if price > 0 {
		qty = math.Min(qty, tc.available*marginBuffer*float64(lev)/price)
	}
	// The AI's quantity is the new baseline whether or not the add is copied.
	st.LeaderQty = a.Leader.Qty
	if qty <= 0 || price <= 0 {
		if save(st) {
			e.event(tc.link.ID, "skip", a.Symbol, a.Side, a.Qty, price, "add not copied: margin cap or no free margin")
		}
		return
	}
	got, err := e.placeOpen(r.account, a.Symbol, a.Side, qty, lev)
	if err != nil {
		if save(st) {
			e.event(tc.link.ID, "skip", a.Symbol, a.Side, qty, price, "add not copied: "+errText(err))
		}
		return
	}
	st.FollowerQty += got
	st.OrderAt, st.StopLoss, st.TakeProfit = tc.now, 0, 0
	if save(st) {
		tc.available -= got * price / float64(lev)
		tc.touched[a.Symbol] = true
		e.event(tc.link.ID, "add", a.Symbol, a.Side, got, price, fmt.Sprintf("AI added %.0f%%", a.Scale*100))
	}
}

func (e *Engine) reduce(r *runner, tc *tickCtx, a Action, save func(store.MirrorPosition) bool) {
	st := a.State
	f := tc.follower[key(a.Symbol, a.Side)]
	qty := a.Qty
	if a.Kind == ActClose && f.Qty > st.FollowerQty*(1+dustShare) {
		// The user holds more than the copy: close only the copy.
		qty = st.FollowerQty
	}
	var err error
	if a.Side == "long" {
		_, err = r.account.CloseLong(a.Symbol, qty)
	} else {
		_, err = r.account.CloseShort(a.Symbol, qty)
	}
	if err != nil {
		// Closes are retried every tick until they go through.
		e.failOnce(r, tc.link.ID, a.Symbol, a.Side, "close failed, retrying: "+errText(err))
		return
	}
	tc.touched[a.Symbol] = true
	if a.Kind == ActClose {
		if err := e.store.DeletePosition(tc.link.ID, a.Symbol, a.Side); err != nil {
			r.setError(errText(err))
		}
		closed := qty
		if closed == 0 {
			closed = f.Qty
		}
		e.event(tc.link.ID, "close", a.Symbol, a.Side, closed, f.Mark, "AI closed")
		if tc.link.FollowProtect && !e.copiedOn(tc.link.ID, a.Symbol) {
			// Stops left on a coin with no copy would fire on a later position.
			_ = r.account.CancelStopOrders(a.Symbol)
		}
		return
	}
	st.FollowerQty -= qty
	st.LeaderQty, st.OrderAt = a.Leader.Qty, tc.now
	if save(st) {
		e.event(tc.link.ID, "reduce", a.Symbol, a.Side, qty, f.Mark, fmt.Sprintf("AI reduced %.0f%%", a.Scale*100))
	}
}

// copiedOn tells whether a link still copies a side of symbol.
func (e *Engine) copiedOn(linkID, symbol string) bool {
	states, err := e.store.Positions(linkID)
	if err != nil {
		return true
	}
	for _, st := range states {
		if st.Symbol == symbol && st.Skip == "" && st.FollowerQty > 0 {
			return true
		}
	}
	return false
}
