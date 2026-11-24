package trader

import (
	"fmt"
	"math"
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"strings"
	"time"
)

var openingCandles = market.GetWithTimeframesFor
var openingQuote = market.GetReferenceQuote

func positiveBudget(v, fallback float64) float64 {
	if v == 0 {
		return fallback
	}
	return v
}

// sizeOpening computes a notional ceiling from actual stop distance including
// costs. Available balance and configured book margin are independent ceilings.
func sizeOpening(d *kernel.Decision, r store.RiskControlConfig, equity, available, usedMargin, bookRisk, price float64, signalManaged bool) error {
	for _, v := range []float64{equity, available, usedMargin, bookRisk, price, d.PositionSizeUSD, d.StopLoss, d.TakeProfit, r.MaxMarginUsage, r.MaxTradeRiskPct, r.MaxPortfolioRiskPct, r.TradingCostPct, r.MinRiskRewardRatio, r.MinPositionSize, r.MaxDailyLossPct, r.MaxAccountDrawdownPct, r.MinStopATR, r.MaxStopATR} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return fmt.Errorf("invalid opening risk input")
		}
	}
	if equity <= 0 || price <= 0 || d.PositionSizeUSD <= 0 || d.Leverage <= 0 {
		return fmt.Errorf("opening requires positive equity, quote, size and leverage")
	}
	if err := validateProtectionPrices(d.Action, price, d.StopLoss, d.TakeProfit, signalManaged); err != nil {
		return err
	}
	cost := positiveBudget(r.TradingCostPct, 0.15) / 100
	distance := math.Abs(price-d.StopLoss) / price
	riskPerDollar := distance + cost
	if !signalManaged {
		reward := math.Abs(d.TakeProfit-price)/price - cost
		if reward <= 0 || reward/riskPerDollar < r.MinRiskRewardRatio {
			return fmt.Errorf("net reward/risk %.3f below configured %.3f after costs", reward/riskPerDollar, r.MinRiskRewardRatio)
		}
	}
	marginCap := r.MaxMarginUsage
	if marginCap <= 0 || marginCap > 1 {
		return fmt.Errorf("max margin usage must be in (0,1]")
	}
	free := math.Min(available, equity*marginCap-usedMargin)
	if free <= 0 {
		return fmt.Errorf("book margin limit reached")
	}
	capSize := free / (marginOverheadFactor/float64(d.Leverage) + takerFeeRate) * positionSizeSafetyFactor
	if !signalManaged {
		perTrade := equity * positiveBudget(r.MaxTradeRiskPct, 1) / 100
		remaining := equity*positiveBudget(r.MaxPortfolioRiskPct, 4)/100 - bookRisk
		if remaining <= 0 {
			return fmt.Errorf("portfolio stop-risk budget exhausted")
		}
		capSize = math.Min(capSize, math.Min(perTrade, remaining)/riskPerDollar)
	}
	d.PositionSizeUSD = math.Min(d.PositionSizeUSD, capSize)
	d.RiskUSD = d.PositionSizeUSD * riskPerDollar
	minimum := math.Max(12, r.MinPositionSize)
	if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
		minimum = math.Max(minimum, 60)
	}
	if d.PositionSizeUSD < minimum {
		return fmt.Errorf("risk-limited size %.2f below minimum order", d.PositionSizeUSD)
	}
	return nil
}

