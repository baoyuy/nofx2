package store

import (
	"time"

	"gorm.io/gorm"
)

// CopyTradeFollow is a Binance lead trader on the copy-trade monitor page.
// NOFX is single-user, so follows are not split by user.
type CopyTradeFollow struct {
	PortfolioID  string  `gorm:"column:portfolio_id;primaryKey" json:"portfolio_id"`
	Nickname     string  `gorm:"column:nickname" json:"nickname"`
	AvatarURL    string  `gorm:"column:avatar_url" json:"avatar_url"`
	Status       string  `gorm:"column:status" json:"status"`
	PositionShow bool    `gorm:"column:position_show" json:"position_show"`
	AUM          float64 `gorm:"column:aum" json:"aum"`
	// MarginBalance is the trader's account balance (USDT), to size positions.
	MarginBalance float64 `gorm:"column:margin_balance" json:"margin_balance"`
	// StartTime is when the trader started leading (ms); 0 = unknown.
	StartTime    int64 `gorm:"column:start_time" json:"start_time"`
	CopyCount    int   `gorm:"column:copy_count" json:"copy_count"`
	MaxCopyCount int   `gorm:"column:max_copy_count" json:"max_copy_count"`
	// 30-day results.
	ROI     float64 `gorm:"column:roi" json:"roi"`
	PnL     float64 `gorm:"column:pnl" json:"pnl"`
	MDD     float64 `gorm:"column:mdd" json:"mdd"`
	WinRate float64 `gorm:"column:win_rate" json:"win_rate"`

	// Fills up to this time (ms) are stored; 0 = not backfilled yet.
	FillsUntil int64 `gorm:"column:fills_until" json:"-"`
	// Wall-clock completion time of a full catch-up, never a profile/page write.
	FillsSyncedAt  int64 `gorm:"column:fills_synced_at" json:"-"`
	FillsPending   bool  `gorm:"column:fills_pending" json:"-"`
	ProfileAt      int64 `gorm:"column:profile_at" json:"-"`
	ProfileVersion int   `gorm:"column:profile_version" json:"-"`
	LastTradeTime  int64 `gorm:"column:last_trade_time" json:"last_trade_time"`
	// Private portfolios hide trades and positions, so they never show data;
	// PublicID is the trader's public portfolio, if any.
	Private  bool   `gorm:"column:private" json:"private"`
	PublicID string `gorm:"column:public_id" json:"public_id,omitempty"`
	// Source is "auto" for traders the auto-follow manager picked and may drop,
	// "manual" for ones the user followed, which it never drops. Follows from
	// before auto-follow existed count as auto.
	Source string `gorm:"column:source;default:auto" json:"source"`
	// PickedAt is when the auto-follow manager picked the trader (ms); 0 for
	// manual follows and for those from before auto-follow existed.
	PickedAt int64 `gorm:"column:picked_at" json:"picked_at,omitempty"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"followed_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"-"`
}

func (CopyTradeFollow) TableName() string { return "copytrade_follows" }

// CopyTradeFill is one stored fill of a followed trader.
type CopyTradeFill struct {
	ID           int64   `gorm:"column:id;primaryKey;autoIncrement"`
	PortfolioID  string  `gorm:"column:portfolio_id;not null;index:idx_copytrade_fill,priority:1"`
	Time         int64   `gorm:"column:time;not null;index:idx_copytrade_fill,priority:2"`
	Seq          int     `gorm:"column:seq"`
	Symbol       string  `gorm:"column:symbol;not null"`
	Side         string  `gorm:"column:side"`
	PositionSide string  `gorm:"column:position_side"`
	Qty          float64 `gorm:"column:qty"`
	Price        float64 `gorm:"column:price"`
	Fee          float64 `gorm:"column:fee"`
	RealizedPnL  float64 `gorm:"column:realized_pnl"`
}

func (CopyTradeFill) TableName() string { return "copytrade_fills" }

type CopyTradeStore struct{ db *gorm.DB }

func NewCopyTradeStore(db *gorm.DB) *CopyTradeStore { return &CopyTradeStore{db: db} }

// InitTables creates the copy-trade tables (exported for tests).
func (s *CopyTradeStore) InitTables() error { return s.initTables() }

func (s *CopyTradeStore) initTables() error {
	return s.db.AutoMigrate(&CopyTradeFollow{}, &CopyTradeFill{}, &CopyTradeSignal{},
		&CopyTradeExclusion{}, &CopyTradeAutoLog{})
}

