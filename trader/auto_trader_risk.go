package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strings"
	"time"
)

const (
	// The monitor arms only once the underlying PRICE has moved +5% in the
	// position's favor (leverage-independent — at 10x the old margin-basis
	// check armed at a +0.5% price wiggle and strangled every winner), then
	// closes if the position gives back 40% of its peak profit.
	drawdownClosePriceGainPct = 5.0
	drawdownCloseGivebackPct  = 40.0
	// Keep total planned margin below 100% so every configured slot retains
	// room for exchange overhead and fees. This is an allocation target, not
	// the per-position hard cap in RiskControlConfig.
	autopilotBookMarginBudget = 0.96
)

// shouldDrawdownClose arms from the lifecycle's historical PRICE peak, not
// current profit. Once >5% was reached, falling back below 5% cannot disarm it.
func shouldDrawdownClose(peakPricePnLPct, drawdownPct float64) bool {
	return peakPricePnLPct > drawdownClosePriceGainPct && drawdownPct >= drawdownCloseGivebackPct
}

// startDrawdownMonitor starts drawdown monitoring
func (at *AutoTrader) startDrawdownMonitor() {
	if _, err := at.store.ProfitState(); err != nil {
		logger.Errorf("Cannot start trusted profit monitoring: %v", err)
		return
	}
	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()

		ticker := time.NewTicker(1 * time.Minute) // Check every minute
		defer ticker.Stop()

		logger.Info("📊 Started position drawdown monitoring (check every minute)")

		for {
			select {
			case <-ticker.C:
				at.checkPositionDrawdown()
			case <-at.stopMonitorCh:
				logger.Info("⏹ Stopped position drawdown monitoring")
				return
			}
		}
	}()
}

// checkPositionDrawdown reuses the one-minute position/quote snapshot. Optional
// protection never adds a second market feed or a faster polling loop.
func (at *AutoTrader) checkPositionDrawdown() {
	snapshotStarted := time.Now().UnixMilli()
	positions, err := at.trader.GetPositions()
	if err != nil {
		logger.Infof("❌ Drawdown monitoring: failed to get positions: %v", err)
		return
	}
	active := make(map[string]bool)
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		quantity, _ := pos["positionAmt"].(float64)
		if quantity == 0 {
			continue
		}
		active[symbol+"_"+strings.ToLower(side)] = true
		if err := at.protectPositionProfit(pos); err != nil {
			logger.Warnf("Profit monitoring %s %s: %v", symbol, side, err)
		}
	}
	if ps, err := at.store.ProfitState(); err == nil {
		if err := ps.Prune(at.id, active, snapshotStarted); err != nil {
			logger.Warnf("Profit state cleanup failed: %v", err)
		}
	}
	at.peakPnLCacheMutex.Lock()
	for key := range at.peakPnLCache {
		if !active[key] {
			delete(at.peakPnLCache, key)
		}
	}
	at.peakPnLCacheMutex.Unlock()
}

// emergencyClosePosition emergency close position function. The close is
// recorded like an AI close so position history and the re-entry cooldown
// see it.
func (at *AutoTrader) emergencyClosePosition(symbol, side string, quantity, markPrice, entryPrice float64) error {
	switch side {
	case "long":
		order, err := at.trader.CloseLong(symbol, 0) // 0 = close all
		if err != nil {
			return err
		}
		logger.Infof("✅ Emergency close long position succeeded, order ID: %v", order["orderId"])
		at.recordAndConfirmOrder(order, symbol, "close_long", quantity, markPrice, 0, entryPrice)
	case "short":
		order, err := at.trader.CloseShort(symbol, 0) // 0 = close all
		if err != nil {
			return err
		}
		logger.Infof("✅ Emergency close short position succeeded, order ID: %v", order["orderId"])
		at.recordAndConfirmOrder(order, symbol, "close_short", quantity, markPrice, 0, entryPrice)
	default:
		return fmt.Errorf("unknown position direction: %s", side)
	}

	return nil
}

