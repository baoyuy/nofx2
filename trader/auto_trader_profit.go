package trader

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
	"nofx/trader/binance"
)

var profitLifecycleLocks sync.Map
var profitObservationEpoch = strconv.FormatInt(time.Now().UnixNano(), 10)

// LockPositionLifecycle is shared by protection and AI/manual open/close paths.
// Hold it from before rechecking the position through order recording and the
// lifecycle hook. Protection exits deliberately bypass AI close throttling.
func (at *AutoTrader) LockPositionLifecycle(symbol, side string) func() {
	account := at.exchangeID
	if account == "" {
		account = at.id
	}
	key := at.exchange + "/" + account + "/" + strings.ToUpper(symbol) + "/" + strings.ToLower(side)
	v, _ := profitLifecycleLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (at *AutoTrader) profitConfig() store.ProfitProtectionConfig {
	at.strategyMu.RLock()
	defer at.strategyMu.RUnlock()
	if at.config.StrategyConfig == nil {
		return store.NormalizeProfitProtection(store.ProfitProtectionConfig{})
	}
	return store.NormalizeProfitProtection(at.config.StrategyConfig.RiskControl.ProfitProtection)
}

func priceProfitPct(entry, mark float64, side string) float64 {
	if side == "short" {
		return (entry - mark) / entry * 100
	}
	return (mark - entry) / entry * 100
}

// profitIdentity never imports the legacy in-memory/prompt peaks. Unknown
// exchange identities use entry + process-first-observation and intentionally
// rebaseline on restart rather than trust an unverifiable prior position.
func (at *AutoTrader) profitIdentity(symbol, side string, entry float64, pos map[string]interface{}, ps *store.ProfitStateStore) (string, error) {
	suffix := ""
	if pt, ok := at.trader.(*binance.PaperTrader); ok {
		snapshot, exists := pt.ProfitPosition(symbol, side)
		if !exists || snapshot.EntryPrice != entry {
			return "", fmt.Errorf("paper position changed during observation")
		}
		suffix = fmt.Sprintf("/paper:%d", snapshot.OpenedAt.UnixNano())
	}
	if at.store != nil {
		dbPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, side)
		if err != nil {
			return "", err
		}
		if dbPos != nil && dbPos.ID > 0 && math.Abs(dbPos.EntryPrice-entry) <= math.Max(entry*1e-6, 1e-12) {
			return fmt.Sprintf("db:%d:%d%s", dbPos.ID, dbPos.EntryTime, suffix), nil
		}
	}
	if suffix != "" {
		return suffix, nil
	}
	if created, ok := pos["createdTime"].(int64); ok && created > 0 {
		return fmt.Sprintf("exchange:%d", created), nil
	}
	prefix := "observation:" + profitObservationEpoch + ":"
	previous, err := ps.Get(at.id, symbol, side)
	if err != nil {
		return "", err
	}
	if previous != nil && previous.EntryPrice == entry && strings.HasPrefix(previous.Lifecycle, prefix) {
		return previous.Lifecycle, nil
	}
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 10), nil
}

func (at *AutoTrader) observeProfit(pos map[string]interface{}, opened bool) (*store.PositionProfitState, *store.ProfitStateStore, float64, error) {
	symbol, _ := pos["symbol"].(string)
	side, _ := pos["side"].(string)
	side = strings.ToLower(side)
	entry, _ := pos["entryPrice"].(float64)
	mark, _ := pos["markPrice"].(float64)
	qty, _ := pos["positionAmt"].(float64)
	if symbol == "" || (side != "long" && side != "short") || !finitePositive(entry) || !finitePositive(mark) || qty == 0 || math.IsNaN(qty) || math.IsInf(qty, 0) {
		return nil, nil, 0, fmt.Errorf("invalid position observation")
	}
	ps, err := at.store.ProfitState()
	if err != nil {
		return nil, nil, 0, err
	}
	lifecycle, err := at.profitIdentity(symbol, side, entry, pos, ps)
	if err != nil {
		return nil, nil, 0, err
	}
	cfg := at.profitConfig()
	configJSON, _ := json.Marshal(cfg)
	current := priceProfitPct(entry, mark, side)
	seed := store.PositionProfitState{TraderID: at.id, Symbol: symbol, Side: side, Lifecycle: lifecycle, EntryPrice: entry, PeakPricePct: math.Max(0, current), Eligible: opened && cfg.Enabled && at.exchange == binance.PaperExchangeType, ConfigJSON: string(configJSON)}
	state, fresh, err := ps.Observe(seed, opened)
	if err != nil {
		return nil, nil, 0, err
	}
	if fresh && !opened {
		logger.Infof("📊 %s %s: trusted profit baseline starts now; legacy prompt peaks discarded; optional paper protection not enabled for discovered positions", symbol, side)
	}
	// UI compatibility only. Exact assignment replaces a possibly polluted cache;
	// all decisions use lifecycle-validated, price-basis persisted state instead.
	leverage, _ := pos["leverage"].(float64)
	if leverage <= 0 {
		leverage = 1
	}
	at.peakPnLCacheMutex.Lock()
	if at.peakPnLCache == nil {
		at.peakPnLCache = make(map[string]float64)
	}
	at.peakPnLCache[symbol+"_"+side] = state.PeakPricePct * leverage
	at.peakPnLCacheMutex.Unlock()
	return state, ps, current, nil
}

