package explosive

import (
	"context"
	"math"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
)

// PaperConfig defines a deterministic rule set for the board's simulated
// accounts. No real money: fills use the live price and a taker fee.
type PaperConfig struct {
	DefaultEquity float64
	RiskPerTrade  float64
	StopATR       float64
	TargetATR     float64
	MaxPositions  int
	MaxNotionalX  float64
	FeeRate       float64
	TimeStopHours float64
	Tick          time.Duration
	MinVolSpike   float64
	NeedOIRising  bool
	MaxDayGain    float64
}

// DefaultPaperConfig is a conservative short-term long-only rule.
func DefaultPaperConfig() PaperConfig {
	return PaperConfig{
		DefaultEquity: 1000, RiskPerTrade: 0.005,
		StopATR: 1.5, TargetATR: 3.0, MaxPositions: 3, MaxNotionalX: 0.5,
		FeeRate: 0.0005, TimeStopHours: 4, Tick: 60 * time.Second,
		MinVolSpike: 3, NeedOIRising: true, MaxDayGain: 0.5,
	}
}

// PaperTrader runs the rule engine for the built-in default account plus every
// enabled configured account.
type PaperTrader struct {
	cfg   PaperConfig
	store *store.PaperStore
	mu    sync.Mutex
}

func NewPaperTrader(st *store.PaperStore, cfg PaperConfig) *PaperTrader {
	p := &PaperTrader{cfg: cfg, store: st}
	p.applyStoredSettings()
	return p
}

// applyStoredSettings overlays the user-tuned 量化 settings onto the defaults.
// A zero risk means "never saved", so the defaults stay.
func (p *PaperTrader) applyStoredSettings() {
	s, err := p.store.GetSettings()
	if err != nil || s.RiskPct <= 0 {
		return
	}
	p.cfg.RiskPerTrade = s.RiskPct / 100
	if s.StopATR > 0 {
		p.cfg.StopATR = s.StopATR
	}
	if s.TargetATR > 0 {
		p.cfg.TargetATR = s.TargetATR
	}
	if s.MaxPositions > 0 {
		p.cfg.MaxPositions = s.MaxPositions
	}
	if s.MaxNotionalX > 0 {
		p.cfg.MaxNotionalX = s.MaxNotionalX
	}
	if s.MinVolSpike > 0 {
		p.cfg.MinVolSpike = s.MinVolSpike
	}
	if s.TimeStopHours > 0 {
		p.cfg.TimeStopHours = s.TimeStopHours
	}
}

// Settings returns the effective rule settings for the UI.
func (p *PaperTrader) Settings() store.ExplosivePaperSettings {
	s, _ := p.store.GetSettings()
	if s.RiskPct <= 0 {
		s = store.ExplosivePaperSettings{
			ID: 1, RiskPct: p.cfg.RiskPerTrade * 100, StopATR: p.cfg.StopATR,
			TargetATR: p.cfg.TargetATR, MaxPositions: p.cfg.MaxPositions,
			MaxNotionalX: p.cfg.MaxNotionalX, MinVolSpike: p.cfg.MinVolSpike,
			TimeStopHours: p.cfg.TimeStopHours,
		}
	}
	return s
}

// UpdateSettings saves user-tuned rule settings and applies them immediately.
func (p *PaperTrader) UpdateSettings(v store.ExplosivePaperSettings) store.ExplosivePaperSettings {
	if v.RiskPct < 0.1 {
		v.RiskPct = 0.1
	}
	if v.RiskPct > 5 {
		v.RiskPct = 5
	}
	if v.StopATR < 0.3 {
		v.StopATR = 0.3
	}
	if v.TargetATR < 0.5 {
		v.TargetATR = 0.5
	}
	if v.MaxPositions < 1 {
		v.MaxPositions = 1
	}
	if v.MaxPositions > 8 {
		v.MaxPositions = 8
	}
	if v.MaxNotionalX < 0.1 {
		v.MaxNotionalX = 0.1
	}
	if v.MaxNotionalX > 2 {
		v.MaxNotionalX = 2
	}
	if v.MinVolSpike < 0 {
		v.MinVolSpike = 0
	}
	if v.TimeStopHours < 0.5 {
		v.TimeStopHours = 0.5
	}
	v.UpdatedAt = time.Now().UnixMilli()
	_ = p.store.SaveSettings(&v)
	p.mu.Lock()
	p.applyStoredSettings()
	p.mu.Unlock()
	return v
}