// GetPeakPnLCache gets peak profit cache
func (at *AutoTrader) GetPeakPnLCache() map[string]float64 {
	at.peakPnLCacheMutex.RLock()
	defer at.peakPnLCacheMutex.RUnlock()

	// Return a copy of the cache
	cache := make(map[string]float64)
	for k, v := range at.peakPnLCache {
		cache[k] = v
	}
	return cache
}

// UpdatePeakPnL updates peak profit cache
func (at *AutoTrader) UpdatePeakPnL(symbol, side string, currentPnLPct float64) {
	at.peakPnLCacheMutex.Lock()
	defer at.peakPnLCacheMutex.Unlock()
	if at.peakPnLCache == nil {
		at.peakPnLCache = make(map[string]float64)
	}

	posKey := symbol + "_" + side
	if peak, exists := at.peakPnLCache[posKey]; exists {
		// Update peak (if long, take larger value; if short, currentPnLPct is negative, also compare)
		if currentPnLPct > peak {
			at.peakPnLCache[posKey] = currentPnLPct
		}
	} else {
		// First time recording
		at.peakPnLCache[posKey] = currentPnLPct
	}
}

// ClearPeakPnLCache clears peak cache for specified position
func (at *AutoTrader) ClearPeakPnLCache(symbol, side string) {
	at.peakPnLCacheMutex.Lock()
	defer at.peakPnLCacheMutex.Unlock()

	posKey := symbol + "_" + side
	delete(at.peakPnLCache, posKey)
}

// ============================================================================
// Risk Control Helpers
// ============================================================================

// isBTCETH checks if a symbol is BTC or ETH
func isBTCETH(symbol string) bool {
	symbol = strings.ToUpper(symbol)
	return strings.HasPrefix(symbol, "BTC") || strings.HasPrefix(symbol, "ETH")
}

// isMajorAsset returns true for assets that should use the BTC/ETH higher
// position-value tier rather than the altcoin (1x equity) tier. This covers
// BTC/ETH crypto perps AND Hyperliquid XYZ assets (US equities, commodities,
// forex) — none of which are "altcoins" and all of which deserve the higher
// per-position cap so the AI can actually take meaningful positions.
func isMajorAsset(symbol string) bool {
	if isBTCETH(symbol) {
		return true
	}
	return market.IsXyzDexAsset(symbol)
}

// enforcePositionValueRatio checks and enforces position value ratio limits (CODE ENFORCED)
// Returns the adjusted position size (capped if necessary) and whether the position was capped
// positionSizeUSD: the original position size in USD
// equity: the account equity
// symbol: the trading symbol
func (at *AutoTrader) enforcePositionValueRatio(positionSizeUSD float64, equity float64, symbol string) (float64, bool) {
	if at.config.StrategyConfig == nil {
		return positionSizeUSD, false
	}

	riskControl := at.config.StrategyConfig.RiskControl

	// Get the appropriate position value ratio limit. BTC/ETH AND Hyperliquid
	// XYZ assets (US stocks etc.) use the higher tier; pure altcoins use the
	// lower tier.
	var maxPositionValueRatio float64
	if isMajorAsset(symbol) {
		maxPositionValueRatio = riskControl.BTCETHMaxPositionValueRatio
		if maxPositionValueRatio <= 0 {
			maxPositionValueRatio = 5.0 // Default: 5x for BTC/ETH and XYZ assets
		}
	} else {
		maxPositionValueRatio = riskControl.AltcoinMaxPositionValueRatio
		if maxPositionValueRatio <= 0 {
			maxPositionValueRatio = 1.0 // Default: 1x for altcoins
		}
	}
	if at.config.StrategyConfig.CoinSource.SourceType == "vergex_signal" &&
		maxPositionValueRatio > store.AutopilotMaxPositionValueRatio {
		maxPositionValueRatio = store.AutopilotMaxPositionValueRatio
	}

	// Calculate max allowed position value = equity × ratio
	maxPositionValue := equity * maxPositionValueRatio

	// Check if position size exceeds limit
	if positionSizeUSD > maxPositionValue {
		logger.Infof("  ⚠️ [RISK CONTROL] Position %.2f USDT exceeds limit (equity %.2f × %.1fx = %.2f USDT max for %s), capping",
			positionSizeUSD, equity, maxPositionValueRatio, maxPositionValue, symbol)
		return maxPositionValue, true
	}

	return positionSizeUSD, false
}

