package store

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EventCard is one tagged information event (a catalyst) produced from the raw
// news / prediction-market stream. This is the "信息打标" unit the posts
// describe: what happened, which coin, direction, whether it beat expectations,
// importance, and whether it is a scheduled node. It also carries a lightweight
// outcome record (entry price -> later hit/miss) for a hit-rate loop.
type EventCard struct {
	UserID     string `gorm:"primaryKey" json:"user_id"`
	TraderID   string `gorm:"primaryKey" json:"trader_id"`
	Coin       string `gorm:"primaryKey" json:"coin"`
	CardID     string `gorm:"primaryKey" json:"card_id"`
	Symbol     string `json:"symbol,omitempty"` // exchange symbol for price lookup
	Type       string `json:"type"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Direction  string `json:"direction"` // bullish|bearish|neutral
	Surprise   string `json:"surprise"`  // above|below|inline|unknown
	Importance int    `json:"importance"`
	IsNode     bool   `json:"is_node"`
	NodeAt     string `json:"node_at,omitempty"`
	ValidUntil string `json:"valid_until,omitempty"`
	Source     string `json:"source"`
	EvidenceJSON string `json:"evidence_ids"`
	Reason     string `json:"reason"`
	ModelID    string `json:"model_id"`
	TaggedAt   time.Time `gorm:"index:idx_event_card_tagged" json:"tagged_at"`

	// Outcome tracking (set once on first insert, preserved on later upserts).
	FirstTaggedAt time.Time  `json:"first_tagged_at"`
	EntryPrice    float64    `json:"entry_price"`
	EvalStatus    string     `json:"eval_status"` // pending|hit|miss|neutral|skipped
	EvalPct       float64    `json:"eval_pct"`
	EvalAt        *time.Time `json:"eval_at,omitempty"`
}

func (EventCard) TableName() string { return "event_cards" }

type EventCardStore struct{ db *gorm.DB }

func NewEventCardStore(db *gorm.DB) *EventCardStore { return &EventCardStore{db} }

func (s *EventCardStore) InitTables() error { return s.db.AutoMigrate(&EventCard{}) }

// UpsertMany inserts/updates cards by (user, trader, coin, card_id). Outcome
// fields (first_tagged_at/entry_price/eval_*) are preserved on update so a
// refresh never resets the hit-rate record.
func (s *EventCardStore) UpsertMany(cards []EventCard) error {
	if len(cards) == 0 {
		return nil
	}
	updateCols := []string{"symbol", "type", "title", "summary", "direction", "surprise", "importance",
		"is_node", "node_at", "valid_until", "source", "evidence_json", "reason", "model_id", "tagged_at"}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "trader_id"}, {Name: "coin"}, {Name: "card_id"}},
		DoUpdates: clause.AssignmentColumns(updateCols),
	}).Create(&cards).Error
}

// DeleteExpired removes cards whose validity/node passed or that were not
// refreshed within ttl. Grace keeps a node card briefly after its node time.
func (s *EventCardStore) DeleteExpired(now time.Time, ttl, nodeGrace time.Duration) error {
	nowStr := now.UTC().Format(time.RFC3339)
	nodeCutoff := now.Add(-nodeGrace).UTC().Format(time.RFC3339)
	tagCutoff := now.Add(-ttl)
	return s.db.Where("(valid_until <> '' AND valid_until < ?) OR (node_at <> '' AND node_at < ?) OR tagged_at < ?",
		nowStr, nodeCutoff, tagCutoff).Delete(&EventCard{}).Error
}

func (s *EventCardStore) ListByUser(userID string) ([]EventCard, error) {
	var out []EventCard
	err := s.db.Where("user_id = ?", userID).Order("coin, importance DESC").Find(&out).Error
	return out, err
}

func (s *EventCardStore) DeleteOlderThan(cutoff time.Time) error {
	return s.db.Where("tagged_at < ?", cutoff).Delete(&EventCard{}).Error
}

// ListPendingForEval returns directional cards whose first observation is old
// enough to evaluate but not so old that a price move is no longer about it.
func (s *EventCardStore) ListPendingForEval(now time.Time, horizon time.Duration) ([]EventCard, error) {
	var out []EventCard
	cutoff := now.Add(-horizon)
	floor := now.Add(-72 * time.Hour)
	err := s.db.Where("eval_status = ? AND entry_price > 0 AND direction IN (?, ?) AND first_tagged_at <= ? AND first_tagged_at >= ?",
		"pending", "bullish", "bearish", cutoff, floor).Limit(500).Find(&out).Error
	return out, err
}

func (s *EventCardStore) MarkEvaluated(userID, traderID, coin, cardID, status string, pct float64, at time.Time) error {
	return s.db.Model(&EventCard{}).
		Where("user_id = ? AND trader_id = ? AND coin = ? AND card_id = ?", userID, traderID, coin, cardID).
		Updates(map[string]any{"eval_status": status, "eval_pct": pct, "eval_at": at.UTC()}).Error
}

// CardStats counts evaluated cards per status for one user.
func (s *EventCardStore) CardStats(userID string) (map[string]int, error) {
	type row struct {
		EvalStatus string
		N          int
	}
	var rows []row
	if err := s.db.Model(&EventCard{}).Select("eval_status, count(*) as n").
		Where("user_id = ?", userID).Group("eval_status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]int{"pending": 0, "hit": 0, "miss": 0, "neutral": 0, "skipped": 0, "total": 0}
	for _, r := range rows {
		out[r.EvalStatus] = r.N
		out["total"] += r.N
	}
	return out, nil
}

// ReplaceForTraderCoin swaps the card set for one (trader, coin). Kept for
// tests and manual resets; runtime uses UpsertMany to preserve intelligence.
func (s *EventCardStore) ReplaceForTraderCoin(userID, traderID, coin string, cards []EventCard) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND trader_id = ? AND coin = ?", userID, traderID, coin).Delete(&EventCard{}).Error; err != nil {
			return err
		}
		if len(cards) == 0 {
			return nil
		}
		return tx.Create(&cards).Error
	})
}