func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

// ObservePositionPeak is the prompt/UI hook. The return value is entry-cost
// leveraged ROI for display; the protected peak remains unleveraged price %.
// It performs no exchange requests and never places orders.
func (at *AutoTrader) ObservePositionPeak(pos map[string]interface{}) (float64, error) {
	symbol, _ := pos["symbol"].(string)
	side, _ := pos["side"].(string)
	unlock := at.LockPositionLifecycle(symbol, side)
	defer unlock()
	state, _, _, err := at.observeProfit(pos, false)
	if err != nil {
		return 0, err
	}
	leverage, _ := pos["leverage"].(float64)
	if leverage <= 0 {
		leverage = 1
	}
	return state.PeakPricePct * leverage, nil
}

// ProfitProtectionOpened must run after a CONFIRMED NEW open has been recorded
// in trader_positions, and before releasing LockPositionLifecycle. Do not call
// it at startup or for an addition to an existing position. No network requests.
func (at *AutoTrader) ProfitProtectionOpened(symbol, side string) error {
	if at.store == nil {
		return fmt.Errorf("profit lifecycle requires store")
	}
	side = strings.ToLower(side)
	var entry, qty float64
	leverage := 1
	if pt, ok := at.trader.(*binance.PaperTrader); ok {
		p, exists := pt.ProfitPosition(symbol, side)
		if !exists {
			return fmt.Errorf("new paper position not found")
		}
		entry, qty, leverage = p.EntryPrice, p.Quantity, p.Leverage
	} else {
		if at.store == nil {
			return fmt.Errorf("profit lifecycle requires store")
		}
		p, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, side)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("new position not recorded")
		}
		entry, qty, leverage = p.EntryPrice, p.Quantity, p.Leverage
	}
	ps, err := at.store.ProfitState()
	pos := map[string]interface{}{"symbol": symbol, "side": side, "entryPrice": entry, "markPrice": entry, "positionAmt": qty, "leverage": float64(leverage)}
	if err != nil {
		return err
	}
	lifecycle, err := at.profitIdentity(symbol, side, entry, pos, ps)
	if err != nil {
		return err
	}
	cfg := at.profitConfig()
	encoded, _ := json.Marshal(cfg)
	return ps.Open(store.PositionProfitState{TraderID: at.id, Symbol: symbol, Side: side, Lifecycle: lifecycle, EntryPrice: entry, Eligible: cfg.Enabled && at.exchange == binance.PaperExchangeType, ConfigJSON: string(encoded)})
}

// ProfitProtectionClosed requires confirmed flat, while holding the shared
// lifecycle lock. Partial closes must retain the state. Never use for an old
// queued event if a same-symbol replacement position is already open.
func (at *AutoTrader) ProfitProtectionClosed(symbol, side string) error {
	at.ClearPeakPnLCache(symbol, strings.ToLower(side))
	ps, err := at.store.ProfitState()
	if err != nil {
		return err
	}
	return ps.Clear(at.id, symbol, strings.ToLower(side), "")
}

