package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/logger"
	"nofx/store"
	"nofx/trader/binance"
	"time"
)

const (
	// marginOverheadFactor and takerFeeRate approximate the total funds an
	// exchange reserves when opening a position:
	// totalRequired ≈ positionSize/leverage + positionSize*takerFeeRate + positionSize/leverage*1%
	//              = positionSize * (marginOverheadFactor/leverage + takerFeeRate)
	marginOverheadFactor = 1.01
	takerFeeRate         = 0.001

	// positionSizeSafetyFactor leaves a buffer below the maximum affordable
	// position size so a price move between sizing and execution cannot
	// trigger an insufficient-margin rejection.
	positionSizeSafetyFactor = 0.98
)

// executeDecisionWithRecord executes AI decision and records detailed information
func (at *AutoTrader) executeDecisionWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord)
	case "close_long":
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		return at.executeCloseShortWithRecord(decision, actionRecord)
	case "hold":
		if at.usesSignalManagedExit() {
			if err := at.trader.CancelTakeProfitOrders(decision.Symbol); err != nil {
				logger.Infof("  ⚠ Failed to remove fixed take profit for signal-managed hold: %v", err)
			}
		}
		return nil
	case "wait":
		return nil
	default:
		return fmt.Errorf("unknown action: %s", decision.Action)
	}
}

// executeOpenLongWithRecord executes open long position and records detailed information
func (at *AutoTrader) executeOpenLongWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	unlockLifecycle := at.LockPositionLifecycle(decision.Symbol, "long")
	defer unlockLifecycle()
	at.positionActionMu.Lock()
	defer at.positionActionMu.Unlock()
	logger.Infof("  📈 Open long: %s", decision.Symbol)

	// ⚠️ Get current positions for multiple checks
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// [CODE ENFORCED] Check max positions limit
	if err := at.enforceMaxPositions(len(positions)); err != nil {
		return err
	}

	// Check if there's already a position in the same symbol and direction
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
			return fmt.Errorf("❌ %s already has long position, close it first", decision.Symbol)
		}
	}

	marketData, err := at.prepareOpening(decision, positions)
	if err != nil {
		return err
	}
	actualPositionSize := decision.PositionSizeUSD
	actionRecord.StopLoss = decision.StopLoss
	actionRecord.TakeProfit = decision.TakeProfit
	actionRecord.Leverage = decision.Leverage

	// Calculate quantity with adjusted position size
	quantity := actualPositionSize / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// Set margin mode
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Continue execution, doesn't affect trading
	}

	// Open position
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return fmt.Errorf("failed to open long position for %s: %w", decision.Symbol, err)
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	logger.Infof("  ✓ Position opened successfully, order ID: %v, quantity: %.4f", order["orderId"], quantity)

	// Record position opening time
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// Keep a protective stop on the exchange. Vergex positions use the current
	// direction board for ordinary exits, so a fixed take-profit must not close
	// an otherwise unchanged signal.
	if err := at.trader.SetStopLoss(decision.Symbol, "LONG", quantity, decision.StopLoss); err != nil {
		return at.closeUnprotectedPosition(decision.Symbol, "long", quantity, fmt.Errorf("failed to set mandatory stop loss: %w", err))
	}
	if at.usesSignalManagedExit() {
		actionRecord.TakeProfit = 0
		logger.Infof("  ✓ Fixed take profit skipped: Claw402 direction signal manages ordinary exits")
	} else if err := at.trader.SetTakeProfit(decision.Symbol, "LONG", quantity, decision.TakeProfit); err != nil {
		return at.closeUnprotectedPosition(decision.Symbol, "long", quantity, fmt.Errorf("failed to set mandatory take profit: %w", err))
	}

	at.recordAndConfirmOrder(order, decision.Symbol, "open_long", quantity, marketData.CurrentPrice, decision.Leverage, 0)

	if err := at.verifyOpeningFill(decision); err != nil {
		return at.closeUnprotectedPosition(decision.Symbol, "long", quantity, fmt.Errorf("post-fill risk verification: %w", err))
	}

	if at.exchange == binance.PaperExchangeType {
		if err := at.ProfitProtectionOpened(decision.Symbol, "long", decision.StopLoss); err != nil {
			logger.Warnf("Paper profit protection lifecycle initialization failed: %v", err)
		}
	}
	return nil
}

// executeOpenShortWithRecord executes open short position and records detailed information
func (at *AutoTrader) executeOpenShortWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	unlockLifecycle := at.LockPositionLifecycle(decision.Symbol, "short")
	defer unlockLifecycle()
	at.positionActionMu.Lock()
	defer at.positionActionMu.Unlock()
	logger.Infof("  📉 Open short: %s", decision.Symbol)

	// ⚠️ Get current positions for multiple checks
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// [CODE ENFORCED] Check max positions limit
	if err := at.enforceMaxPositions(len(positions)); err != nil {
		return err
	}

	// Check if there's already a position in the same symbol and direction
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
			return fmt.Errorf("❌ %s already has short position, close it first", decision.Symbol)
		}
	}

	marketData, err := at.prepareOpening(decision, positions)
	if err != nil {
		return err
	}
	actualPositionSize := decision.PositionSizeUSD
	actionRecord.StopLoss = decision.StopLoss
	actionRecord.TakeProfit = decision.TakeProfit
	actionRecord.Leverage = decision.Leverage

	// Calculate quantity with adjusted position size
	quantity := actualPositionSize / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// Set margin mode
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Continue execution, doesn't affect trading
	}

	// Open position
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return fmt.Errorf("failed to open short position for %s: %w", decision.Symbol, err)
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	logger.Infof("  ✓ Position opened successfully, order ID: %v, quantity: %.4f", order["orderId"], quantity)

	// Record position opening time
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// Keep a protective stop on the exchange. Vergex positions use the current
	// direction board for ordinary exits, so a fixed take-profit must not close
	// an otherwise unchanged signal.
	if err := at.trader.SetStopLoss(decision.Symbol, "SHORT", quantity, decision.StopLoss); err != nil {
		return at.closeUnprotectedPosition(decision.Symbol, "short", quantity, fmt.Errorf("failed to set mandatory stop loss: %w", err))
	}
	if at.usesSignalManagedExit() {
		actionRecord.TakeProfit = 0
		logger.Infof("  ✓ Fixed take profit skipped: Claw402 direction signal manages ordinary exits")
	} else if err := at.trader.SetTakeProfit(decision.Symbol, "SHORT", quantity, decision.TakeProfit); err != nil {
		return at.closeUnprotectedPosition(decision.Symbol, "short", quantity, fmt.Errorf("failed to set mandatory take profit: %w", err))
	}

	at.recordAndConfirmOrder(order, decision.Symbol, "open_short", quantity, marketData.CurrentPrice, decision.Leverage, 0)

	if err := at.verifyOpeningFill(decision); err != nil {
		return at.closeUnprotectedPosition(decision.Symbol, "short", quantity, fmt.Errorf("post-fill risk verification: %w", err))
	}

	if at.exchange == binance.PaperExchangeType {
		if err := at.ProfitProtectionOpened(decision.Symbol, "short", decision.StopLoss); err != nil {
			logger.Warnf("Paper profit protection lifecycle initialization failed: %v", err)
		}
	}
	return nil
}
