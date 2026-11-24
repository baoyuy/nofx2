package trader

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nofx/logger"
	"nofx/market"
	"nofx/store"
)

// Paper-account position reconciliation.
//
// Exchanges with an order-sync loop (Hyperliquid, Lighter, ...) reconcile local
// OPEN position rows against the live book. Paper accounts had no such loop, so
// a position the paper venue closed without a matching local close (the
// emergency close after a failed post-fill check is the observed case: decisions
// 2809 and 2925 left UNI and ZEC rows OPEN although the account held nothing)
// stayed OPEN in the database forever and showed up on the dashboard.
//
// The paper venue's own state is the truth here; it lives in the same process,
// so reading it is free and cannot fail on the network.

const paperReconcileInterval = time.Minute

// liveQtyFromPositions builds the LivePositionKey -> quantity map that
// ReconcileOpenPositionsWithLive expects.
//
// positionAmt follows the Binance hedge-mode sign convention: a short carries a
// NEGATIVE amount. Quantities are taken as absolute values; skipping
// non-positive amounts (as a long-only reading would) would mark every live
// short as "not held" and close its row.
func liveQtyFromPositions(positions []map[string]interface{}) (map[string]float64, error) {
	live := make(map[string]float64, len(positions))
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		qty, ok := pos["positionAmt"].(float64)
		if !ok || math.IsNaN(qty) || math.IsInf(qty, 0) {
			return nil, fmt.Errorf("position %s has no readable quantity", symbol)
		}
		side = strings.ToUpper(strings.TrimSpace(side))
		if symbol == "" || (side != "LONG" && side != "SHORT") {
			return nil, fmt.Errorf("position %q has unknown side %q", symbol, side)
		}
		qty = math.Abs(qty)
		if qty == 0 {
			continue
		}
		live[store.LivePositionKey(market.Normalize(symbol), side)] += qty
	}
	return live, nil
}

// reconcilePaperPositions closes local OPEN rows on this paper account that the
// paper venue no longer holds, and trims rows larger than what it holds.
//
// A failed or unreadable read of the venue reconciles nothing: an empty map
// would mean "the account holds nothing" and close every row.
func (at *AutoTrader) reconcilePaperPositions() error {
	if at.store == nil || at.exchangeID == "" {
		return nil
	}
	positions, err := getFreshPositions(at.trader)
	if err != nil {
		return fmt.Errorf("read paper positions: %w", err)
	}
	live, err := liveQtyFromPositions(positions)
	if err != nil {
		return err
	}
	closed, err := at.store.Position().ReconcileOpenPositionsWithLive(at.exchangeID, live)
	if err != nil {
		return err
	}
	if closed > 0 {
		at.logInfof("🧹 Paper reconcile: closed %d position row(s) the paper account no longer holds", closed)
	}
	return nil
}

// startPaperReconcile runs reconciliation once at start, then every minute,
// until the trader stops.
func (at *AutoTrader) startPaperReconcile(stop <-chan struct{}) {
	run := func() {
		if err := at.reconcilePaperPositions(); err != nil {
			logger.Infof("⚠️ Paper position reconcile skipped: %v", err)
		}
	}
	run()
	go func() {
		t := time.NewTicker(paperReconcileInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				run()
			}
		}
	}()
}