func (at *AutoTrader) applyAutopilotFullSizeOpen(decision *kernel.Decision, equity float64) {
	if at == nil || decision == nil || at.config.StrategyConfig == nil || equity <= 0 {
		return
	}

	cfg := at.config.StrategyConfig
	if cfg.CoinSource.SourceType != "vergex_signal" {
		return
	}

	riskControl := cfg.RiskControl
	leverage := riskControl.AltcoinMaxLeverage
	positionValueRatio := riskControl.AltcoinMaxPositionValueRatio
	if isMajorAsset(decision.Symbol) {
		leverage = riskControl.BTCETHMaxLeverage
		positionValueRatio = riskControl.BTCETHMaxPositionValueRatio
	}
	if leverage < store.MinLeverage {
		leverage = store.MinLeverage
	}
	if leverage > store.MaxAltLeverage {
		leverage = store.MaxAltLeverage
	}
	if positionValueRatio <= 0 {
		positionValueRatio = 1.0
	}
	if positionValueRatio > store.AutopilotMaxPositionValueRatio {
		positionValueRatio = store.AutopilotMaxPositionValueRatio
	}
	maxPositions := riskControl.MaxPositions
	if maxPositions <= 0 {
		maxPositions = 1
	}
	marginUsage := riskControl.MaxMarginUsage
	if marginUsage <= 0 || marginUsage > 1 {
		marginUsage = 1
	}
	allocatedRatio := float64(leverage) * marginUsage * autopilotBookMarginBudget / float64(maxPositions)
	if allocatedRatio < positionValueRatio {
		positionValueRatio = allocatedRatio
	}

	fullPositionSize := equity * positionValueRatio
	if fullPositionSize <= 0 {
		return
	}

	if decision.Leverage != leverage || decision.PositionSizeUSD != fullPositionSize {
		logger.Infof("  📏 [AUTOPILOT] Full-size open enforced for %s: leverage %dx → %dx, notional %.2f → %.2f USDT",
			decision.Symbol, decision.Leverage, leverage, decision.PositionSizeUSD, fullPositionSize)
	}
	decision.Leverage = leverage
	decision.PositionSizeUSD = fullPositionSize
}

// enforceMinPositionSize checks minimum position size (CODE ENFORCED)
func (at *AutoTrader) enforceMinPositionSize(positionSizeUSD float64) error {
	if at.config.StrategyConfig == nil {
		return nil
	}

	minSize := at.config.StrategyConfig.RiskControl.MinPositionSize
	if minSize <= 0 {
		minSize = 12 // Default: 12 USDT
	}

	if positionSizeUSD < minSize {
		return fmt.Errorf("❌ [RISK CONTROL] Position %.2f USDT below minimum (%.2f USDT)", positionSizeUSD, minSize)
	}
	return nil
}

// enforceMaxPositions checks maximum positions count (CODE ENFORCED)
func (at *AutoTrader) enforceMaxPositions(currentPositionCount int) error {
	if at.config.StrategyConfig == nil {
		return nil
	}

	maxPositions := at.config.StrategyConfig.RiskControl.MaxPositions
	if maxPositions <= 0 {
		maxPositions = 3 // Default: 3 positions
	}

	if currentPositionCount >= maxPositions {
		return fmt.Errorf("❌ [RISK CONTROL] Already at max positions (%d/%d)", currentPositionCount, maxPositions)
	}
	return nil
}

// getSideFromAction converts order action to side (BUY/SELL)
func getSideFromAction(action string) string {
	switch action {
	case "open_long", "close_short":
		return "BUY"
	case "open_short", "close_long":
		return "SELL"
	default:
		return "BUY"
	}
}
