package copytrade

import (
	"context"
	"errors"

	"nofx/store"
)

// IsLimited tells an error that means Binance pushed back (or the client is
// waiting one out), as opposed to a failure of one request.
func IsLimited(err error) bool {
	var limited *limitedError
	return errors.Is(err, ErrPaused) || errors.As(err, &limited)
}

// FetchFills reads a portfolio's fills newest first, back to sinceMs or the
// end of what Binance keeps, at most maxPages pages. It is the one-off read a
// backtest needs; the monitor's own sync (syncFillsPage) also survives
// restarts and keeps a watermark.
//
// reached tells that the rows go back to sinceMs or to the oldest fill
// Binance has. On an error the rows read so far are returned with it: they
// are a whole, shorter history.
func (c *Client) FetchFills(ctx context.Context, id string, sinceMs int64, maxPages int) (rows []Trade, reached bool, err error) {
	for page := 1; page <= maxPages; page++ {
		p, err := c.Trades(ctx, id, page, TradePageSize)
		if err != nil {
			return rows, false, err
		}
		list := p.List
		// Fills that arrived since the previous page pushed this one down:
		// it starts with rows already held.
		for k := min(len(list), len(rows)); k > 0; k-- {
			if sameTrades(list[:k], rows[len(rows)-k:]) {
				list = list[k:]
				break
			}
		}
		rows = append(rows, list...)
		if len(p.List) < TradePageSize || (len(rows) > 0 && rows[len(rows)-1].Time < sinceMs) {
			return rows, true, nil
		}
	}
	return rows, false, nil
}

// StoredFills converts fetched rows (newest first) to stored fills, as the
// monitor stores them: Seq orders fills within one timestamp.
func StoredFills(id string, rows []Trade) []store.CopyTradeFill {
	out := make([]store.CopyTradeFill, len(rows))
	for i, t := range rows {
		out[i] = storedFill(id, i, t)
	}
	return out
}

// ApplyDetail copies a profile into a follow row.
func ApplyDetail(f *store.CopyTradeFollow, d Detail) { applyDetail(f, d) }