func (at *AutoTrader) prepareOpening(d *kernel.Decision, positions []map[string]interface{}) (*market.Data, error) {
	cfg := at.GetStrategyConfig()
	if cfg == nil {
		return nil, fmt.Errorf("opening requires strategy configuration")
	}
	r := cfg.RiskControl
	if cfg.CoinSource.SourceType == "coin_pool" {
		coins, err := at.strategyEngine.GetCandidateCoins()
		if err != nil {
			return nil, fmt.Errorf("refresh coin pool: %w", err)
		}
		if reason := at.strategyEngine.OpenBlockReason(*d, &kernel.Context{CandidateCoins: coins}); reason != "" {
			if reason == "symbol no longer in live coin pool" {
				return nil, fmt.Errorf("symbol no longer in live coin pool: %s removed or expired while AI was analysing; entry skipped, retry with next cycle's pool", d.Symbol)
			}
			return nil, fmt.Errorf("%s", reason)
		}
	}

	if d.Confidence < 0 || d.Confidence > 100 || (at.programStableConfig() == nil && d.Confidence < r.MinConfidence) {
		return nil, fmt.Errorf("confidence %d below configured %d", d.Confidence, r.MinConfidence)
	}
	maxLev := r.AltcoinMaxLeverage
	if isMajorAsset(d.Symbol) {
		maxLev = r.BTCETHMaxLeverage
	}
	if d.Leverage <= 0 || maxLev <= 0 {
		return nil, fmt.Errorf("invalid leverage")
	}
	if d.Leverage > maxLev {
		d.Leverage = maxLev
	}
	tf := cfg.Indicators.Klines.PrimaryTimeframe
	if tf == "" {
		tf = "1h"
	}
	md, err := openingCandles(d.Symbol, at.exchange, []string{tf}, tf, 60)
	if err != nil {
		return nil, fmt.Errorf("primary-timeframe candles: %w", err)
	}
	quote, err := openingQuote(d.Symbol, at.exchange)
	if err != nil {
		return nil, fmt.Errorf("execution quote: %w", err)
	}
	md.SetQuote(quote)
	if !md.OpeningAllowed(time.Now()) {
		return nil, fmt.Errorf("execution quote or primary candles stale")
	}
	if err := validateProtectionPrices(d.Action, md.CurrentPrice, d.StopLoss, d.TakeProfit, at.usesSignalManagedExit()); err != nil {
		return nil, err
	}
	atr := 0.0
	if series := md.TimeframeData[tf]; series != nil {
		atr = series.ATR14
	}
	if (r.MinStopATR > 0 || r.MaxStopATR > 0) && atr <= 0 {
		return nil, fmt.Errorf("primary timeframe ATR unavailable")
	}
	d.StopLoss = clampStopToATR(d.Action == "open_long", md.CurrentPrice, atr, d.StopLoss, r.MinStopATR, r.MaxStopATR)
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, err
	}
	equity := extractInitialBalance(balance)
	available, _ := balance["availableBalance"].(float64)
	used, bookRisk := 0.0, 0.0
	for _, p := range positions {
		symbol, _ := p["symbol"].(string)
		side, _ := p["side"].(string)
		if symbol == d.Symbol {
			return nil, fmt.Errorf("symbol already held")
		}
		qty, _ := p["positionAmt"].(float64)
		qty = math.Abs(qty)
		mark, _ := p["markPrice"].(float64)
		lev, _ := p["leverage"].(float64)
		if !finitePositive(mark) || !finitePositive(lev) || !finitePositive(qty) || (side != "long" && side != "short") {
			return nil, fmt.Errorf("existing position valuation unavailable")
		}
		used += qty * mark / lev
		if !at.usesSignalManagedExit() {
			orders, e := at.trader.GetOpenOrders(symbol)
			if e != nil {
				return nil, fmt.Errorf("cannot verify portfolio stops: %w", e)
			}
			stop := 0.0
			for _, o := range orders {
				kind, orderSide := strings.ToUpper(o.Type), strings.ToUpper(o.Side)
				positionSide := strings.ToUpper(o.PositionSide)
				validSide := positionSide == "" || positionSide == "BOTH" || positionSide == strings.ToUpper(side)
				if finitePositive(o.StopPrice) && o.Quantity+1e-9 >= qty && validSide && (kind == "STOP_MARKET" || kind == "STOP" || kind == "STOP_LOSS") && ((side == "long" && orderSide == "SELL") || (side == "short" && orderSide == "BUY")) {
					// Choose the worst fully covering stop, never underestimate risk.
					if stop == 0 || (side == "long" && o.StopPrice < stop) || (side == "short" && o.StopPrice > stop) {
						stop = o.StopPrice
					}
				}
			}
			if stop <= 0 {
				return nil, fmt.Errorf("existing %s has no verifiable protective stop", symbol)
			}
			loss := mark - stop
			if side == "short" {
				loss = stop - mark
			}
			bookRisk += qty * (math.Max(0, loss) + mark*positiveBudget(r.TradingCostPct, 0.15)/100)
		}
	}
	if !at.usesSignalManagedExit() {
		state, e := at.observeAccountRisk(equity)
		if e != nil {
			return nil, e
		}
		if reason := accountRiskReason(state, equity, r); reason != "" {
			return nil, fmt.Errorf("%s", reason)
		}
	}
	at.applyAutopilotFullSizeOpen(d, equity)
	d.PositionSizeUSD, _ = at.enforcePositionValueRatio(d.PositionSizeUSD, equity, d.Symbol)
	if err := sizeOpening(d, r, equity, available, used, bookRisk, md.CurrentPrice, at.usesSignalManagedExit()); err != nil {
		return nil, err
	}
	if !md.OpeningAllowed(time.Now()) {
		return nil, fmt.Errorf("execution data expired during account checks")
	}
	return md, nil
}