// StartPaper migrates storage, runs the engine until ctx ends, and returns it.
func StartPaper(ctx context.Context, st *store.PaperStore) *PaperTrader {
	if err := st.AutoMigrate(); err != nil {
		logger.Warnf("explosive paper: migrate failed: %v", err)
		return nil
	}
	p := NewPaperTrader(st, DefaultPaperConfig())
	logger.Infof("📝 Explosive paper engine started: default equity=%.0f risk=%.2f%% stop=%.1fATR target=%.1fATR",
		p.cfg.DefaultEquity, p.cfg.RiskPerTrade*100, p.cfg.StopATR, p.cfg.TargetATR)
	go func() {
		t := time.NewTicker(p.cfg.Tick)
		defer t.Stop()
		p.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.tick(ctx)
			}
		}
	}()
	return p
}

func (p *PaperTrader) priceMap(b Board) map[string]float64 {
	pm := make(map[string]float64, len(b.Rows)+len(b.Radar)+len(b.Blasted))
	add := func(rows []Row) {
		for _, r := range rows {
			if r.Price > 0 {
				pm[r.Symbol] = r.Price
			}
		}
	}
	add(b.Rows)
	add(b.Radar)
	add(b.Blasted)
	return pm
}

type paperAccount struct {
	id    int64
	name  string
	start float64
}

// accountList returns the default account (id 0) and every enabled configured
// paper account.
func (p *PaperTrader) accountList() []paperAccount {
	out := []paperAccount{{id: 0, name: "默认模拟盘", start: p.cfg.DefaultEquity}}
	rows, _ := p.store.Accounts()
	for _, a := range rows {
		if !a.Enabled {
			continue
		}
		start := a.StartingEquity
		if start <= 0 {
			start = p.cfg.DefaultEquity
		}
		out = append(out, paperAccount{id: a.ID, name: a.Name, start: start})
	}
	return out
}

func (p *PaperTrader) tick(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	board, err := Shared.Board(ctx, false)
	if err != nil {
		return
	}
	p.applyStoredSettings()
	pm := p.priceMap(board)
	priceOf := func(sym string) float64 {
		if v, ok := pm[sym]; ok && v > 0 {
			return v
		}
		if v, ok := Shared.Price(sym); ok {
			return v
		}
		return 0
	}
	now := time.Now()
	for _, acc := range p.accountList() {
		p.processAccount(acc, board, priceOf, now)
	}
}

func (p *PaperTrader) processAccount(acc paperAccount, board Board, priceOf func(string) float64, now time.Time) {
	realized, _ := p.store.RealizedTotal(acc.id)
	open, _ := p.store.OpenTrades(acc.id)
	openEntryFees := 0.0
	unrealized := 0.0
	stillOpen := make(map[string]bool, len(open))

	for i := range open {
		t := &open[i]
		pr := priceOf(t.Symbol)
		if pr <= 0 {
			stillOpen[t.Symbol] = true
			continue
		}
		exit := ""
		fill := 0.0
		switch {
		case pr <= t.Stop:
			exit, fill = "stop", t.Stop
		case pr >= t.Target:
			exit, fill = "target", t.Target
		case now.Sub(time.UnixMilli(t.OpenedAt)).Hours() >= p.cfg.TimeStopHours:
			exit, fill = "time", pr
		}
		if exit != "" {
			exitFee := t.Quantity * fill * p.cfg.FeeRate
			pnl := (fill-t.Entry)*t.Quantity - t.Fee - exitFee
			t.Status, t.ClosedAt, t.ExitPrice, t.ExitReason = "closed", now.UnixMilli(), fill, exit
			t.Fee += exitFee
			t.Realized = pnl
			_ = p.store.SaveTrade(t)
			realized += pnl
			continue
		}
		stillOpen[t.Symbol] = true
		openEntryFees += t.Fee
		unrealized += (pr - t.Entry) * t.Quantity
	}

	equity := acc.start + realized + unrealized - openEntryFees
	openCount := len(stillOpen)
	for _, r := range board.Rows {
		if openCount >= p.cfg.MaxPositions {
			break
		}
		if stillOpen[r.Symbol] || !p.entryOK(r) {
			continue
		}
		atrFrac := r.ATRPct / 100
		if atrFrac <= 0 || r.Price <= 0 {
			continue
		}
		stopDist := p.cfg.StopATR * atrFrac
		if stopDist <= 0 || stopDist >= 1 {
			continue
		}
		notional := (equity * p.cfg.RiskPerTrade) / stopDist
		if capN := equity * p.cfg.MaxNotionalX; notional > capN {
			notional = capN
		}
		if notional < 10 {
			continue
		}
		qty := notional / r.Price
		fee := notional * p.cfg.FeeRate
		t := &store.ExplosivePaperTrade{
			AccountID: acc.id, Symbol: r.Symbol, Side: "long", OpenedAt: now.UnixMilli(),
			Entry: r.Price, Quantity: qty, Notional: notional,
			Stop: r.Price * (1 - stopDist), Target: r.Price * (1 + p.cfg.TargetATR*atrFrac),
			ATRPct: r.ATRPct, RiskUSD: notional * stopDist, Fee: fee,
			VolSpike: r.VolSpike1h, OIPct: r.OIChange1h, DayGain: r.DayGain, Status: "open",
		}
		if err := p.store.CreateTrade(t); err != nil {
			logger.Warnf("explosive paper: create trade %s/%s: %v", acc.name, r.Symbol, err)
			continue
		}
		stillOpen[r.Symbol] = true
		openCount++
		openEntryFees += fee
		logger.Infof("📝 paper[%s] open %s @%.6f stop=%.6f target=%.6f risk=%.1fUSD", acc.name, r.Symbol, t.Entry, t.Stop, t.Target, t.RiskUSD)
	}

	equity = acc.start + realized + unrealized - openEntryFees
	_ = p.store.CreateEquity(&store.ExplosivePaperEquity{
		AccountID: acc.id, TS: now.UnixMilli(), Equity: equity, Realized: realized,
		Unrealized: unrealized - openEntryFees, OpenCount: openCount,
	})
}

