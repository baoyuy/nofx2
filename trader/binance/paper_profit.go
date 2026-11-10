package binance

import (
	"fmt"
	"math"
	"time"
)

// PaperProfitPosition is a local, immutable identity/stop snapshot. No quote
// request is made; the one-minute risk monitor supplies its existing mark.
type PaperProfitPosition struct {
	Symbol, Side                   string
	OpenedAt                       time.Time
	EntryPrice, Quantity, StopLoss float64
	Leverage                       int
}

func (p *PaperTrader) ProfitPosition(symbol, side string) (PaperProfitPosition, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pos := p.state.Positions[paperKey(symbol, side)]
	if pos == nil {
		return PaperProfitPosition{}, false
	}
	return PaperProfitPosition{pos.Symbol, pos.Side, pos.OpenedAt, pos.EntryPrice, pos.Quantity, pos.StopLoss, pos.Leverage}, true
}

// PaperBreakevenStop covers the actual simulator entry/exit taker fees and exit
// slippage. EntryPrice already includes entry slippage. Positive lockPct is an
// additional gross price buffer, not guaranteed net profit (gaps can overrun it).
func PaperBreakevenStop(entry float64, side string, lockPct float64) float64 {
	if side == "short" {
		return entry * (1 - paperTakerFeeRate - lockPct/100) / ((1 + paperSlippageRate) * (1 + paperTakerFeeRate))
	}
	return entry * (1 + paperTakerFeeRate + lockPct/100) / ((1 - paperSlippageRate) * (1 - paperTakerFeeRate))
}

// ApplyProfitProtection is the atomic, only-tightening variant of SetStopLoss.
// Identity revalidation, stop update and any already-crossed-floor close share
// the same simulator mutex as AI/manual closes and native stop triggers. A stale
// monitor can never close a reopened same-symbol position or submit two fills.
// Simulator close events use the existing durable close-recording pipeline.
func (p *PaperTrader) ApplyProfitProtection(expected PaperProfitPosition, mark, stop float64, closeNow bool) (actualStop float64, closed bool, err error) {
	if mark <= 0 || math.IsNaN(mark) || math.IsInf(mark, 0) || math.IsNaN(stop) || math.IsInf(stop, 0) {
		return 0, false, fmt.Errorf("invalid profit-protection price")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pos := p.state.Positions[paperKey(expected.Symbol, expected.Side)]
	if pos == nil || !pos.OpenedAt.Equal(expected.OpenedAt) || pos.EntryPrice != expected.EntryPrice || pos.Quantity != expected.Quantity {
		return 0, false, fmt.Errorf("paper profit-protection lifecycle changed")
	}
	actualStop = pos.StopLoss
	if stop > 0 && (actualStop == 0 || (pos.Side == "long" && stop > actualStop) || (pos.Side == "short" && stop < actualStop)) {
		actualStop = stop
	}
	crossed := actualStop > 0 && ((pos.Side == "long" && mark <= actualStop) || (pos.Side == "short" && mark >= actualStop))
	if closeNow || crossed {
		fill := mark * (1 - paperSlippageRate)
		if pos.Side == "short" {
			fill = mark * (1 + paperSlippageRate)
		}
		_, event := p.closeLocked(pos.Symbol, pos.Side, 0, fill, "profit_protection")
		p.state.PendingCloses = append(p.state.PendingCloses, event)
		if n := len(p.state.PendingCloses); n > paperMaxPendingCloses {
			p.state.PendingCloses = p.state.PendingCloses[n-paperMaxPendingCloses:]
		}
		return actualStop, true, p.saveLocked()
	}
	if actualStop == pos.StopLoss {
		return actualStop, false, nil
	}
	old := pos.StopLoss
	pos.StopLoss = actualStop
	if err := p.saveLocked(); err != nil {
		pos.StopLoss = old
		return old, false, err
	}
	return actualStop, false, nil
}
