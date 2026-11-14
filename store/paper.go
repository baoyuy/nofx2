package store

import "gorm.io/gorm"

// ExplosivePaperTrade is one simulated (paper) trade the board's rule engine
// opened. No real money is involved; fills use the live price and a taker fee,
// so the record measures whether the rule actually has an edge.
type ExplosivePaperTrade struct {
	ID     int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Symbol string `gorm:"column:symbol;index" json:"symbol"`
	Side   string `gorm:"column:side" json:"side"` // long

	OpenedAt int64   `gorm:"column:opened_at;index" json:"opened_at"`
	Entry    float64 `gorm:"column:entry_price" json:"entry_price"`
	Quantity float64 `gorm:"column:quantity" json:"quantity"`
	Notional float64 `gorm:"column:notional" json:"notional"`
	Stop     float64 `gorm:"column:stop_price" json:"stop_price"`
	Target   float64 `gorm:"column:target_price" json:"target_price"`
	ATRPct   float64 `gorm:"column:atr_pct" json:"atr_pct"`
	RiskUSD  float64 `gorm:"column:risk_usd" json:"risk_usd"`
	// Signal snapshot at entry, for later analysis.
	VolSpike float64 `gorm:"column:vol_spike" json:"vol_spike"`
	OIPct    float64 `gorm:"column:oi_pct" json:"oi_pct"`
	DayGain  float64 `gorm:"column:day_gain" json:"day_gain"`

	Status     string  `gorm:"column:status;index" json:"status"` // open | closed
	ClosedAt   int64   `gorm:"column:closed_at" json:"closed_at"`
	ExitPrice  float64 `gorm:"column:exit_price" json:"exit_price"`
	ExitReason string  `gorm:"column:exit_reason" json:"exit_reason"` // stop | target | time
	Fee        float64 `gorm:"column:fee" json:"fee"`
	Realized   float64 `gorm:"column:realized_pnl" json:"realized_pnl"`
}

// ExplosivePaperEquity is one snapshot of the paper account.
type ExplosivePaperEquity struct {
	ID         int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TS         int64   `gorm:"column:ts;index" json:"ts"`
	Equity     float64 `gorm:"column:equity" json:"equity"`
	Realized   float64 `gorm:"column:realized" json:"realized"`
	Unrealized float64 `gorm:"column:unrealized" json:"unrealized"`
	OpenCount  int     `gorm:"column:open_count" json:"open_count"`
}

// PaperStore persists the explosive board's paper trades and equity.
type PaperStore struct{ db *gorm.DB }

func NewPaperStore(db *gorm.DB) *PaperStore { return &PaperStore{db: db} }

func (s *PaperStore) AutoMigrate() error {
	return s.db.AutoMigrate(&ExplosivePaperTrade{}, &ExplosivePaperEquity{})
}

func (s *PaperStore) OpenTrades() ([]ExplosivePaperTrade, error) {
	var out []ExplosivePaperTrade
	err := s.db.Where("status = ?", "open").Find(&out).Error
	return out, err
}

func (s *PaperStore) CreateTrade(t *ExplosivePaperTrade) error { return s.db.Create(t).Error }

func (s *PaperStore) SaveTrade(t *ExplosivePaperTrade) error { return s.db.Save(t).Error }

func (s *PaperStore) RecentTrades(limit int) ([]ExplosivePaperTrade, error) {
	var out []ExplosivePaperTrade
	err := s.db.Order("opened_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *PaperStore) CreateEquity(e *ExplosivePaperEquity) error { return s.db.Create(e).Error }

func (s *PaperStore) EquitySeries(limit int) ([]ExplosivePaperEquity, error) {
	var out []ExplosivePaperEquity
	err := s.db.Order("ts DESC").Limit(limit).Find(&out).Error
	return out, err
}

// RealizedTotal sums realised PnL of closed paper trades.
func (s *PaperStore) RealizedTotal() (float64, error) {
	var sum float64
	err := s.db.Model(&ExplosivePaperTrade{}).Where("status = ?", "closed").
		Select("COALESCE(SUM(realized_pnl),0)").Scan(&sum).Error
	return sum, err
}

// ClosedStats returns count, wins, and gross win/loss sums for closed trades.
func (s *PaperStore) ClosedStats() (n, wins int, winSum, lossSum float64, err error) {
	var rows []ExplosivePaperTrade
	if err = s.db.Where("status = ?", "closed").Find(&rows).Error; err != nil {
		return
	}
	n = len(rows)
	for _, r := range rows {
		if r.Realized >= 0 {
			wins++
			winSum += r.Realized
		} else {
			lossSum += r.Realized
		}
	}
	return
}
