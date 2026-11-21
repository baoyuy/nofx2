package store

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EventTradeTag is the latest two-model event stance for one (user, trader,
// coin). It is overwritten every cycle, so the table stays small; it is the
// read model the Events page shows without opening the trading page.
type EventTradeTag struct {
	UserID     string    `gorm:"primaryKey" json:"user_id"`
	TraderID   string    `gorm:"primaryKey" json:"trader_id"`
	Coin       string    `gorm:"primaryKey" json:"coin"`
	Bias       string    `json:"bias"` // bullish | bearish | neutral
	Confidence int       `json:"confidence"`
	Reason     string    `json:"reason"`
	TaggedAt   time.Time `gorm:"index:idx_event_trade_tag_tagged" json:"tagged_at"`
}

func (EventTradeTag) TableName() string { return "event_trade_tags" }

type EventTradeTagStore struct{ db *gorm.DB }

func NewEventTradeTagStore(db *gorm.DB) *EventTradeTagStore { return &EventTradeTagStore{db} }

func (s *EventTradeTagStore) InitTables() error { return s.db.AutoMigrate(&EventTradeTag{}) }

// Upsert overwrites the row for (user, trader, coin).
func (s *EventTradeTagStore) Upsert(tag EventTradeTag) error {
	if tag.TaggedAt.IsZero() {
		tag.TaggedAt = time.Now().UTC()
	}
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&tag).Error
}

func (s *EventTradeTagStore) ListByUser(userID string) ([]EventTradeTag, error) {
	var out []EventTradeTag
	err := s.db.Where("user_id = ?", userID).Order("trader_id, coin").Find(&out).Error
	return out, err
}

// DeleteOlderThan drops tags for traders that have not tagged recently.
func (s *EventTradeTagStore) DeleteOlderThan(cutoff time.Time) error {
	return s.db.Where("tagged_at < ?", cutoff).Delete(&EventTradeTag{}).Error
}
