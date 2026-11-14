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
	DefaultEquity     float64
	RiskPerTrade      float64
	StopATR           float64
	TargetATR         float64
	MaxPositions      int
	MaxNotionalX      float64
	FeeRate           float64
	TimeStopHours     float64
	Tick              time.Duration
	Leverage          float64
	SlippageBps       float64
	MaxVolumePct      float64
	MaxGrossX         float64
	DailyLossLimitPct float64
	CooldownMin       float64
	TrailStartATR     float64
	TrailATR          float64
	MinVolSpike       float64
	GainMin           float64
	GainMax           float64
	OIRequired        bool
	AllowShort        bool
	ShortMinGain      float64
	PartialTP         bool
	TP1ATR            float64
	TP1Fraction       float64
}

// DefaultPaperConfig is a conservative short-term long-only rule.
func DefaultPaperConfig() PaperConfig {
	return PaperConfig{
		DefaultEquity: 1000, RiskPerTrade: 0.005,
		StopATR: 1.5, TargetATR: 3.0, MaxPositions: 3, MaxNotionalX: 0.5,
		FeeRate: 0.0005, TimeStopHours: 4, Tick: 60 * time.Second,
		MinVolSpike: 3, Leverage: 5, SlippageBps: 5, MaxVolumePct: 0.01, MaxGrossX: 1.0,
		DailyLossLimitPct: 3, CooldownMin: 30, TrailStartATR: 1.0, TrailATR: 0.8,
		GainMin: 0.02, GainMax: 0.30, OIRequired: false,
		AllowShort: false, ShortMinGain: 0.15, PartialTP: true, TP1ATR: 1.5, TP1Fraction: 0.5,
	}
}

// PaperTrader runs the rule engine for the built-in default account plus every
// enabled configured account.
type PaperTrader struct {
	cfg        PaperConfig
	store      *store.PaperStore
	mu         sync.Mutex
	lastPrices map[string]float64
	lastTick   time.Time
	halted     string
}

func NewPaperTrader(st *store.PaperStore, cfg PaperConfig) *PaperTrader {
	return &PaperTrader{cfg: cfg, store: st, lastPrices: make(map[string]float64)}
}

// overlaySettings applies saved settings on top of a base config.
func overlaySettings(base PaperConfig, s store.ExplosivePaperSettings) PaperConfig {
	if s.RiskPct <= 0 {
		return base
	}
	base.RiskPerTrade = s.RiskPct / 100
	set := func(dst *float64, v float64) {
		if v > 0 {
			*dst = v
		}
	}
	set(&base.StopATR, s.StopATR)
	set(&base.TargetATR, s.TargetATR)
	set(&base.MaxNotionalX, s.MaxNotionalX)
	set(&base.MinVolSpike, s.MinVolSpike)
	set(&base.TimeStopHours, s.TimeStopHours)
	set(&base.Leverage, s.Leverage)
	set(&base.SlippageBps, s.SlippageBps)
	set(&base.MaxVolumePct, s.MaxVolumePct)
	set(&base.MaxGrossX, s.MaxGrossX)
	set(&base.DailyLossLimitPct, s.DailyLossLimitPct)
	set(&base.CooldownMin, s.CooldownMin)
	set(&base.TrailStartATR, s.TrailStartATR)
	set(&base.TrailATR, s.TrailATR)
	if s.ShortMinGain > 0 {
		base.ShortMinGain = s.ShortMinGain / 100
	}
	set(&base.TP1ATR, s.TP1ATR)
	if s.TP1Fraction > 0 && s.TP1Fraction <= 1 {
		base.TP1Fraction = s.TP1Fraction
	}
	if s.MaxPositions > 0 {
		base.MaxPositions = s.MaxPositions
	}
	if s.GainMinPct > 0 {
		base.GainMin = s.GainMinPct / 100
	}
	if s.GainMaxPct > 0 {
		base.GainMax = s.GainMaxPct / 100
	}
	base.OIRequired = s.OIRequired
	base.AllowShort = s.AllowShort
	base.PartialTP = s.PartialTP
	return base
}

