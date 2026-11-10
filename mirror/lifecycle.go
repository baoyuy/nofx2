package mirror

import (
	"context"
	"fmt"
	"strings"

	"nofx/store"
)

// closeAll closes every copy of a link on the follower, as far as it can.
func (e *Engine) closeAll(link store.MirrorLink, acc Account) []string {
	states, err := e.store.Positions(link.ID)
	if err != nil {
		return []string{errText(err)}
	}
	follower := map[string]Position{}
	if raw, err := acc.GetPositions(); err == nil {
		follower = parsePositions(raw)
	}
	var failed []string
	for _, st := range states {
		f, held := follower[key(st.Symbol, st.Side)]
		if st.Skip != "" || st.FollowerQty <= 0 || !held {
			continue
		}
		qty := 0.0
		if f.Qty > st.FollowerQty*(1+dustShare) {
			qty = st.FollowerQty
		}
		if st.Side == "long" {
			_, err = acc.CloseLong(st.Symbol, qty)
		} else {
			_, err = acc.CloseShort(st.Symbol, qty)
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s %s: %s", st.Symbol, st.Side, errText(err)))
			e.event(link.ID, "error", st.Symbol, st.Side, 0, 0, "close failed: "+errText(err))
			continue
		}
		_ = acc.CancelStopOrders(st.Symbol)
		if err := e.store.DeletePosition(link.ID, st.Symbol, st.Side); err != nil {
			failed = append(failed, errText(err))
		}
		e.event(link.ID, "close", st.Symbol, st.Side, f.Qty, f.Mark, "link stopped")
	}
	return failed
}

// stopped saves a link as stopped.
func (e *Engine) stopped(link *store.MirrorLink, reason, detail string) {
	link.Running, link.StopReason = false, reason
	if err := e.store.SaveLink(link); err != nil {
		e.event(link.ID, "error", "", "", 0, 0, "save stop: "+errText(err))
	}
	e.event(link.ID, "stop", "", "", 0, 0, strings.TrimSpace(reason+" "+detail))
}

// StartLink starts a saved link. The follower's equity now is where its
// loss cap counts from; AI positions already open are not copied.
func (e *Engine) StartLink(ctx context.Context, link store.MirrorLink) error {
	if why := e.deps.Busy(link); why != "" {
		return fmt.Errorf("%s", why)
	}
	acc, err := e.deps.Follower(link.UserID, link.ExchangeID)
	if err != nil {
		return fmt.Errorf("follower account: %w", err)
	}
	if _, err := e.deps.Leader(link.LeaderID); err != nil {
		return fmt.Errorf("AI trader: %w", err)
	}
	b, err := acc.GetBalance()
	if err != nil {
		return fmt.Errorf("follower balance: %w", err)
	}
	eq, _ := equityOf(b)
	link.Running, link.StopReason, link.StartEquity = true, "", eq
	if err := e.store.SaveLink(&link); err != nil {
		return err
	}
	e.event(link.ID, "start", "", "", 0, 0, fmt.Sprintf("equity %.2f USDT, %s", eq, describe(link)))
	e.run(ctx, link.ID)
	return nil
}

// StopLink stops a link, closing its copies first when asked. It returns
// the copies that failed to close.
func (e *Engine) StopLink(userID, id string, closePositions bool) ([]string, error) {
	e.halt(id)
	link, err := e.store.Link(userID, id)
	if err != nil {
		return nil, err
	}
	var failed []string
	if closePositions {
		acc, err := e.deps.Follower(link.UserID, link.ExchangeID)
		if err != nil {
			return nil, fmt.Errorf("follower account: %w", err)
		}
		failed = e.closeAll(link, acc)
	}
	if link.Running {
		e.stopped(&link, "manual", "")
	}
	return failed, nil
}

// Running tells whether a link's goroutine runs.
func (e *Engine) Running(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runners[id] != nil
}