func (p *PaperTrader) entryOK(r Row) bool {
	if r.Price <= 0 || r.ATRPct <= 0 {
		return false
	}
	if r.DayGain <= 0.02 || r.DayGain > p.cfg.MaxDayGain {
		return false
	}
	if r.VolSpike1h < p.cfg.MinVolSpike {
		return false
	}
	if p.cfg.NeedOIRising && (!r.OIKnown || r.OIChange1h <= 0) {
		return false
	}
	return true
}

// ExplosivePaperView is the read model the API returns for one account.
type ExplosivePaperView struct {
	AccountID      int64                        `json:"account_id"`
	Label          string                       `json:"label"`
	StartingEquity float64                      `json:"starting_equity"`
	Equity         float64                      `json:"equity"`
	ReturnPct      float64                      `json:"return_pct"`
	Realized       float64                      `json:"realized"`
	Unrealized     float64                      `json:"unrealized"`
	OpenCount      int                          `json:"open_count"`
	Trades         int                          `json:"trades"`
	Wins           int                          `json:"wins"`
	WinRate        float64                      `json:"win_rate"`
	AvgWin         float64                      `json:"avg_win"`
	AvgLoss        float64                      `json:"avg_loss"`
	ProfitFactor   float64                      `json:"profit_factor"`
	Open           []OpenPositionView           `json:"open"`
	Recent         []store.ExplosivePaperTrade  `json:"recent"`
	EquitySeries   []store.ExplosivePaperEquity `json:"equity_series"`
	Config         map[string]float64           `json:"config"`
	UpdatedAt      int64                        `json:"updated_at"`
}

// OpenPositionView is one live position with its current price and floating PnL.
type OpenPositionView struct {
	ID            int64   `json:"id"`
	Symbol        string  `json:"symbol"`
	Entry         float64 `json:"entry_price"`
	Current       float64 `json:"current_price"`
	Stop          float64 `json:"stop_price"`
	Target        float64 `json:"target_price"`
	Quantity      float64 `json:"quantity"`
	Notional      float64 `json:"notional"`
	RiskUSD       float64 `json:"risk_usd"`
	ATRPct        float64 `json:"atr_pct"`
	Unrealized    float64 `json:"unrealized_pnl"`
	UnrealizedPct float64 `json:"unrealized_pct"`
	OpenedAt      int64   `json:"opened_at"`
}

// AccountSummary is a compact per-account card for the account switcher.
type AccountSummary struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	ExchangeID     string  `json:"exchange_id"`
	StartingEquity float64 `json:"starting_equity"`
	Equity         float64 `json:"equity"`
	ReturnPct      float64 `json:"return_pct"`
	Trades         int     `json:"trades"`
	WinRate        float64 `json:"win_rate"`
	OpenCount      int     `json:"open_count"`
}

func (p *PaperTrader) startFor(accID int64) (name string, start float64) {
	for _, a := range p.accountList() {
		if a.id == accID {
			return a.name, a.start
		}
	}
	return "默认模拟盘", p.cfg.DefaultEquity
}