// advanceProfitProtection latches arms forever within this lifecycle and never
// loosens a persisted stop, including after config edits or process restart.
func advanceProfitProtection(state *store.PositionProfitState, current float64, paperEnabled bool) (legacyClose bool) {
	state.PeakPricePct = math.Max(state.PeakPricePct, current)
	state.LegacyArmed = state.LegacyArmed || state.PeakPricePct > drawdownClosePriceGainPct
	giveback := 0.0
	if state.PeakPricePct > 0 {
		giveback = (state.PeakPricePct - current) / state.PeakPricePct * 100
	}
	legacyClose = state.LegacyArmed && shouldDrawdownClose(state.PeakPricePct, giveback)
	if !paperEnabled || !state.Eligible {
		return legacyClose
	}
	var cfg store.ProfitProtectionConfig
	if json.Unmarshal([]byte(state.ConfigJSON), &cfg) != nil || !cfg.Enabled {
		return legacyClose
	}
	cfg = store.NormalizeProfitProtection(cfg)
	state.BreakevenArmed = state.BreakevenArmed || state.PeakPricePct >= cfg.BreakevenActivationPct
	state.TrailingArmed = state.TrailingArmed || state.PeakPricePct >= cfg.TrailingActivationPct
	candidate := 0.0
	if state.BreakevenArmed {
		candidate = binance.PaperBreakevenStop(state.EntryPrice, state.Side, cfg.BreakevenLockPct)
	}
	if state.TrailingArmed {
		floorPct := state.PeakPricePct * (1 - cfg.TrailingGivebackPct/100)
		trailing := state.EntryPrice * (1 + floorPct/100)
		if state.Side == "short" {
			trailing = state.EntryPrice * (1 - floorPct/100)
		}
		candidate = tighterProfitStop(state.Side, candidate, trailing)
	}
	state.StopPrice = tighterProfitStop(state.Side, state.StopPrice, candidate)
	return legacyClose
}

func tighterProfitStop(side string, old, candidate float64) float64 {
	if candidate <= 0 {
		return old
	}
	if old <= 0 {
		return candidate
	}
	if side == "short" {
		return math.Min(old, candidate)
	}
	return math.Max(old, candidate)
}

func (at *AutoTrader) protectPositionProfit(pos map[string]interface{}) error {
	symbol, _ := pos["symbol"].(string)
	side, _ := pos["side"].(string)
	side = strings.ToLower(side)
	unlock := at.LockPositionLifecycle(symbol, side)
	defer unlock()
	at.positionActionMu.Lock()
	defer at.positionActionMu.Unlock()
	state, ps, current, err := at.observeProfit(pos, false)
	if err != nil {
		return err
	}
	cfg := at.profitConfig()
	paperEnabled := at.exchange == binance.PaperExchangeType && cfg.Enabled
	legacyClose := advanceProfitProtection(state, current, paperEnabled)
	// Persist arms and desired floor BEFORE exchange action. If action fails, the
	// next tick retries the same or tighter floor, never a reset/looser one.
	if err := ps.Save(state); err != nil {
		return err
	}
	mark, _ := pos["markPrice"].(float64)
	if pt, ok := at.trader.(*binance.PaperTrader); ok {
		snapshot, exists := pt.ProfitPosition(symbol, side)
		if !exists || snapshot.EntryPrice != state.EntryPrice {
			return fmt.Errorf("paper position changed")
		}
		// Disabled means no new configurable actions, but already-set native paper
		// stops remain active. Baseline 5%/40% protection still applies everywhere.
		stop := 0.0
		if paperEnabled && state.Eligible {
			stop = state.StopPrice
		}
		if !legacyClose && stop == 0 {
			return nil
		}
		actual, closed, applyErr := pt.ApplyProfitProtection(snapshot, mark, stop, legacyClose)
		if closed {
			_ = ps.Clear(at.id, symbol, side, state.Lifecycle)
			at.ClearPeakPnLCache(symbol, side)
			return applyErr // durable simulator event recorded by syncPaperCloses
		}
		if applyErr != nil {
			return applyErr
		}
		state.StopPrice = tighterProfitStop(side, state.StopPrice, actual)
		return ps.Save(state)
	}
	if !legacyClose {
		return nil
	}
	// Real exchanges keep the ORIGINAL 5% price / 40% giveback semantics; only
	// lifecycle, denominator and persistent arming bugs are fixed.
	positions, err := at.trader.GetPositions()
	if err != nil {
		return err
	}
	for _, live := range positions {
		sym, _ := live["symbol"].(string)
		s, _ := live["side"].(string)
		if sym != symbol || strings.ToLower(s) != side {
			continue
		}
		liveState, _, _, err := at.observeProfit(live, false)
		if err != nil {
			return err
		}
		if liveState.Lifecycle != state.Lifecycle || liveState.EntryPrice != state.EntryPrice {
			return fmt.Errorf("position lifecycle changed before protective close")
		}
		entry, _ := live["entryPrice"].(float64)
		mark, _ := live["markPrice"].(float64)
		qty, _ := live["positionAmt"].(float64)
		if qty == 0 {
			return nil
		}
		if err := at.emergencyClosePosition(symbol, side, math.Abs(qty), mark, entry); err != nil {
			return err
		}
		at.ClearPeakPnLCache(symbol, side)
		return ps.Clear(at.id, symbol, side, state.Lifecycle)
	}
	return nil
}
