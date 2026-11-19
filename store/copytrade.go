package store

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	// PerfAt is when the 30-day results were last read (ms).
	PerfAt        int64 `gorm:"column:perf_at" json:"-"`
	LastTradeTime int64 `gorm:"column:last_trade_time" json:"last_trade_time"`
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
	// Rows from before these columns existed: every one was a ranked signal,
	// and its 72h result was never asked for.
	old := s.db.Migrator().HasTable(&CopyTradeSignal{}) && !s.db.Migrator().HasColumn(&CopyTradeSignal{}, "ranked")
	if err := s.db.AutoMigrate(&CopyTradeFollow{}, &CopyTradeFill{}, &CopyTradeSignal{},
		&CopyTradeExclusion{}, &CopyTradeAutoLog{},
		&CopyTradeBoardMember{}, &CopyTradePaperRoster{}, &CopyTradePaperFill{},
		&CopyTradeCoinScore{}, &CopyTradeScoreMemory{}, &CopyTradeScoreOutcome{}); err != nil {
		return err
	}
	if old {
		return s.db.Model(&CopyTradeSignal{}).Where("1 = 1").Updates(map[string]any{"ranked": true, "done_72h": true}).Error
	}
	return nil
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
	// 72h: lead traders hold for days. Rows from before the column existed
	// are marked done by the migration, so they are never re-evaluated.
	Ret72h  *float64 `gorm:"column:ret_72h"`
	Done72h bool     `gorm:"column:done_72h;index"`
	// Bench is BTC's price change over the same hours: a call is only as
	// good as what it added to the market's own move.
	Bench4h  *float64 `gorm:"column:bench_4h"`
	Bench24h *float64 `gorm:"column:bench_24h"`
	Bench72h *float64 `gorm:"column:bench_72h"`

	// What the call was made of, to find which parts carry the edge.
	// Features is 1 on rows recorded with them (older rows have 0 here).
	Features     int     `gorm:"column:features"`
	LongTraders  int     `gorm:"column:long_traders"`
	ShortTraders int     `gorm:"column:short_traders"`
	Openers      int     `gorm:"column:openers"` // fresh calls on the leading side
	Adds         int     `gorm:"column:adds"`    // adds to existing positions on the leading side
	Trapped      int     `gorm:"column:trapped"` // trapped holders on the leading side
	LongShare    float64 `gorm:"column:long_share"`
	// LongShareDelta is the change of LongShare against the hour before; nil
	// when that hour has no row.
	LongShareDelta *float64 `gorm:"column:long_share_delta"`
	HoldHours      float64  `gorm:"column:hold_hours"`    // mean age of the leading side's positions
	EntryGapPct    float64  `gorm:"column:entry_gap_pct"` // price past the leading side's average entry, %
	// The fresh-call reading (copytrade.Shadow), scored next to Confidence.
	ShadowDirection string  `gorm:"column:shadow_direction"`
	ShadowScore     float64 `gorm:"column:shadow_score"`
	ShadowCalls     int     `gorm:"column:shadow_calls"`
	ShadowExits     int     `gorm:"column:shadow_exits"`
	ShadowBurst     int     `gorm:"column:shadow_burst"`
	// The cross-coin score (copytrade.Rank): a second reading recorded next to
	// Confidence and Shadow, so the hit-rate page can tell whether ranking by
	// "is there a trade here" beats ranking by "how one-sided are the holders".
	RankDirection    string  `gorm:"column:rank_direction"`
	RankScore        float64 `gorm:"column:rank_score"`
	RankStrength     float64 `gorm:"column:rank_strength"`
	RankIndependence float64 `gorm:"column:rank_independence"`
	// The per-style reading (copytrade.StyleReadings). Not stored yet: it is
	// measured in backtests first, so it adds no column.
	StyleDirection string  `gorm:"-"`
	StyleScore     float64 `gorm:"-"`
	StyleTraders   int     `gorm:"-"`
	// Ranked: among the hour's top signals by confidence, the set recorded
	// before every coin was. Hit rates of Confidence count only these, so the
	// record stays comparable across the change.
	Ranked bool `gorm:"column:ranked"`
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
	err := s.db.Where("(done_4h = ? AND hour <= ?) OR (done_24h = ? AND hour <= ?) OR (done_72h = ? AND hour <= ?)",
		false, now-4*3600, false, now-24*3600, false, now-72*3600).
		Order("hour").Limit(5000).Find(&out).Error
	return out, err
}