// cfgFor returns the effective config for one account: its own settings, else
// the default account's, else the built-in defaults.
func (p *PaperTrader) cfgFor(accID int64) PaperConfig {
	if s, err := p.store.GetSettings(accID); err == nil && s.RiskPct > 0 {
		return overlaySettings(p.cfg, s)
	}
	if accID != 0 {
		if s, err := p.store.GetSettings(0); err == nil && s.RiskPct > 0 {
			return overlaySettings(p.cfg, s)
		}
	}
	return p.cfg
}

// Settings returns the effective rule settings for one account.
func (p *PaperTrader) Settings(accID int64) store.ExplosivePaperSettings {
	s, _ := p.store.GetSettings(accID)
	if s.RiskPct <= 0 && accID != 0 {
		if g, err := p.store.GetSettings(0); err == nil && g.RiskPct > 0 {
			s = g
		}
	}
	cfg := p.cfgFor(accID)
	if s.RiskPct <= 0 {
		s = store.ExplosivePaperSettings{}
	}
	if s.RiskPct <= 0 {
		s.RiskPct = cfg.RiskPerTrade * 100
	}
	if s.StopATR <= 0 {
		s.StopATR = cfg.StopATR
	}
	if s.TargetATR <= 0 {
		s.TargetATR = cfg.TargetATR
	}
	if s.MaxPositions <= 0 {
		s.MaxPositions = cfg.MaxPositions
	}
	if s.MaxNotionalX <= 0 {
		s.MaxNotionalX = cfg.MaxNotionalX
	}
	if s.MinVolSpike <= 0 {
		s.MinVolSpike = cfg.MinVolSpike
	}
	if s.TimeStopHours <= 0 {
		s.TimeStopHours = cfg.TimeStopHours
	}
	if s.Leverage <= 0 {
		s.Leverage = cfg.Leverage
	}
	if s.SlippageBps <= 0 {
		s.SlippageBps = cfg.SlippageBps
	}
	if s.MaxVolumePct <= 0 {
		s.MaxVolumePct = cfg.MaxVolumePct
	}
	if s.MaxGrossX <= 0 {
		s.MaxGrossX = cfg.MaxGrossX
	}
	if s.DailyLossLimitPct <= 0 {
		s.DailyLossLimitPct = cfg.DailyLossLimitPct
	}
	if s.CooldownMin <= 0 {
		s.CooldownMin = cfg.CooldownMin
	}
	if s.TrailStartATR <= 0 {
		s.TrailStartATR = cfg.TrailStartATR
	}
	if s.TrailATR <= 0 {
		s.TrailATR = cfg.TrailATR
	}
	if s.ShortMinGain <= 0 {
		s.ShortMinGain = cfg.ShortMinGain * 100
	}
	if s.TP1ATR <= 0 {
		s.TP1ATR = cfg.TP1ATR
	}
	if s.TP1Fraction <= 0 {
		s.TP1Fraction = cfg.TP1Fraction
	}
	if s.GainMinPct <= 0 {
		s.GainMinPct = cfg.GainMin * 100
	}
	if s.GainMaxPct <= 0 {
		s.GainMaxPct = cfg.GainMax * 100
	}
	s.AccountID = accID
	return s
}