// CopyTradeExclusion keeps a lead trader out of auto-follow until Until (ms;
// 0 = for good): dropped for low quality, or unfollowed by the user.
type CopyTradeExclusion struct {
	PortfolioID string `gorm:"column:portfolio_id;primaryKey" json:"portfolio_id"`
	Nickname    string `gorm:"column:nickname" json:"nickname"`
	// Reason is a comma-separated list of rule codes, or "manual".
	Reason    string `gorm:"column:reason" json:"reason"`
	Until     int64  `gorm:"column:until" json:"until"`
	CreatedAt int64  `gorm:"column:created_at" json:"created_at"`
}

func (CopyTradeExclusion) TableName() string { return "copytrade_exclusions" }

// CopyTradeAutoLog is one action of the auto-follow manager.
type CopyTradeAutoLog struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Time        int64  `gorm:"column:time;index" json:"time"`
	Action      string `gorm:"column:action" json:"action"` // follow | drop | trim
	PortfolioID string `gorm:"column:portfolio_id" json:"portfolio_id"`
	Nickname    string `gorm:"column:nickname" json:"nickname"`
	Reason      string `gorm:"column:reason" json:"reason"`
}

func (CopyTradeAutoLog) TableName() string { return "copytrade_auto_log" }

// Exclude upserts an exclusion.
func (s *CopyTradeStore) Exclude(e CopyTradeExclusion) error { return s.db.Save(&e).Error }

func (s *CopyTradeStore) Unexclude(id string) error {
	return s.db.Delete(&CopyTradeExclusion{}, "portfolio_id = ?", id).Error
}

// Exclusions returns the exclusions still in force at nowMs, newest first.
func (s *CopyTradeStore) Exclusions(nowMs int64) ([]CopyTradeExclusion, error) {
	var out []CopyTradeExclusion
	err := s.db.Where("until = 0 OR until > ?", nowMs).Order("created_at DESC").Find(&out).Error
	return out, err
}

// AddAutoLog records an action and keeps the newest 500.
func (s *CopyTradeStore) AddAutoLog(l CopyTradeAutoLog) error {
	if err := s.db.Create(&l).Error; err != nil {
		return err
	}
	return s.db.Exec("DELETE FROM copytrade_auto_log WHERE id <= (SELECT id FROM copytrade_auto_log ORDER BY id DESC LIMIT 1 OFFSET 500)").Error
}

// AutoLogs returns actions at or after sinceMs, newest first, at most limit.
func (s *CopyTradeStore) AutoLogs(sinceMs int64, limit int) ([]CopyTradeAutoLog, error) {
	var out []CopyTradeAutoLog
	err := s.db.Where("time >= ?", sinceMs).Order("time DESC, id DESC").Limit(limit).Find(&out).Error
	return out, err
}

// CopyTradeSignal is one hourly snapshot of a coin's direction confidence
// among followed traders, and how the price moved after it.
type CopyTradeSignal struct {
	ID         int64   `gorm:"column:id;primaryKey;autoIncrement"`
	Hour       int64   `gorm:"column:hour;not null;uniqueIndex:idx_copytrade_signal,priority:1"` // unix seconds, top of the hour
	Symbol     string  `gorm:"column:symbol;not null;uniqueIndex:idx_copytrade_signal,priority:2"`
	Direction  string  `gorm:"column:direction"`
	Confidence float64 `gorm:"column:confidence"`
	Traders    int     `gorm:"column:traders"`
	// Price is the close of the hour before Hour.
	Price float64 `gorm:"column:price"`
	// Returns are fractional price changes after Hour; Done4h/Done24h mark
	// them evaluated (Ret is nil when no candle was available).
	Ret4h   *float64 `gorm:"column:ret_4h"`
	Ret24h  *float64 `gorm:"column:ret_24h"`
	Done4h  bool     `gorm:"column:done_4h;index"`
	Done24h bool     `gorm:"column:done_24h;index"`
}

func (CopyTradeSignal) TableName() string { return "copytrade_signals" }

// HasSignals tells whether the hour was already recorded.
func (s *CopyTradeStore) HasSignals(hour int64) (bool, error) {
	var n int64
	err := s.db.Model(&CopyTradeSignal{}).Where("hour = ?", hour).Count(&n).Error
	return n > 0, err
}

func (s *CopyTradeStore) SaveSignals(rows []CopyTradeSignal) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.CreateInBatches(rows, 100).Error
}

// PendingSignals returns signals whose 4h or 24h result can be evaluated
// by now (unix seconds).
func (s *CopyTradeStore) PendingSignals(now int64) ([]CopyTradeSignal, error) {
	var out []CopyTradeSignal
	err := s.db.Where("(done_4h = ? AND hour <= ?) OR (done_24h = ? AND hour <= ?)", false, now-4*3600, false, now-24*3600).
		Order("hour").Limit(2000).Find(&out).Error
	return out, err
}

