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
// account. No real money: fills use the live price and a taker fee, so the
// record shows whether the rule has an edge after costs.
type PaperConfig struct {
	Label          string
	StartingEquity float64       // virtual USDT
	RiskPerTrade   float64       // fraction of equity risked per trade (0.005 = 0.5%)
	StopATR        float64       // stop distance in ATR (primary, from board ATR%)
	TargetATR      float64       // take-profit distance in ATR
	MaxPositions   int           // concurrent open trades
	MaxNotionalX   float64       // cap notional per trade at this × equity
	FeeRate        float64       // taker fee per side
	TimeStopHours  float64       // close after this many hours if neither stop nor target
	Tick           time.Duration // evaluation cadence
	// Entry gates, straight from the board's live features.
	MinVolSpike  float64
	NeedOIRising bool
	MaxDayGain   float64
}

// DefaultPaperConfig is a conservative short-term long-only rule.
func DefaultPaperConfig() PaperConfig {
	return PaperConfig{
		Label: "爆发榜·规则模拟盘", StartingEquity: 1000, RiskPerTrade: 0.005,
		StopATR: 1.5, TargetATR: 3.0, MaxPositions: 3, MaxNotionalX: 0.5,
		FeeRate: 0.0005, TimeStopHours: 4, Tick: 60 * time.Second,
		MinVolSpike: 3, NeedOIRising: true, MaxDayGain: 0.5,
	}
}

// PaperTrader runs the rule engine against the live board.
type PaperTrader struct {
	cfg   PaperConfig
	store *store.PaperStore
	mu    sync.Mutex
	last  ExplosivePaperView
}

// NewPaperTrader builds a paper trader over the given storage.
func NewPaperTrader(st *store.PaperStore, cfg PaperConfig) *PaperTrader {
	return &PaperTrader{cfg: cfg, store: st}
}

// StartPaper migrates storage, runs the engine until ctx ends, and returns it.
func StartPaper(ctx context.Context, st *store.PaperStore) *PaperTrader {
	if err := st.AutoMigrate(); err != nil {
		logger.Warnf("explosive paper: migrate failed: %v", err)
		return nil
	}
	p := NewPaperTrader(st, DefaultPaperConfig())
	logger.Infof("📝 Explosive paper engine started: equity=%.0f risk=%.2f%% stop=%.1fATR target=%.1fATR",
		p.cfg.StartingEquity, p.cfg.RiskPerTrade*100, p.cfg.StopATR, p.cfg.TargetATR)
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
			return v
		}
		if v, ok := Shared.Price(sym); ok {
			return v
		}
		return 0
	}

	now := time.Now()
	open, _ := p.store.OpenTrades()
	realized, _ := p.store.RealizedTotal()
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

	equity := p.cfg.StartingEquity + realized + unrealized - openEntryFees

	// Entries from the board's ambush rows (already ranked by expected final).
	openCount := len(stillOpen)
	for _, r := range board.Rows {
		if openCount >= p.cfg.MaxPositions {
			break
		}
		if stillOpen[r.Symbol] {
			continue
		}
		if !p.entryOK(r) {
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
			Symbol: r.Symbol, Side: "long", OpenedAt: now.UnixMilli(),
			Entry: r.Price, Quantity: qty, Notional: notional,
			Stop: r.Price * (1 - stopDist), Target: r.Price * (1 + p.cfg.TargetATR*atrFrac),
			ATRPct: r.ATRPct, RiskUSD: notional * stopDist, Fee: fee,
			VolSpike: r.VolSpike1h, OIPct: r.OIChange1h, DayGain: r.DayGain, Status: "open",
		}
		if err := p.store.CreateTrade(t); err != nil {
			logger.Warnf("explosive paper: create trade %s: %v", r.Symbol, err)
			continue
		}
		stillOpen[r.Symbol] = true
		openCount++
		openEntryFees += fee
		logger.Infof("📝 paper open %s @%.6f stop=%.6f target=%.6f risk=%.1fUSD", r.Symbol, t.Entry, t.Stop, t.Target, t.RiskUSD)
	}

	equity = p.cfg.StartingEquity + realized + unrealized - openEntryFees
	_ = p.store.CreateEquity(&store.ExplosivePaperEquity{
		TS: now.UnixMilli(), Equity: equity, Realized: realized,
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

// ExplosivePaperView is the read model the API returns.
type ExplosivePaperView struct {
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
	Open           []store.ExplosivePaperTrade  `json:"open"`
	Recent         []store.ExplosivePaperTrade  `json:"recent"`
	EquitySeries   []store.ExplosivePaperEquity `json:"equity_series"`
	Config         map[string]float64           `json:"config"`
	UpdatedAt      int64                        `json:"updated_at"`
}

// Snapshot assembles the current paper account state for the API.
func (p *PaperTrader) Snapshot() ExplosivePaperView {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := ExplosivePaperView{Label: p.cfg.Label, StartingEquity: p.cfg.StartingEquity}
	open, _ := p.store.OpenTrades()
	v.Open = open
	v.OpenCount = len(open)
	v.Recent, _ = p.store.RecentTrades(50)
	v.Realized, _ = p.store.RealizedTotal()
	n, wins, winSum, lossSum, _ := p.store.ClosedStats()
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
	series, _ := p.store.EquitySeries(240)
	// reverse to chronological
	for i, j := 0, len(series)-1; i < j; i, j = i+1, j-1 {
		series[i], series[j] = series[j], series[i]
	}
	v.EquitySeries = series
	if len(series) > 0 {
		last := series[len(series)-1]
		v.Equity, v.Unrealized, v.UpdatedAt = last.Equity, last.Unrealized, last.TS
	} else {
		v.Equity = p.cfg.StartingEquity
	}
	if v.Open == nil {
		v.Open = []store.ExplosivePaperTrade{}
	}
	if v.Recent == nil {
		v.Recent = []store.ExplosivePaperTrade{}
	}
	if v.EquitySeries == nil {
		v.EquitySeries = []store.ExplosivePaperEquity{}
	}
	v.ReturnPct = (v.Equity/p.cfg.StartingEquity - 1) * 100
	v.Config = map[string]float64{
		"risk_per_trade_pct": p.cfg.RiskPerTrade * 100,
		"stop_atr":           p.cfg.StopATR,
		"target_atr":         p.cfg.TargetATR,
		"max_positions":      float64(p.cfg.MaxPositions),
		"min_vol_spike":      p.cfg.MinVolSpike,
		"time_stop_hours":    p.cfg.TimeStopHours,
	}
	p.last = v
	return v
}
