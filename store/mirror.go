package store

import (
	"time"

	"gorm.io/gorm"
)

// MirrorLink copies one AI trader's positions to another Binance account
// (real or paper) of the same user.
type MirrorLink struct {
	ID         string `gorm:"column:id;primaryKey" json:"id"`
	UserID     string `gorm:"column:user_id;not null;index" json:"-"`
	LeaderID   string `gorm:"column:leader_id;not null;index" json:"leader_id"`
	ExchangeID string `gorm:"column:exchange_id;not null;index" json:"exchange_id"`
	// SizeMode "fixed": every open uses FixedMargin USDT of margin. "ratio":
	// the follower uses the same share of its equity as the AI, times Ratio.
	SizeMode    string  `gorm:"column:size_mode;not null" json:"size_mode"`
	FixedMargin float64 `gorm:"column:fixed_margin" json:"fixed_margin"`
	Ratio       float64 `gorm:"column:ratio" json:"ratio"`
	// LeverageMode "follow": the AI's leverage, at most MaxLeverage (0 = no
	// cap). "fixed": always Leverage.
	LeverageMode string `gorm:"column:leverage_mode;not null" json:"leverage_mode"`
	Leverage     int    `gorm:"column:leverage" json:"leverage"`
	MaxLeverage  int    `gorm:"column:max_leverage" json:"max_leverage"`
	// FollowProtect copies the AI's stop loss and take profit orders. Closes
	// are copied either way.
	FollowProtect bool `gorm:"column:follow_protect" json:"follow_protect"`
	// MaxMargin caps the margin of one open (0 = no cap). MaxLoss stops the
	// link and closes its positions once the account lost that much USDT
	// since the link started (0 = no cap).
	MaxMargin float64 `gorm:"column:max_margin" json:"max_margin"`
	MaxLoss   float64 `gorm:"column:max_loss" json:"max_loss"`
	Running   bool    `gorm:"column:running" json:"running"`
	// StartEquity is the follower's equity when the link last started.
	StartEquity float64   `gorm:"column:start_equity" json:"start_equity"`
	StopReason  string    `gorm:"column:stop_reason" json:"stop_reason"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (MirrorLink) TableName() string { return "mirror_links" }

// MirrorPosition is what a link knows of one AI position and its copy.
type MirrorPosition struct {
	LinkID string `gorm:"column:link_id;primaryKey" json:"-"`
	Symbol string `gorm:"column:symbol;primaryKey" json:"symbol"`
	Side   string `gorm:"column:side;primaryKey" json:"side"` // long | short
	// LeaderQty is the AI's quantity when the link last acted on it.
	LeaderQty float64 `gorm:"column:leader_qty" json:"leader_qty"`
	// FollowerQty is the quantity the link holds for it; 0 when not copied.
	FollowerQty float64 `gorm:"column:follower_qty" json:"follower_qty"`
	// Skip is why the position is not copied ("" = copied): it was open before
	// the link started, the open failed, or the follower closed it.
	Skip       string  `gorm:"column:skip" json:"skip,omitempty"`
	StopLoss   float64 `gorm:"column:stop_loss" json:"stop_loss,omitempty"`
	TakeProfit float64 `gorm:"column:take_profit" json:"take_profit,omitempty"`
	OpenedAt   int64   `gorm:"column:opened_at" json:"opened_at"`
	// OrderAt is the last order the link placed for it (ms).
	OrderAt int64 `gorm:"column:order_at" json:"-"`
}

func (MirrorPosition) TableName() string { return "mirror_positions" }

// MirrorEvent is one thing a link did or skipped.
type MirrorEvent struct {
	ID     int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	LinkID string `gorm:"column:link_id;not null;index" json:"-"`
	Time   int64  `gorm:"column:time;not null" json:"time"`
	// Action: open, add, reduce, close, protect, skip, error, start, stop,
	// follower_closed.
	Action string  `gorm:"column:action;not null" json:"action"`
	Symbol string  `gorm:"column:symbol" json:"symbol,omitempty"`
	Side   string  `gorm:"column:side" json:"side,omitempty"`
	Qty    float64 `gorm:"column:qty" json:"qty,omitempty"`
	Price  float64 `gorm:"column:price" json:"price,omitempty"`
	Detail string  `gorm:"column:detail" json:"detail,omitempty"`
}

func (MirrorEvent) TableName() string { return "mirror_events" }

const mirrorEventsKept = 1000

type MirrorStore struct{ db *gorm.DB }

func NewMirrorStore(db *gorm.DB) *MirrorStore { return &MirrorStore{db: db} }

// InitTables creates the mirror tables (exported for tests).
func (s *MirrorStore) InitTables() error { return s.initTables() }

func (s *MirrorStore) initTables() error {
	return s.db.AutoMigrate(&MirrorLink{}, &MirrorPosition{}, &MirrorEvent{})
}

// Links returns the user's links, all of them when userID is "".
func (s *MirrorStore) Links(userID string) ([]MirrorLink, error) {
	var out []MirrorLink
	q := s.db.Order("created_at")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	return out, q.Find(&out).Error
}

func (s *MirrorStore) Link(userID, id string) (MirrorLink, error) {
	var l MirrorLink
	err := s.db.Where("user_id = ? AND id = ?", userID, id).First(&l).Error
	return l, err
}

// LinkByID reads a link of any user, for the engine.
func (s *MirrorStore) LinkByID(id string) (MirrorLink, error) {
	var l MirrorLink
	err := s.db.Where("id = ?", id).First(&l).Error
	return l, err
}

func (s *MirrorStore) SaveLink(l *MirrorLink) error { return s.db.Save(l).Error }

// DeleteLink removes a link with its positions and events.
func (s *MirrorStore) DeleteLink(userID, id string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("user_id = ? AND id = ?", userID, id).Delete(&MirrorLink{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("link_id = ?", id).Delete(&MirrorPosition{}).Error; err != nil {
			return err
		}
		return tx.Where("link_id = ?", id).Delete(&MirrorEvent{}).Error
	})
}

func (s *MirrorStore) Positions(linkID string) ([]MirrorPosition, error) {
	var out []MirrorPosition
	return out, s.db.Where("link_id = ?", linkID).Order("opened_at").Find(&out).Error
}

func (s *MirrorStore) SavePosition(p *MirrorPosition) error { return s.db.Save(p).Error }

func (s *MirrorStore) DeletePosition(linkID, symbol, side string) error {
	return s.db.Where("link_id = ? AND symbol = ? AND side = ?", linkID, symbol, side).Delete(&MirrorPosition{}).Error
}

// AddEvent logs an event and keeps the newest mirrorEventsKept per link.
func (s *MirrorStore) AddEvent(e MirrorEvent) error {
	if err := s.db.Create(&e).Error; err != nil {
		return err
	}
	return s.db.Exec(`DELETE FROM mirror_events WHERE link_id = ? AND id <= (SELECT id FROM mirror_events
		WHERE link_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`, e.LinkID, e.LinkID, mirrorEventsKept).Error
}

// Events returns a link's events, newest first.
func (s *MirrorStore) Events(linkID string, limit int) ([]MirrorEvent, error) {
	var out []MirrorEvent
	return out, s.db.Where("link_id = ?", linkID).Order("id DESC").Limit(limit).Find(&out).Error
}