// UpdateSettings saves one account's rule settings and applies them.
func (p *PaperTrader) UpdateSettings(accID int64, v store.ExplosivePaperSettings) store.ExplosivePaperSettings {
	v.ID, v.AccountID = 0, accID
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
	if v.Leverage < 1 {
		v.Leverage = 1
	}
	if v.Leverage > 20 {
		v.Leverage = 20
	}
	if v.SlippageBps < 0 {
		v.SlippageBps = 0
	}
	if v.SlippageBps > 100 {
		v.SlippageBps = 100
	}
	if v.MaxVolumePct <= 0 {
		v.MaxVolumePct = 0.01
	}
	if v.MaxVolumePct > 1 {
		v.MaxVolumePct = 1
	}
	if v.MaxGrossX <= 0 {
		v.MaxGrossX = 1
	}
	if v.MaxGrossX > 10 {
		v.MaxGrossX = 10
	}
	if v.DailyLossLimitPct <= 0 {
		v.DailyLossLimitPct = 3
	}
	if v.DailyLossLimitPct > 50 {
		v.DailyLossLimitPct = 50
	}
	if v.CooldownMin < 0 {
		v.CooldownMin = 0
	}
	if v.CooldownMin > 1440 {
		v.CooldownMin = 1440
	}
	if v.TrailStartATR <= 0 {
		v.TrailStartATR = 1
	}
	if v.TrailATR <= 0 {
		v.TrailATR = 0.8
	}
	if v.GainMinPct < 0 {
		v.GainMinPct = 0
	}
	if v.GainMaxPct <= v.GainMinPct {
		v.GainMaxPct = v.GainMinPct + 5
	}
	if v.ShortMinGain < 0 {
		v.ShortMinGain = 0
	}
	if v.TP1ATR <= 0 {
		v.TP1ATR = 1.5
	}
	if v.TP1Fraction <= 0 || v.TP1Fraction > 1 {
		v.TP1Fraction = 0.5
	}
	v.UpdatedAt = time.Now().UnixMilli()
	_ = p.store.SaveSettings(&v)
	return p.Settings(accID)
}

