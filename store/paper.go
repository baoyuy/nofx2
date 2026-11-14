package store

import "gorm.io/gorm"

// ExplosivePaperAccount is one simulated account the board's rule engine runs.
// It can reference a configured exchange account (ExchangeID) and carries its
// own starting equity, so several accounts can be compared side by side.
// Account id 0 is the built-in default account.
type ExplosivePaperAccount struct {
	ID             int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ExchangeID     string  `gorm:"column:exchange_id;index" json:"exchange_id"`
	Name           string  `gorm:"column:name" json:"name"`
	StartingEquity float64 `gorm:"column:starting_equity" json:"starting_equity"`
	Enabled        bool    `gorm:"column:enabled;default:true" json:"enabled"`
	CreatedAt      int64   `gorm:"column:created_at" json:"created_at"`
}

// ExplosivePaperTrade is one simulated (paper) trade the board's rule engine
// opened. No real money: fills use the live price and a taker fee.
type ExplosivePaperTrade struct {
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	AccountID int64  `gorm:"column:account_id;index" json:"account_id"`
	Symbol    string `gorm:"column:symbol;index" json:"symbol"`
	Side      string `gorm:"column:side" json:"side"`

	OpenedAt int64   `gorm:"column:opened_at;index" json:"opened_at"`
	Entry    float64 `gorm:"column:entry_price" json:"entry_price"`
	Quantity float64 `gorm:"column:quantity" json:"quantity"`
	Notional float64 `gorm:"column:notional" json:"notional"`
	Stop     float64 `gorm:"column:stop_price" json:"stop_price"`
	Target   float64 `gorm:"column:target_price" json:"target_price"`
	ATRPct   float64 `gorm:"column:atr_pct" json:"atr_pct"`
	RiskUSD  float64 `gorm:"column:risk_usd" json:"risk_usd"`
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

// ExplosivePaperEquity is one snapshot of a paper account.
type ExplosivePaperEquity struct {
	ID         int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	AccountID  int64   `gorm:"column:account_id;index" json:"account_id"`
	TS         int64   `gorm:"column:ts;index" json:"ts"`
	Equity     float64 `gorm:"column:equity" json:"equity"`
	Realized   float64 `gorm:"column:realized" json:"realized"`
	Unrealized float64 `gorm:"column:unrealized" json:"unrealized"`
	OpenCount  int     `gorm:"column:open_count" json:"open_count"`
}

// PaperStore persists the explosive board's paper accounts, trades and equity.
type PaperStore struct{ db *gorm.DB }

func NewPaperStore(db *gorm.DB) *PaperStore { return &PaperStore{db: db} }

func (s *PaperStore) AutoMigrate() error {
	return s.db.AutoMigrate(&ExplosivePaperAccount{}, &ExplosivePaperTrade{}, &ExplosivePaperEquity{}, &ExplosivePaperSettings{})
}

func (s *PaperStore) Accounts() ([]ExplosivePaperAccount, error) {
	var out []ExplosivePaperAccount
	err := s.db.Order("id ASC").Find(&out).Error
	return out, err
}

func (s *PaperStore) AddAccount(a *ExplosivePaperAccount) error { return s.db.Create(a).Error }

func (s *PaperStore) RemoveAccount(id int64) error {
	return s.db.Where("id = ?", id).Delete(&ExplosivePaperAccount{}).Error
}

func (s *PaperStore) OpenTrades(accountID int64) ([]ExplosivePaperTrade, error) {
	var out []ExplosivePaperTrade
	err := s.db.Where("account_id = ? AND status = ?", accountID, "open").Find(&out).Error
	return out, err
}

func (s *PaperStore) CreateTrade(t *ExplosivePaperTrade) error { return s.db.Create(t).Error }
func (s *PaperStore) SaveTrade(t *ExplosivePaperTrade) error   { return s.db.Save(t).Error }

func (s *PaperStore) RecentTrades(accountID int64, limit int) ([]ExplosivePaperTrade, error) {
	var out []ExplosivePaperTrade
	err := s.db.Where("account_id = ?", accountID).Order("opened_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *PaperStore) CreateEquity(e *ExplosivePaperEquity) error { return s.db.Create(e).Error }

func (s *PaperStore) EquitySeries(accountID int64, limit int) ([]ExplosivePaperEquity, error) {
	var out []ExplosivePaperEquity
	err := s.db.Where("account_id = ?", accountID).Order("ts DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *PaperStore) RealizedTotal(accountID int64) (float64, error) {
	var sum float64
	err := s.db.Model(&ExplosivePaperTrade{}).Where("account_id = ? AND status = ?", accountID, "closed").
		Select("COALESCE(SUM(realized_pnl),0)").Scan(&sum).Error
	return sum, err
}

func (s *PaperStore) ClosedStats(accountID int64) (n, wins int, winSum, lossSum float64, err error) {
	var rows []ExplosivePaperTrade
	if err = s.db.Where("account_id = ? AND status = ?", accountID, "closed").Find(&rows).Error; err != nil {
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

// ExplosivePaperSettings is the single row (id=1) of rule parameters the user
// can tune from the 量化 settings panel.
type ExplosivePaperSettings struct {
	ID            int64   `gorm:"column:id;primaryKey" json:"id"`
	RiskPct       float64 `gorm:"column:risk_pct" json:"risk_pct"`             // per-trade risk, percent
	StopATR       float64 `gorm:"column:stop_atr" json:"stop_atr"`             // stop distance in ATR
	TargetATR     float64 `gorm:"column:target_atr" json:"target_atr"`         // take-profit distance in ATR
	MaxPositions  int     `gorm:"column:max_positions" json:"max_positions"`   // concurrent positions
	MaxNotionalX  float64 `gorm:"column:max_notional_x" json:"max_notional_x"` // per-trade notional cap × equity
	MinVolSpike   float64 `gorm:"column:min_vol_spike" json:"min_vol_spike"`   // entry volume-spike gate
	TimeStopHours float64 `gorm:"column:time_stop_hours" json:"time_stop_hours"`
	UpdatedAt     int64   `gorm:"column:updated_at" json:"updated_at"`
}

func (s *PaperStore) GetSettings() (ExplosivePaperSettings, error) {
	var out ExplosivePaperSettings
	err := s.db.Where("id = ?", 1).First(&out).Error
	if err == gorm.ErrRecordNotFound {
		return ExplosivePaperSettings{ID: 1}, nil
	}
	return out, err
}

func (s *PaperStore) SaveSettings(v *ExplosivePaperSettings) error {
	v.ID = 1
	return s.db.Save(v).Error
}