// Snapshot assembles one account's state for the API.
func (p *PaperTrader) Snapshot(accID int64) ExplosivePaperView {
	p.mu.Lock()
	defer p.mu.Unlock()
	name, start := p.startFor(accID)
	v := ExplosivePaperView{AccountID: accID, Label: name, StartingEquity: start}
	open, _ := p.store.OpenTrades(accID)
	v.Open = make([]OpenPositionView, 0, len(open))
	for _, t := range open {
		cur := t.Entry
		if pr, ok := Shared.Price(t.Symbol); ok && pr > 0 {
			cur = pr
		}
		up := (cur - t.Entry) * t.Quantity
		upPct := 0.0
		if t.Entry > 0 {
			upPct = (cur/t.Entry - 1) * 100
		}
		v.Open = append(v.Open, OpenPositionView{
			ID: t.ID, Symbol: t.Symbol, Entry: t.Entry, Current: cur,
			Stop: t.Stop, Target: t.Target, Quantity: t.Quantity, Notional: t.Notional,
			RiskUSD: t.RiskUSD, ATRPct: t.ATRPct, Unrealized: up, UnrealizedPct: upPct, OpenedAt: t.OpenedAt,
		})
	}
	v.OpenCount = len(v.Open)
	v.Recent, _ = p.store.RecentTrades(accID, 50)
	v.Realized, _ = p.store.RealizedTotal(accID)
	n, wins, winSum, lossSum, _ := p.store.ClosedStats(accID)
	v.Trades, v.Wins = n, wins
	if n > 0 {
		v.WinRate = float64(wins) / float64(n)
	}
	if wins > 0 {
		v.AvgWin = winSum / float64(wins)
	}
	if nl := n - wins; nl > 0 {
		v.AvgLoss = lossSum / float64(nl)
	}
	if lossSum != 0 {
		v.ProfitFactor = winSum / math.Abs(lossSum)
	}
	series, _ := p.store.EquitySeries(accID, 240)
	for i, j := 0, len(series)-1; i < j; i, j = i+1, j-1 {
		series[i], series[j] = series[j], series[i]
	}
	if series == nil {
		series = []store.ExplosivePaperEquity{}
	}
	v.EquitySeries = series
	if len(series) > 0 {
		last := series[len(series)-1]
		v.Equity, v.Unrealized, v.UpdatedAt = last.Equity, last.Unrealized, last.TS
	} else {
		v.Equity = start
	}
	if v.Recent == nil {
		v.Recent = []store.ExplosivePaperTrade{}
	}
	v.ReturnPct = (v.Equity/start - 1) * 100
	v.Config = map[string]float64{
		"risk_per_trade_pct": p.cfg.RiskPerTrade * 100,
		"stop_atr":           p.cfg.StopATR,
		"target_atr":         p.cfg.TargetATR,
		"max_positions":      float64(p.cfg.MaxPositions),
		"min_vol_spike":      p.cfg.MinVolSpike,
		"time_stop_hours":    p.cfg.TimeStopHours,
	}
	return v
}

// Accounts lists the default account plus every enabled configured account with
// a compact summary, so the UI can show an account switcher.
func (p *PaperTrader) Accounts() []AccountSummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []AccountSummary
	for _, a := range p.accountList() {
		equity := a.start
		if series, _ := p.store.EquitySeries(a.id, 1); len(series) > 0 {
			equity = series[0].Equity
		}
		n, wins, _, _, _ := p.store.ClosedStats(a.id)
		open, _ := p.store.OpenTrades(a.id)
		winRate := 0.0
		if n > 0 {
			winRate = float64(wins) / float64(n)
		}
		exID := ""
		out = append(out, AccountSummary{
			ID: a.id, Name: a.name, ExchangeID: exID, StartingEquity: a.start,
			Equity: equity, ReturnPct: (equity/a.start - 1) * 100,
			Trades: n, WinRate: winRate, OpenCount: len(open),
		})
	}
	return out
}

// AddAccount registers a new paper account linked to a configured exchange.
func (p *PaperTrader) AddAccount(exchangeID, name string, startEquity float64) (int64, error) {
	if startEquity <= 0 {
		startEquity = p.cfg.DefaultEquity
	}
	a := &store.ExplosivePaperAccount{
		ExchangeID: exchangeID, Name: name, StartingEquity: startEquity,
		Enabled: true, CreatedAt: time.Now().UnixMilli(),
	}
	if err := p.store.AddAccount(a); err != nil {
		return 0, err
	}
	return a.ID, nil
}

// RemoveAccount deletes a paper account (its trades/equity stay for history).
func (p *PaperTrader) RemoveAccount(id int64) error {
	if id == 0 {
		return nil
	}
	return p.store.RemoveAccount(id)
}
