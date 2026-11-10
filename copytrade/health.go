package copytrade

import (
	"context"
	"math"
	"time"

	"nofx/logger"
)

// Health tells whether a held position still says something about the
// trader's view: a deep loss may just be a trader waiting to break even, and
// an inferred position may have been liquidated without a fill we can tell
// apart (Binance's trade history has no liquidation flag).
type Health struct {
	// ReturnPct is the unrealized result in %: of margin when Margin is known,
	// else of the entry price. Valid only when Priced.
	ReturnPct   float64 `json:"return_pct"`
	PctOfMargin bool    `json:"pct_of_margin"`
	Priced      bool    `json:"priced"`
	// Trapped uses exactly ReturnPct's displayed basis, never a past entry.
	Trapped             bool    `json:"trapped"`
	TrappedThresholdPct float64 `json:"trapped_threshold_pct"`
	// Historical entry deviation is informational, not current PnL or Trapped.
	HistoricalEntryWarning bool    `json:"historical_entry_warning"`
	FirstEntryReturnPct    float64 `json:"first_entry_return_pct"`
	FirstEntryPctOfMargin  bool    `json:"first_entry_pct_of_margin"`
	FirstEntryThresholdPct float64 `json:"first_entry_threshold_pct"`
	// Liquidated: inferred position whose loss, with the trader's other
	// inferred losses, reached the trader's whole balance. Excluded from
	// counts and confidence.
	Liquidated bool `json:"liquidated"`
	// Fresh: opened or added to within freshWindow.
	Fresh bool `json:"fresh"`
}

const (
	// At 20x a 1% move is already 20% of margin, so a lower bar flags
	// ordinary noise (40% of positions on the first day at 20%/2%).
	trappedMarginPct = 50.0
	trappedMovePct   = trappedMarginPct / assumedLeverage
	// assumedLeverage turns a price move into a margin loss when leverage is
	// unknown (inferred positions).
	assumedLeverage = 10.0
	freshWindow     = 24 * time.Hour
	freshBoost      = 1.3
	markInterval    = 10 * time.Second
)

// MarksFunc returns mark prices by symbol.
type MarksFunc func(ctx context.Context) (map[string]float64, error)

// marginLossPct is the loss as % of margin (positive = losing).
func (h Health) marginLossPct() float64 {
	if !h.Priced || h.ReturnPct >= 0 {
		return 0
	}
	if h.PctOfMargin {
		return -h.ReturnPct
	}
	return -h.ReturnPct * assumedLeverage
}

// healthFactor scales a position's vote: a losing position counts less
// (0.55 at 50% of margin lost, 0.1 at 100%), a liquidated one not at all,
// and a fresh open or add counts more unless the current position is trapped.
func healthFactor(p Position) float64 {
	if p.Liquidated {
		return 0
	}
	f := 1 - 0.9*math.Min(100, p.marginLossPct())/100
	if p.Fresh && !p.Trapped {
		f *= freshBoost
	}
	return f
}

// assessHistoricalEntry records adverse movement from the first observed fill
// only when that entry is worse than the current average. Applying today's
// leverage is a reference estimate, not the actual PnL/margin of that old fill.
func (p *Position) assessHistoricalEntry() {
	if p.FirstEntry <= 0 || p.MarkPrice <= 0 || p.EntryPrice <= 0 {
		return
	}
	dir := 1.0
	if p.Side == "short" {
		dir = -1
	}
	if dir*(p.FirstEntry-p.EntryPrice) <= 0 {
		return // no adverse historical entry beyond the current average
	}
	p.FirstEntryReturnPct = dir * (p.MarkPrice - p.FirstEntry) / p.FirstEntry * 100
	p.FirstEntryThresholdPct = trappedMovePct
	if p.Leverage > 0 {
		p.FirstEntryReturnPct *= p.Leverage
		p.FirstEntryPctOfMargin = true
		p.FirstEntryThresholdPct = trappedMarginPct
	}
	p.HistoricalEntryWarning = p.FirstEntryReturnPct <= -p.FirstEntryThresholdPct
}

// assessHealth prices positions at marks and flags trapped, fresh and likely
// liquidated ones. It returns a new slice.
func assessHealth(positions []Position, marks map[string]float64, balance float64, now time.Time) []Position {
	out := make([]Position, len(positions))
	var inferredLoss float64
	for i, p := range positions {
		p.Health = Health{}
		dir := 1.0
		if p.Side == "short" {
			dir = -1
		}
		if p.Source != "public" {
			if m := marks[p.Symbol]; m > 0 {
				p.MarkPrice = m
				p.UnrealizedPnL = dir * (m - p.EntryPrice) * p.Qty
			} else {
				p.MarkPrice, p.UnrealizedPnL = 0, 0
			}
		}
		if p.MarkPrice > 0 && p.EntryPrice > 0 {
			p.Priced = true
			if p.Margin > 0 {
				p.ReturnPct, p.PctOfMargin = p.UnrealizedPnL/p.Margin*100, true
			} else {
				p.ReturnPct = dir * (p.MarkPrice - p.EntryPrice) / p.EntryPrice * 100
			}
		}
		if p.Priced {
			p.TrappedThresholdPct = trappedMovePct
			if p.PctOfMargin {
				p.TrappedThresholdPct = trappedMarginPct
			}
			p.Trapped = p.ReturnPct <= -p.TrappedThresholdPct
			p.assessHistoricalEntry()
		}
		last := p.LastAddAt
		if last == 0 {
			last = p.OpenedAt
		}
		p.Fresh = last > 0 && now.Sub(time.UnixMilli(last)) < freshWindow
		if p.Source != "public" && p.UnrealizedPnL < 0 {
			inferredLoss -= p.UnrealizedPnL
		}
		out[i] = p
	}
	// A loss that ate the whole balance would have liquidated a cross-margin
	// account. Isolated positions can go earlier; we cannot see those.
	if balance > 0 && inferredLoss >= balance {
		for i := range out {
			if out[i].Source != "public" && out[i].UnrealizedPnL < 0 {
				out[i].Liquidated, out[i].Trapped = true, false
			}
		}
	}
	return out
}

// markLoop keeps mark prices for held coins every ten seconds. This uses the
// separate futures market API, not the copy-trading web request budget.
func (m *Monitor) markLoop(ctx context.Context) {
	if m.Marks == nil {
		return
	}
	t := time.NewTicker(markInterval)
	defer t.Stop()
	for {
		if m.holdsAny() {
			marks, err := m.Marks(ctx)
			if err != nil {
				logger.Warnf("copy-trade: mark prices: %v", err)
			} else {
				m.markMu.Lock()
				m.marks = marks
				m.markMu.Unlock()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Monitor) holdsAny() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.snaps {
		if len(s.Positions) > 0 {
			return true
		}
	}
	return false
}

func (m *Monitor) currentMarks() map[string]float64 {
	m.markMu.RLock()
	defer m.markMu.RUnlock()
	return m.marks
}
