package copytrade

import (
	"context"
	"strings"
	"time"

	"nofx/store"
)

type fillSync struct {
	stop int64
	// rows is the newest-first head of the history fetched so far. After a
	// realignment its length is no longer a multiple of the page size.
	rows      []Trade
	candidate *TradePage
	// written: fills at or after this time are already stored by this sync, so
	// a page only writes what lies below it. 0 = nothing stored yet.
	written int64
	// total is Binance's fill count when rows were last lined up with the
	// head; its growth tells how far a later page was pushed down.
	total int
}

// Publish complete timestamps fetched so far, then yield. Keep the durable
// watermark unchanged until the range is caught up: interrupted backfills can
// safely restart without silently losing history. Offset pages are validated
// against their original head before being committed, because new fills shift
// every later page. Identical legitimate fills must not be deduplicated.
func (m *Monitor) syncFillsPage(ctx context.Context, f *store.CopyTradeFollow, pending *fillSync) (*fillSync, error) {
	if pending == nil {
		pending = &fillSync{stop: f.FillsUntil}
		if pending.stop == 0 {
			pending.stop = time.Now().Add(-backfillWindow).UnixMilli()
		}
	}
	if len(pending.rows) == 0 {
		p, err := m.Client.Trades(ctx, f.PortfolioID, 1, TradePageSize)
		if err != nil {
			return pending, err
		}
		pending.total = p.Total
		return m.commitFills(f, pending, p.List, len(p.List) < TradePageSize)
	}
	if pending.candidate == nil {
		// The page holding the first row not fetched yet; it may start with
		// rows already held, checked when it is committed.
		p, err := m.Client.Trades(ctx, f.PortfolioID, len(pending.rows)/TradePageSize+1, TradePageSize)
		if err != nil {
			return pending, err
		}
		next := *pending
		next.candidate = &p
		return &next, nil
	}
	// Validate the page fetched on the previous scheduler turn.
	p, err := m.Client.Trades(ctx, f.PortfolioID, 1, TradePageSize)
	if err != nil {
		return pending, err
	}
	rows, cand := pending.rows, pending.candidate
	head := rows[:min(TradePageSize, len(rows))]
	shift := 0
	if !sameTrades(head, p.List) {
		if shift = headShift(head, p.List); shift == 0 {
			// The history no longer lines up with what was fetched. The new
			// head is already available: restart from it without an extra
			// request or advancing the durable synchronization watermark.
			return m.commitFills(f, &fillSync{stop: pending.stop, total: p.Total}, p.List, len(p.List) < TradePageSize)
		}
	}
	// before: how many of the new fills were already there when the candidate
	// page was fetched, pushing it that far into rows already held. Only
	// Binance's count tells; without it a shifted candidate is fetched again.
	before, usable := 0, true
	if shift > 0 {
		before = cand.Total - pending.total
		usable = p.Total-pending.total == shift && before >= 0 && before <= shift
		next := &fillSync{stop: pending.stop, written: pending.written, total: p.Total,
			rows: append(append([]Trade(nil), p.List[:shift]...), rows...)}
		if err := m.storeHead(f, next, rows[0].Time); err != nil {
			return pending, err
		}
		pending = next
	}
	overlap := len(rows)%TradePageSize + before
	if usable && len(cand.List) >= overlap && sameTrades(cand.List[:overlap], rows[len(rows)-overlap:]) {
		return m.commitFills(f, pending, cand.List[overlap:], len(cand.List) < TradePageSize)
	}
	if shift > 0 {
		return pending, nil // lined up again: fetch the shifted page next turn
	}
	return m.commitFills(f, &fillSync{stop: pending.stop, total: p.Total}, p.List, len(p.List) < TradePageSize)
}

// storeHead stores the fills that arrived in front of a running backfill.
// Every index moved, so each timestamp from the old head on is written again
// as a whole: Seq orders fills only within one timestamp.
func (m *Monitor) storeHead(f *store.CopyTradeFollow, s *fillSync, oldHead int64) error {
	from := max(oldHead, s.written)
	var fills []store.CopyTradeFill
	for i, t := range s.rows {
		if t.Time < from {
			break
		}
		fills = append(fills, storedFill(f.PortfolioID, i, t))
	}
	if err := m.store.ReplaceFills(f.PortfolioID, from, fills, f.FillsUntil); err != nil {
		return err
	}
	f.LastTradeTime = max(f.LastTradeTime, s.rows[0].Time)
	f.FillsPending = true
	return nil
}

func storedFill(id string, seq int, t Trade) store.CopyTradeFill {
	return store.CopyTradeFill{PortfolioID: id, Time: t.Time, Seq: seq,
		Symbol: strings.ToUpper(t.Symbol), Side: strings.ToUpper(t.Side), PositionSide: strings.ToUpper(t.PositionSide),
		Qty: float64(t.Qty), Price: float64(t.Price), Fee: float64(t.Fee), RealizedPnL: float64(t.RealizedPnL)}
}

func sameTrades(a, b []Trade) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// headShift tells how many new fills sit in front of the old head in a newly
// fetched first page, or 0 when the old head is not found in it.
func headShift(head, list []Trade) int {
	for k := 1; k < len(list); k++ {
		n := min(len(list)-k, len(head))
		if sameTrades(list[k:k+n], head[:n]) {
			return k
		}
	}
	return 0
}

// commitFills appends add to the fetched rows and stores the complete
// timestamps not stored yet. Cursor changes are committed only together with
// the storage update.
func (m *Monitor) commitFills(f *store.CopyTradeFollow, pending *fillSync, add []Trade, reachedEnd bool) (*fillSync, error) {
	next := *pending
	next.candidate = nil
	next.rows = append(append([]Trade(nil), pending.rows...), add...)
	rows := next.rows
	done := reachedEnd
	if len(rows) > 0 && rows[len(rows)-1].Time < next.stop {
		done = true
	}
	from, until := next.stop, f.FillsUntil
	if len(rows) > 0 {
		oldest := rows[len(rows)-1].Time
		if !reachedEnd && oldest >= next.stop {
			// Defer a timestamp split between pages until it is complete.
			from = oldest + 1
		}
	}
	if done {
		until = next.stop
		if len(rows) > 0 {
			until = max(until, rows[0].Time)
		}
	}
	fills := make([]store.CopyTradeFill, 0, len(add))
	for i, t := range rows {
		if t.Time < from || (pending.written > 0 && t.Time >= pending.written) {
			continue
		}
		fills = append(fills, storedFill(f.PortfolioID, i, t))
	}
	syncedAt := int64(0)
	if done {
		syncedAt = time.Now().UnixMilli()
	}
	if err := m.store.ReplaceFillRange(f.PortfolioID, from, pending.written, fills, until, syncedAt); err != nil {
		return pending, err
	}
	next.written = from
	if len(rows) > 0 {
		f.LastTradeTime = max(f.LastTradeTime, rows[0].Time)
	}
	f.FillsUntil = until
	f.FillsPending = !done
	if done {
		f.FillsSyncedAt = syncedAt
		return nil, nil
	}
	return &next, nil
}