// Verify the fill before keeping new exposure. A quote is never a fill guarantee.
func (at *AutoTrader) verifyOpeningFill(d *kernel.Decision) error {
	if inv, ok := at.trader.(interface{ InvalidatePositionCache() }); ok {
		inv.InvalidatePositionCache()
	}
	positions, err := at.trader.GetPositions()
	if err != nil {
		return err
	}
	for _, p := range positions {
		if p["symbol"] != d.Symbol || (d.Action == "open_long" && p["side"] != "long") || (d.Action == "open_short" && p["side"] != "short") {
			continue
		}
		entry, _ := p["entryPrice"].(float64)
		if !finitePositive(entry) {
			return fmt.Errorf("invalid confirmed entry price")
		}
		cfg := at.GetStrategyConfig()
		if cfg == nil {
			return fmt.Errorf("strategy missing after fill")
		}
		if err := validateProtectionPrices(d.Action, entry, d.StopLoss, d.TakeProfit, at.usesSignalManagedExit()); err != nil {
			return err
		}
		cost := positiveBudget(cfg.RiskControl.TradingCostPct, 0.15) / 100
		risk := math.Abs(entry-d.StopLoss)/entry + cost
		if !at.usesSignalManagedExit() && (math.Abs(d.TakeProfit-entry)/entry-cost)/risk < cfg.RiskControl.MinRiskRewardRatio {
			return fmt.Errorf("actual fill violates configured net reward/risk")
		}
		qty, _ := p["positionAmt"].(float64)
		if !finitePositive(math.Abs(qty)) {
			return fmt.Errorf("invalid confirmed quantity")
		}
		if !at.usesSignalManagedExit() && math.Abs(qty)*entry*risk > d.RiskUSD*1.01 {
			return fmt.Errorf("actual fill exceeds approved stop-risk budget")
		}
		return nil
	}
	return fmt.Errorf("new position not confirmed")
}

// Account entry pauses never disable position protection or ordinary exits.
func accountRiskReason(state *store.AccountRiskState, equity float64, r store.RiskControlConfig) string {
	if state == nil || !finitePositive(equity) || !finitePositive(state.DayStartEquity) || !finitePositive(state.PeakEquity) {
		return "account risk baseline unavailable"
	}
	if loss := (state.DayStartEquity - equity) / state.DayStartEquity * 100; loss >= positiveBudget(r.MaxDailyLossPct, 3) {
		return fmt.Sprintf("daily equity loss limit reached: %.2f%% (UTC day)", loss)
	}
	if dd := (state.PeakEquity - equity) / state.PeakEquity * 100; dd >= positiveBudget(r.MaxAccountDrawdownPct, 10) {
		return fmt.Sprintf("account drawdown limit reached: %.2f%%", dd)
	}
	return ""
}

func (at *AutoTrader) observeAccountRisk(equity float64) (*store.AccountRiskState, error) {
	state, err := at.store.ObserveAccountRisk(at.id, equity, time.Now())
	reason := ""
	cfg := at.GetStrategyConfig()
	if err != nil || cfg == nil {
		reason = "account risk baseline unavailable"
	} else {
		reason = accountRiskReason(state, equity, cfg.RiskControl)
	}
	status := map[string]interface{}{"entry_paused": reason != "", "reason": reason, "checked_at": time.Now().UTC().Format(time.RFC3339)}
	if state != nil && err == nil {
		status["utc_day"] = state.Day
		status["day_start_equity"] = state.DayStartEquity
		status["peak_equity"] = state.PeakEquity
		status["equity"] = equity
		status["daily_loss_pct"] = (state.DayStartEquity - equity) / state.DayStartEquity * 100
		status["drawdown_pct"] = (state.PeakEquity - equity) / state.PeakEquity * 100
	}
	at.runtimeHealthMu.Lock()
	at.accountRiskStatus = status
	at.runtimeHealthMu.Unlock()
	return state, err
}