func (s *CopyTradeStore) UpdateSignal(sig *CopyTradeSignal) error {
	return s.db.Model(&CopyTradeSignal{}).Where("id = ?", sig.ID).Updates(map[string]any{
		"ret_4h": sig.Ret4h, "ret_24h": sig.Ret24h, "done_4h": sig.Done4h, "done_24h": sig.Done24h,
	}).Error
}

// SignalsSince returns signals from sinceHour on, oldest first.
func (s *CopyTradeStore) SignalsSince(sinceHour int64) ([]CopyTradeSignal, error) {
	var out []CopyTradeSignal
	err := s.db.Where("hour >= ?", sinceHour).Order("hour").Find(&out).Error
	return out, err
}

func (s *CopyTradeStore) PruneSignals(beforeHour int64) error {
	return s.db.Delete(&CopyTradeSignal{}, "hour < ?", beforeHour).Error
}

func (s *CopyTradeStore) ListFollows() ([]CopyTradeFollow, error) {
	var out []CopyTradeFollow
	err := s.db.Order("created_at").Find(&out).Error
	return out, err
}

func (s *CopyTradeStore) GetFollow(id string) (*CopyTradeFollow, error) {
	var f CopyTradeFollow
	if err := s.db.First(&f, "portfolio_id = ?", id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *CopyTradeStore) CountFollows() (int64, error) {
	var n int64
	err := s.db.Model(&CopyTradeFollow{}).Count(&n).Error
	return n, err
}

// SaveFollow inserts or replaces a follow.
func (s *CopyTradeStore) SaveFollow(f *CopyTradeFollow) error { return s.db.Save(f).Error }

// UpdateFollowProfile never creates a follow or overwrites synchronization
// state. A late response must not revive an unfollowed/re-followed incarnation.
func (s *CopyTradeStore) UpdateFollowProfile(f *CopyTradeFollow) error {
	result := s.db.Model(&CopyTradeFollow{}).Where("portfolio_id = ? AND created_at = ?", f.PortfolioID, f.CreatedAt).
		Updates(map[string]any{
			"nickname": f.Nickname, "avatar_url": f.AvatarURL, "status": f.Status, "position_show": f.PositionShow,
			"aum": f.AUM, "margin_balance": f.MarginBalance, "start_time": f.StartTime,
			"copy_count": f.CopyCount, "max_copy_count": f.MaxCopyCount,
			"roi": f.ROI, "pnl": f.PnL, "mdd": f.MDD, "win_rate": f.WinRate,
			"profile_at": f.ProfileAt, "profile_version": f.ProfileVersion, "last_trade_time": f.LastTradeTime,
			"private": f.Private, "public_id": f.PublicID,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *CopyTradeStore) DeleteFollow(id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&CopyTradeFill{}, "portfolio_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&CopyTradeFollow{}, "portfolio_id = ?", id).Error
	})
}

// Fills returns a trader's fills since sinceMs, oldest first.
func (s *CopyTradeStore) Fills(id string, sinceMs int64) ([]CopyTradeFill, error) {
	var out []CopyTradeFill
	err := s.db.Where("portfolio_id = ? AND time >= ?", id, sinceMs).Order("time, seq DESC").Find(&out).Error
	return out, err
}

// ReplaceFills swaps the fills at or after fromMs for fills, and records
// that fills up to untilMs are stored, in one transaction. syncedAt is supplied
// only after a complete catch-up; partial pages preserve the last completion.
func (s *CopyTradeStore) ReplaceFills(id string, fromMs int64, fills []CopyTradeFill, untilMs int64, syncedAt ...int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&CopyTradeFill{}, "portfolio_id = ? AND time >= ?", id, fromMs).Error; err != nil {
			return err
		}
		if len(fills) > 0 {
			if err := tx.CreateInBatches(fills, 200).Error; err != nil {
				return err
			}
		}
		updates := map[string]any{"fills_until": untilMs, "fills_pending": true}
		if len(syncedAt) > 0 && syncedAt[0] > 0 {
			updates["fills_synced_at"] = syncedAt[0]
			updates["fills_pending"] = false
		}
		return tx.Model(&CopyTradeFollow{}).Where("portfolio_id = ?", id).Updates(updates).Error
	})
}

// PruneFills drops fills older than beforeMs.
func (s *CopyTradeStore) PruneFills(beforeMs int64) error {
	return s.db.Delete(&CopyTradeFill{}, "time < ?", beforeMs).Error
}