// StartPaper migrates storage, runs the engine until ctx ends, and returns it.
func StartPaper(ctx context.Context, st *store.PaperStore) *PaperTrader {
	if err := st.AutoMigrate(); err != nil {
		logger.Warnf("explosive paper: migrate failed: %v", err)
		return nil
	}
	p := NewPaperTrader(st, DefaultPaperConfig())
	logger.Infof("📝 Explosive paper engine started: equity=%.0f risk=%.2f%% stop=%.1fATR target=%.1fATR",
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

func (p *PaperTrader) tick(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	board, err := Shared.Board(ctx, false)
	if err != nil {
		return
	}
	pm := p.priceMap(board)
	priceOf := func(sym string) float64 {
		if v, ok := pm[sym]; ok && v > 0 {
			p.lastPrices[sym] = v
			return v
		}
		if v, ok := Shared.Price(sym); ok && v > 0 {
			p.lastPrices[sym] = v
			return v
		}
		return 0
	}
	now := time.Now()
	for _, acc := range p.accountList() {
		p.processAccount(acc, board, priceOf, now)
	}
	p.lastTick = now
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

func (p *PaperTrader) processAccount(acc paperAccount, board Board, priceOf func(string) float64, now time.Time) {
	cfg := p.cfgFor(acc.id)
	lev := cfg.Leverage
	if lev < 1 {
		lev = 1
	}
	slip := cfg.SlippageBps / 10000
	realized, _ := p.store.RealizedTotal(acc.id)
	open, _ := p.store.OpenTrades(acc.id)
	openEntryFees := 0.0
	unrealized := 0.0
	gross := 0.0
	stillOpen := make(map[string]bool, len(open))

	for i := range open {
		t := &open[i]
		pr := priceOf(t.Symbol)
		if pr <= 0 {
			if cached, ok := p.lastPrices[t.Symbol]; ok && cached > 0 {
				pr = cached
			} else {
				stillOpen[t.Symbol] = true
				gross += t.Notional
				continue
			}
		}
		isShort := t.Side == "short"
		atrFrac := t.ATRPct / 100
		if t.Extreme <= 0 {
			t.Extreme = t.Entry
		}
		if isShort {
			if pr < t.Extreme {
				t.Extreme = pr
			}
		} else if pr > t.Extreme {
			t.Extreme = pr
		}
		// Trailing stop.
		if atrFrac > 0 {
			if isShort {
				if (1 - t.Extreme/t.Entry) >= cfg.TrailStartATR*atrFrac {
					if ns := t.Extreme * (1 + cfg.TrailATR*atrFrac); ns < t.Stop {
						t.Stop = ns
						_ = p.store.SaveTrade(t)
					}
				}
			} else if (t.Extreme/t.Entry - 1) >= cfg.TrailStartATR*atrFrac {
				if ns := t.Extreme * (1 - cfg.TrailATR*atrFrac); ns > t.Stop {
					t.Stop = ns
					_ = p.store.SaveTrade(t)
				}
			}
		}
		liq := t.Entry * (1 - 1/lev)
		if isShort {
			liq = t.Entry * (1 + 1/lev)
		}

		// Partial take-profit at TP1: close a fraction and move the stop to
		// breakeven; the rest runs to TP2 (Target).
		if cfg.PartialTP && !t.PartialTaken && t.TP1 > 0 && t.Quantity > 0 {
			hit := (!isShort && pr >= t.TP1) || (isShort && pr <= t.TP1)
			if hit {
				initial := t.InitialQuantity
				if initial <= 0 {
					initial = t.Quantity // legacy positions opened before the field existed
				}
				qtyClose := initial * cfg.TP1Fraction
				if qtyClose > t.Quantity {
					qtyClose = t.Quantity
				}
				feeShare := t.Fee * cfg.TP1Fraction
				pnl := (t.TP1 - t.Entry) * qtyClose
				if isShort {
					pnl = (t.Entry - t.TP1) * qtyClose
				}
				pnl -= feeShare + qtyClose*t.TP1*cfg.FeeRate
				t.PartialRealized += pnl
				t.Fee -= feeShare
				t.Quantity -= qtyClose
				t.PartialTaken = true
				if isShort {
					if t.Stop > t.Entry {
						t.Stop = t.Entry
					}
				} else if t.Stop < t.Entry {
					t.Stop = t.Entry
				}
				_ = p.store.SaveTrade(t)
				realized += pnl
			}
		}

		exit := ""
		fill := 0.0
		if isShort {
			switch {
			case pr >= liq:
				exit, fill = "liquidated", liq*(1+slip)
			case pr >= t.Stop:
				exit = "stop"
				if pr > t.Stop {
					fill = pr
				} else {
					fill = t.Stop
				}
				fill *= 1 + slip
			case pr <= t.Target:
				exit, fill = "target", t.Target
			case now.Sub(time.UnixMilli(t.OpenedAt)).Hours() >= cfg.TimeStopHours:
				exit, fill = "time", pr*(1+slip)
			}
		} else {
			switch {
			case pr <= liq:
				exit, fill = "liquidated", liq*(1-slip)
			case pr <= t.Stop:
				exit = "stop"
				if pr < t.Stop {
					fill = pr
				} else {
					fill = t.Stop
				}
				fill *= 1 - slip
			case pr >= t.Target:
				exit, fill = "target", t.Target
			case now.Sub(time.UnixMilli(t.OpenedAt)).Hours() >= cfg.TimeStopHours:
				exit, fill = "time", pr*(1-slip)
			}
		}
		if exit != "" && t.Quantity > 0 {
			exitFee := t.Quantity * fill * cfg.FeeRate
			pnl := (fill-t.Entry)*t.Quantity - t.Fee - exitFee
			if isShort {
				pnl = (t.Entry-fill)*t.Quantity - t.Fee - exitFee
			}
			t.Status, t.ClosedAt, t.ExitPrice, t.ExitReason = "closed", now.UnixMilli(), fill, exit
			t.Fee += exitFee
			t.Realized = pnl
			_ = p.store.SaveTrade(t)
			realized += pnl
			continue
		}
		stillOpen[t.Symbol] = true
		gross += t.Notional
		openEntryFees += t.Fee
		u := (pr - t.Entry) * t.Quantity
		if isShort {
			u = (t.Entry - pr) * t.Quantity
		}
		unrealized += u
	}

	equity := acc.start + realized + unrealized - openEntryFees

	halted := ""
	if cfg.DailyLossLimitPct > 0 {
		dayStart := startOfDayMs(now)
		realizedToday, _ := p.store.RealizedSince(acc.id, dayStart)
		limit := acc.start * cfg.DailyLossLimitPct / 100
		if realizedToday+unrealized-openEntryFees <= -limit {
			halted = "daily_loss_limit"
		}
	}
	p.halted = halted

	openCount := len(stillOpen)
	for _, r := range board.Rows {
		if halted != "" || openCount >= cfg.MaxPositions {
			break
		}
		if stillOpen[r.Symbol] {
			continue
		}
		side := ""
		if p.entryOK(r, cfg) {
			side = "long"
		} else if cfg.AllowShort && r.DayGain >= cfg.ShortMinGain && r.Change5m < 0 && r.Price > 0 {
			side = "short"
		}
		if side == "" {
			continue
		}
		if cfg.CooldownMin > 0 {
			if last, _ := p.store.LastClosedAt(acc.id, r.Symbol); last > 0 &&
				now.Sub(time.UnixMilli(last)).Minutes() < cfg.CooldownMin {
				continue
			}
		}
		atrFrac := r.ATRPct / 100
		if atrFrac <= 0 || r.Price <= 0 {
			continue
		}
		stopDist := cfg.StopATR * atrFrac
		if stopDist <= 0 || stopDist >= 1 {
			continue
		}
		entry := r.Price * (1 + slip)
		if side == "short" {
			entry = r.Price * (1 - slip)
		}
		notional := (equity * cfg.RiskPerTrade) / stopDist
		if capN := equity * cfg.MaxNotionalX; notional > capN {
			notional = capN
		}
		if r.Volume24h > 0 && cfg.MaxVolumePct > 0 {
			if capV := r.Volume24h * cfg.MaxVolumePct; notional > capV {
				notional = capV
			}
		}
		if capG := equity*cfg.MaxGrossX - gross; notional > capG {
			notional = capG
		}
		if notional < 10 {
			continue
		}
		qty := notional / entry
		fee := notional * cfg.FeeRate
		stop := entry * (1 - stopDist)
		target := entry * (1 + cfg.TargetATR*atrFrac)
		tp1 := entry * (1 + cfg.TP1ATR*atrFrac)
		if side == "short" {
			stop = entry * (1 + stopDist)
			target = entry * (1 - cfg.TargetATR*atrFrac)
			tp1 = entry * (1 - cfg.TP1ATR*atrFrac)
		}
		t := &store.ExplosivePaperTrade{
			AccountID: acc.id, Symbol: r.Symbol, Side: side, OpenedAt: now.UnixMilli(),
			Entry: entry, Quantity: qty, InitialQuantity: qty, Notional: notional,
			Stop: stop, Target: target, TP1: tp1,
			ATRPct: r.ATRPct, RiskUSD: notional * stopDist, Fee: fee, Extreme: entry,
			VolSpike: r.VolSpike1h, OIPct: r.OIChange1h, DayGain: r.DayGain, Status: "open",
		}
		if err := p.store.CreateTrade(t); err != nil {
			logger.Warnf("explosive paper: create trade %s/%s: %v", acc.name, r.Symbol, err)
			continue
		}
		stillOpen[r.Symbol] = true
		openCount++
		gross += notional
		openEntryFees += fee
		logger.Infof("📝 paper[%s] %s %s @%.6f stop=%.6f target=%.6f", acc.name, side, r.Symbol, entry, stop, target)
	}

	equity = acc.start + realized + unrealized - openEntryFees
	_ = p.store.CreateEquity(&store.ExplosivePaperEquity{
		AccountID: acc.id, TS: now.UnixMilli(), Equity: equity, Realized: realized,
		Unrealized: unrealized - openEntryFees, OpenCount: openCount,
	})
}

// startOfDayMs is Beijing midnight in epoch milliseconds.
func startOfDayMs(now time.Time) int64 {
	cst := time.FixedZone("CST", 8*3600)
	t := now.In(cst)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, cst).UnixMilli()
}

func (p *PaperTrader) entryOK(r Row, cfg PaperConfig) bool {
	if r.Price <= 0 || r.ATRPct <= 0 {
		return false
	}
	if r.DayGain < cfg.GainMin || r.DayGain > cfg.GainMax {
		return false
	}
	volOK := r.VolSpike1h >= cfg.MinVolSpike
	oiOK := r.OIKnown && r.OIChange1h > 0
	if cfg.OIRequired {
		return volOK && oiOK
	}
	return volOK || oiOK
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
	LastTick       int64                        `json:"last_tick"`
	StaleCount     int                          `json:"stale_count"`
	Halted         string                       `json:"halted"`
	EngineStale    bool                         `json:"engine_stale"`
	UpdatedAt      int64                        `json:"updated_at"`
}

// OpenPositionView is one live position with its current price and floating PnL.
type OpenPositionView struct {
	ID            int64   `json:"id"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
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
	Margin        float64 `json:"margin"`
	Stale         bool    `json:"stale"`
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
	cfg := p.cfgFor(accID)
	lev := cfg.Leverage
	if lev <= 0 {
		lev = 1
	}
	open, _ := p.store.OpenTrades(accID)
	v.Open = make([]OpenPositionView, 0, len(open))
	liveUnrealized := 0.0
	liveEntryFees := 0.0
	for _, t := range open {
		cur := t.Entry
		stale := true
		if pr, ok := Shared.Price(t.Symbol); ok && pr > 0 {
			cur, stale = pr, false
		} else if pr, ok := p.lastPrices[t.Symbol]; ok && pr > 0 {
			cur, stale = pr, true
		}
		if stale {
			v.StaleCount++
		}
		up := (cur - t.Entry) * t.Quantity
		liveUnrealized += up
		liveEntryFees += t.Fee
		upPct := 0.0
		if t.Entry > 0 {
			upPct = (cur/t.Entry - 1) * 100
		}
		v.Open = append(v.Open, OpenPositionView{
			ID: t.ID, Symbol: t.Symbol, Side: t.Side, Entry: t.Entry, Current: cur,
			Stop: t.Stop, Target: t.Target, Quantity: t.Quantity, Notional: t.Notional,
			RiskUSD: t.RiskUSD, ATRPct: t.ATRPct, Unrealized: up, UnrealizedPct: upPct,
			Margin: t.Notional / lev, Stale: stale, OpenedAt: t.OpenedAt,
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
	// Live figures: the recorded snapshot only updates each engine tick, so
	// recompute against the current prices to match what the position table
	// shows (otherwise equity says profit while the positions are dropping).
	v.Unrealized = liveUnrealized - liveEntryFees
	v.Equity = start + v.Realized + v.Unrealized
	v.UpdatedAt = time.Now().UnixMilli()
	v.Halted = p.halted
	if !p.lastTick.IsZero() {
		v.LastTick = p.lastTick.UnixMilli()
		v.EngineStale = time.Since(p.lastTick) > 3*cfg.Tick
	} else {
		v.EngineStale = true
	}
	if v.Recent == nil {
		v.Recent = []store.ExplosivePaperTrade{}
	}
	v.ReturnPct = (v.Equity/start - 1) * 100
	v.Config = map[string]float64{
		"risk_per_trade_pct": cfg.RiskPerTrade * 100,
		"stop_atr":           cfg.StopATR,
		"target_atr":         cfg.TargetATR,
		"max_positions":      float64(cfg.MaxPositions),
		"min_vol_spike":      cfg.MinVolSpike,
		"time_stop_hours":    cfg.TimeStopHours,
		"leverage":           cfg.Leverage,
		"tp1_atr":            cfg.TP1ATR,
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
		realized, _ := p.store.RealizedTotal(a.id)
		open, _ := p.store.OpenTrades(a.id)
		unrealized := 0.0
		fees := 0.0
		for _, t := range open {
			cur := t.Entry
			if pr, ok := Shared.Price(t.Symbol); ok && pr > 0 {
				cur = pr
			}
			unrealized += (cur - t.Entry) * t.Quantity
			fees += t.Fee
		}
		equity := a.start + realized + unrealized - fees
		n, wins, _, _, _ := p.store.ClosedStats(a.id)
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
