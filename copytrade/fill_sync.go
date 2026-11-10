package copytrade

import (
	"context"
	"strings"
	"time"

	"nofx/store"
)

type fillSync struct {
	stop      int64
	page      int
	rows      []Trade
	candidate *TradePage
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
	page := pending.page + 1
	if pending.candidate != nil {
		page = 1 // validate the page fetched on the previous scheduler turn
	}
	p, err := m.Client.Trades(ctx, f.PortfolioID, page, TradePageSize)
	if err != nil {
		return pending, err
	}
	if pending.page > 0 {
		if pending.candidate == nil {
			next := *pending
			next.candidate = &p
			return &next, nil
		}
		head := pending.rows[:min(TradePageSize, len(pending.rows))]
		stable := len(head) == len(p.List)
		if stable {
			for i := range head {
				if head[i] != p.List[i] {
					stable = false
					break
				}
			}
		}
		if stable {
			p = *pending.candidate
		} else {
			// The new head is already available: restart from it without an
			// extra request or advancing the durable synchronization watermark.
			pending = &fillSync{stop: pending.stop}
		}
	}
	// Commit cursor changes only together with the storage update.
	next := *pending
	next.candidate = nil
	next.page++
	next.rows = append(append([]Trade(nil), pending.rows...), p.List...)
	rows := next.rows
	reachedEnd := len(p.List) < TradePageSize
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
	fills := make([]store.CopyTradeFill, 0, len(rows))
	for i, t := range rows {
		if t.Time < from {
			continue
		}
		fills = append(fills, store.CopyTradeFill{PortfolioID: f.PortfolioID, Time: t.Time, Seq: i,
			Symbol: strings.ToUpper(t.Symbol), Side: strings.ToUpper(t.Side), PositionSide: strings.ToUpper(t.PositionSide),
			Qty: float64(t.Qty), Price: float64(t.Price), Fee: float64(t.Fee), RealizedPnL: float64(t.RealizedPnL)})
	}
	syncedAt := int64(0)
	if done {
		syncedAt = time.Now().UnixMilli()
	}
	if err := m.store.ReplaceFills(f.PortfolioID, from, fills, until, syncedAt); err != nil {
		return pending, err
	}
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