func (s *CopyTradeStore) UpdateSignal(sig *CopyTradeSignal) error {
	return s.db.Model(&CopyTradeSignal{}).Where("id = ?", sig.ID).Updates(map[string]any{
		"ret_4h": sig.Ret4h, "ret_24h": sig.Ret24h, "done_4h": sig.Done4h, "done_24h": sig.Done24h,
		"ret_72h": sig.Ret72h, "done_72h": sig.Done72h,
		"bench_4h": sig.Bench4h, "bench_24h": sig.Bench24h, "bench_72h": sig.Bench72h,
	}).Error
}

// SignalsSince returns signals from sinceHour on, oldest first.
func (s *CopyTradeStore) SignalsSince(sinceHour int64) ([]CopyTradeSignal, error) {
	var out []CopyTradeSignal
	err := s.db.Where("hour >= ?", sinceHour).Order("hour").Find(&out).Error
	return out, err
}

// SignalsAt returns the signals of one hour.
func (s *CopyTradeStore) SignalsAt(hour int64) ([]CopyTradeSignal, error) {
	var out []CopyTradeSignal
	err := s.db.Where("hour = ?", hour).Find(&out).Error
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
			"profile_at": f.ProfileAt, "profile_version": f.ProfileVersion, "perf_at": f.PerfAt,
			"last_trade_time": f.LastTradeTime,
			"private":         f.Private, "public_id": f.PublicID,
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

// SetFollowSource marks a follow as the user's ("manual") or auto-follow's.
func (s *CopyTradeStore) SetFollowSource(id, source string) error {
	return s.db.Model(&CopyTradeFollow{}).Where("portfolio_id = ?", id).
		Updates(map[string]any{"source": source, "picked_at": 0}).Error
}

// DeleteFollowKeepFills removes a follow but leaves its fills, which
// PruneFills removes as they age out.
func (s *CopyTradeStore) DeleteFollowKeepFills(id string) error {
	return s.db.Delete(&CopyTradeFollow{}, "portfolio_id = ?", id).Error
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
	return s.ReplaceFillRange(id, fromMs, 0, fills, untilMs, syncedAt...)
}

// ReplaceFillRange is ReplaceFills limited to fills before beforeMs (0 = no
// upper bound), so a backfill can add one page without rewriting newer ones.
func (s *CopyTradeStore) ReplaceFillRange(id string, fromMs, beforeMs int64, fills []CopyTradeFill, untilMs int64, syncedAt ...int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		del := tx.Where("portfolio_id = ? AND time >= ?", id, fromMs)
		if beforeMs > 0 {
			del = del.Where("time < ?", beforeMs)
		}
		if err := del.Delete(&CopyTradeFill{}).Error; err != nil {
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

// FillTraders lists every portfolio with stored fills, followed or not.
func (s *CopyTradeStore) FillTraders() ([]string, error) {
	var out []string
	err := s.db.Model(&CopyTradeFill{}).Distinct().Order("portfolio_id").Pluck("portfolio_id", &out).Error
	return out, err
}

// PruneFills drops fills older than beforeMs.
func (s *CopyTradeStore) PruneFills(beforeMs int64) error {
	return s.db.Delete(&CopyTradeFill{}, "time < ?", beforeMs).Error
}

// CopyTradeBoardMember tracks one lead trader seen on Binance's 90-day copier
// PnL leaderboard. The board sync follows the top public accounts and records
// private ones (whose trades Binance hides) as observe-only, so the page can
// still show them.
type CopyTradeBoardMember struct {
	PortfolioID string `gorm:"column:portfolio_id;primaryKey" json:"portfolio_id"`
	Nickname    string `gorm:"column:nickname" json:"nickname"`
	AvatarURL   string `gorm:"column:avatar_url" json:"avatar_url"`
	// PortfolioType is PUBLIC, PRIVATE or SIGNAL as the board reported it.
	PortfolioType string `gorm:"column:portfolio_type" json:"portfolio_type"`
	// Rank is the 1-based position on the board when last seen.
	Rank int `gorm:"column:rank" json:"rank"`
	// Followed is true for a public account the sync follows; Observe marks a
	// private account kept for display only.
	Followed bool `gorm:"column:followed" json:"followed"`
	Observe  bool `gorm:"column:observe" json:"observe"`
	// Missed counts consecutive daily scans the trader was absent from the
	// board (only meaningful while Followed).
	Missed int `gorm:"column:missed" json:"missed"`
	// LastSeenAt is when the trader was last present on the board (ms).
	LastSeenAt int64 `gorm:"column:last_seen_at" json:"last_seen_at"`
	// Observe-only display data: last trade time and 30-day results.
	LastTradeTime int64   `gorm:"column:last_trade_time" json:"last_trade_time"`
	ROI30         float64 `gorm:"column:roi30" json:"roi30"`
	PnL30         float64 `gorm:"column:pnl30" json:"pnl30"`
	MDD30         float64 `gorm:"column:mdd30" json:"mdd30"`
	WinRate30     float64 `gorm:"column:win_rate30" json:"win_rate30"`
	// CoinShare is a JSON array of {symbol, ratio} for the trader's 30-day trade
	// mix, best effort (the endpoint may be unavailable).
	CoinShare   string    `gorm:"column:coin_share" json:"coin_share,omitempty"`
	CoinShareAt int64     `gorm:"column:coin_share_at" json:"coin_share_at,omitempty"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"-"`
}

func (CopyTradeBoardMember) TableName() string { return "copytrade_board_members" }

// SaveBoardMember inserts or replaces one board member row.
func (s *CopyTradeStore) SaveBoardMember(m *CopyTradeBoardMember) error {
	return s.db.Save(m).Error
}

// ListBoardMembers returns every tracked board member, ranked first.
func (s *CopyTradeStore) ListBoardMembers() ([]CopyTradeBoardMember, error) {
	var out []CopyTradeBoardMember
	err := s.db.Order("observe, rank").Find(&out).Error
	return out, err
}

// BoardMember returns one tracked member or gorm.ErrRecordNotFound.
func (s *CopyTradeStore) BoardMember(id string) (*CopyTradeBoardMember, error) {
	var m CopyTradeBoardMember
	if err := s.db.First(&m, "portfolio_id = ?", id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteBoardMember forgets one tracked board member.
func (s *CopyTradeStore) DeleteBoardMember(id string) error {
	return s.db.Delete(&CopyTradeBoardMember{}, "portfolio_id = ?", id).Error
}

// CopyTradePaperRoster is one fixed roster of paper-copy recording: the set of
// traders to record was captured at StartedAt, and later board changes do not
// change who is recorded. This removes survivor bias.
type CopyTradePaperRoster struct {
	ID        string    `gorm:"column:id;primaryKey" json:"id"`
	StartedAt int64     `gorm:"column:started_at" json:"started_at"`
	Members   string    `gorm:"column:members" json:"members"` // comma separated portfolio IDs
	Note      string    `gorm:"column:note" json:"note"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (CopyTradePaperRoster) TableName() string { return "copytrade_paper_rosters" }

// CopyTradePaperFill is one simulated follower fill: a lead trader's action,
// copied at the mark price seen when we noticed it, with the delay recorded.
// The unique index on the trader action makes re-scanning idempotent.
type CopyTradePaperFill struct {
	ID          int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	RosterID    string  `gorm:"column:roster_id;not null;uniqueIndex:idx_paper_fill,priority:1" json:"roster_id"`
	PortfolioID string  `gorm:"column:portfolio_id;not null;uniqueIndex:idx_paper_fill,priority:2;index" json:"portfolio_id"`
	Nickname    string  `gorm:"column:nickname" json:"nickname"`
	Symbol      string  `gorm:"column:symbol;not null;uniqueIndex:idx_paper_fill,priority:3" json:"symbol"`
	Side        string  `gorm:"column:side;not null;uniqueIndex:idx_paper_fill,priority:4" json:"side"`
	Action      string  `gorm:"column:action;not null;uniqueIndex:idx_paper_fill,priority:5" json:"action"`
	TradeTime   int64   `gorm:"column:trade_time;not null;uniqueIndex:idx_paper_fill,priority:6;index" json:"trade_time"`
	Qty         float64 `gorm:"column:qty;not null;uniqueIndex:idx_paper_fill,priority:7" json:"qty"`
	TraderPrice float64 `gorm:"column:trader_price;not null;uniqueIndex:idx_paper_fill,priority:8" json:"trader_price"`
	// SeenAt is when we first recorded the action (ms); DelayMs = SeenAt -
	// TradeTime is the copying delay. SeenPrice is the mark price then, and
	// BenchPrice the same-side BTC mark, both 0 when no price was available.
	SeenAt     int64   `gorm:"column:seen_at" json:"seen_at"`
	DelayMs    int64   `gorm:"column:delay_ms" json:"delay_ms"`
	SeenPrice  float64 `gorm:"column:seen_price" json:"seen_price"`
	BenchPrice float64 `gorm:"column:bench_price" json:"bench_price"`
	Style      string  `gorm:"column:style" json:"style"`
	Fee        float64 `gorm:"column:fee" json:"fee"`
}

func (CopyTradePaperFill) TableName() string { return "copytrade_paper_fills" }

// SavePaperRoster inserts or replaces a roster.
func (s *CopyTradeStore) SavePaperRoster(r *CopyTradePaperRoster) error {
	return s.db.Save(r).Error
}

// ActivePaperRoster returns the newest roster, or nil when none was started.
func (s *CopyTradeStore) ActivePaperRoster() (*CopyTradePaperRoster, error) {
	var r CopyTradePaperRoster
	err := s.db.Order("started_at DESC").First(&r).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListPaperRosters returns every roster, newest first.
func (s *CopyTradeStore) ListPaperRosters() ([]CopyTradePaperRoster, error) {
	var out []CopyTradePaperRoster
	err := s.db.Order("started_at DESC").Find(&out).Error
	return out, err
}

// AddPaperFills inserts simulated fills, ignoring duplicates (same roster,
// trader action and price), and returns how many were new.
func (s *CopyTradeStore) AddPaperFills(rows []CopyTradePaperFill) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	res := s.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 200)
	return int(res.RowsAffected), res.Error
}

// PaperFills returns every recorded fill of one roster, oldest first.
func (s *CopyTradeStore) PaperFills(rosterID string) ([]CopyTradePaperFill, error) {
	var out []CopyTradePaperFill
	err := s.db.Where("roster_id = ?", rosterID).Order("trade_time, id").Find(&out).Error
	return out, err
}

// PaperCursor is the newest trade time recorded for a member of a roster, or 0
// when nothing was recorded yet.
func (s *CopyTradeStore) PaperCursor(rosterID, portfolioID string) (int64, error) {
	var cursor int64
	err := s.db.Model(&CopyTradePaperFill{}).
		Where("roster_id = ? AND portfolio_id = ?", rosterID, portfolioID).
		Select("COALESCE(MAX(trade_time), 0)").Scan(&cursor).Error
	return cursor, err
}

// CountPaperFills counts the recorded fills of one roster.
func (s *CopyTradeStore) CountPaperFills(rosterID string) (int64, error) {
	var n int64
	err := s.db.Model(&CopyTradePaperFill{}).Where("roster_id = ?", rosterID).Count(&n).Error
	return n, err
}

// CopyTradeCoinScore is the latest AI reading of one coin on the copy-trade
// "by coin" page: direction, a 0-100 score, reasons and risks. Read-only
// guidance; it never feeds an order. Payload is the JSON the API returns.
type CopyTradeCoinScore struct {
	UserID     string `gorm:"column:user_id;primaryKey;uniqueIndex:idx_coin_score,priority:1" json:"-"`
	Symbol     string `gorm:"column:symbol;primaryKey;uniqueIndex:idx_coin_score,priority:2" json:"symbol"`
	ModelID    string `gorm:"column:model_id" json:"model_id"`
	Interval   string `gorm:"column:interval" json:"interval"`
	Payload    string `gorm:"column:payload" json:"payload"`
	AnalyzedAt int64  `gorm:"column:analyzed_at" json:"analyzed_at"`
}

func (CopyTradeCoinScore) TableName() string { return "copytrade_coin_scores" }

// SaveCoinScore inserts or replaces the reading for one user and coin.
func (s *CopyTradeStore) SaveCoinScore(x *CopyTradeCoinScore) error { return s.db.Save(x).Error }

// GetCoinScore returns the saved reading for one user and coin.
func (s *CopyTradeStore) GetCoinScore(userID, symbol string) (*CopyTradeCoinScore, error) {
	var x CopyTradeCoinScore
	err := s.db.Where("user_id = ? AND symbol = ?", userID, symbol).First(&x).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// ListCoinScores returns every saved reading for a user, newest first.
func (s *CopyTradeStore) ListCoinScores(userID string) ([]CopyTradeCoinScore, error) {
	var out []CopyTradeCoinScore
	err := s.db.Where("user_id = ?", userID).Order("analyzed_at DESC").Find(&out).Error
	return out, err
}

// CopyTradeScoreMemory is the last reading of one coin, fed back into the next
// run so the model re-evaluates instead of starting from scratch.
type CopyTradeScoreMemory struct {
	UserID       string  `gorm:"column:user_id;primaryKey;uniqueIndex:idx_score_mem,priority:1" json:"-"`
	Symbol       string  `gorm:"column:symbol;primaryKey;uniqueIndex:idx_score_mem,priority:2" json:"symbol"`
	Bias         string  `gorm:"column:bias" json:"bias"`
	Bull         float64 `gorm:"column:bull" json:"bull"`
	Bear         float64 `gorm:"column:bear" json:"bear"`
	Conviction   float64 `gorm:"column:conviction" json:"conviction"`
	SmoothedEdge float64 `gorm:"column:smoothed_edge" json:"smoothed_edge"`
	Invalidation string  `gorm:"column:invalidation" json:"invalidation"`
	Thesis       string  `gorm:"column:thesis" json:"thesis"`
	LastAt       int64   `gorm:"column:last_at" json:"last_at"`
	LastPrice    float64 `gorm:"column:last_price" json:"last_price"`
}

func (CopyTradeScoreMemory) TableName() string { return "copytrade_score_memory" }

func (s *CopyTradeStore) SaveScoreMemory(m *CopyTradeScoreMemory) error { return s.db.Save(m).Error }

func (s *CopyTradeStore) GetScoreMemory(userID, symbol string) (*CopyTradeScoreMemory, error) {
	var m CopyTradeScoreMemory
	err := s.db.Where("user_id = ? AND symbol = ?", userID, symbol).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// CopyTradeScoreOutcome is one score's realized forward result, evaluated at
// 4h/24h/72h against the coin and BTC, so we can measure whether high scores
// and shorts actually work. One row per (user, symbol, analyzed_at).
type CopyTradeScoreOutcome struct {
	ID         int64    `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID     string   `gorm:"column:user_id;uniqueIndex:idx_score_outcome,priority:1" json:"-"`
	Symbol     string   `gorm:"column:symbol;uniqueIndex:idx_score_outcome,priority:2" json:"symbol"`
	AnalyzedAt int64    `gorm:"column:analyzed_at;uniqueIndex:idx_score_outcome,priority:3" json:"analyzed_at"`
	Bias       string   `gorm:"column:bias" json:"bias"`
	Bull       float64  `gorm:"column:bull" json:"bull"`
	Bear       float64  `gorm:"column:bear" json:"bear"`
	EntryPrice float64  `gorm:"column:entry_price" json:"entry_price"`
	Ret4h      *float64 `gorm:"column:ret_4h" json:"ret_4h"`
	Ret24h     *float64 `gorm:"column:ret_24h" json:"ret_24h"`
	Ret72h     *float64 `gorm:"column:ret_72h" json:"ret_72h"`
	Bench4h    *float64 `gorm:"column:bench_4h" json:"bench_4h"`
	Bench24h   *float64 `gorm:"column:bench_24h" json:"bench_24h"`
	Bench72h   *float64 `gorm:"column:bench_72h" json:"bench_72h"`
	Done4h     bool     `gorm:"column:done_4h;index" json:"done_4h"`
	Done24h    bool     `gorm:"column:done_24h;index" json:"done_24h"`
	Done72h    bool     `gorm:"column:done_72h;index" json:"done_72h"`
}

func (CopyTradeScoreOutcome) TableName() string { return "copytrade_score_outcomes" }

// SaveScoreOutcome inserts a pending outcome, ignoring a duplicate.
func (s *CopyTradeStore) SaveScoreOutcome(o *CopyTradeScoreOutcome) error {
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(o).Error
}

// PendingScoreOutcomes returns rows with a horizon that is now due but not done.
func (s *CopyTradeStore) PendingScoreOutcomes(nowMs int64) ([]CopyTradeScoreOutcome, error) {
	var out []CopyTradeScoreOutcome
	err := s.db.Where("(done_4h = ? AND analyzed_at <= ?) OR (done_24h = ? AND analyzed_at <= ?) OR (done_72h = ? AND analyzed_at <= ?)",
		false, nowMs-4*3600*1000, false, nowMs-24*3600*1000, false, nowMs-72*3600*1000).
		Order("analyzed_at").Limit(2000).Find(&out).Error
	return out, err
}

func (s *CopyTradeStore) UpdateScoreOutcome(o *CopyTradeScoreOutcome) error {
	return s.db.Model(&CopyTradeScoreOutcome{}).Where("id = ?", o.ID).Updates(map[string]any{
		"ret_4h": o.Ret4h, "ret_24h": o.Ret24h, "ret_72h": o.Ret72h,
		"bench_4h": o.Bench4h, "bench_24h": o.Bench24h, "bench_72h": o.Bench72h,
		"done_4h": o.Done4h, "done_24h": o.Done24h, "done_72h": o.Done72h,
	}).Error
}

// ListScoreOutcomes returns a user's evaluated outcomes, newest first.
func (s *CopyTradeStore) ListScoreOutcomes(userID string) ([]CopyTradeScoreOutcome, error) {
	var out []CopyTradeScoreOutcome
	err := s.db.Where("user_id = ?", userID).Order("analyzed_at DESC").Limit(20000).Find(&out).Error
	return out, err
}
