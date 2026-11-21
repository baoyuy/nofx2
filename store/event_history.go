package store

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// eventHistoryRetention bounds how long local event history is kept. The
// collector prunes anything older so storage cannot grow without limit.
const eventHistoryRetention = 4 * 24 * time.Hour

// EventHistoryStore stores only public market data scoped to a local user.
// Its lock covers database work only; callers fetch platforms before calling it.
type EventHistoryStore struct {
	db        *gorm.DB
	mu        sync.Mutex
	cleanedAt time.Time
}

func NewEventHistoryStore(db *gorm.DB) *EventHistoryStore { return &EventHistoryStore{db: db} }

type EventHistoryEvent struct {
	UserID                                               string `gorm:"primaryKey"`
	Source                                               string `gorm:"primaryKey"`
	EventID                                              string `gorm:"primaryKey"`
	Title                                                string
	URL                                                  string
	Kind, Publisher, Summary, DiscussionURL, ArticleType string
	CoinsJSON                                            string
	FirstSeenAt                                          time.Time
}

func (EventHistoryEvent) TableName() string { return "event_history_events" }

type EventHistoryMarket struct {
	UserID      string `gorm:"primaryKey"`
	Source      string `gorm:"primaryKey"`
	EventID     string `gorm:"primaryKey"`
	MarketID    string `gorm:"primaryKey"`
	Question    string
	PriceBasis  string
	Fingerprint string
}

func (EventHistoryMarket) TableName() string { return "event_history_markets" }

type EventHistorySnapshot struct {
	UserID      string    `gorm:"primaryKey"`
	Source      string    `gorm:"primaryKey"`
	EventID     string    `gorm:"primaryKey"`
	MarketID    string    `gorm:"primaryKey"`
	ReceivedAt  time.Time `gorm:"primaryKey;index:idx_event_history_retention"`
	Fingerprint string
	PricesJSON  string
}

func (EventHistorySnapshot) TableName() string { return "event_history_snapshots" }
func (s *EventHistoryStore) initTables() error { return s.InitTables() }
func (s *EventHistoryStore) InitTables() error {
	return s.db.AutoMigrate(&EventHistoryEvent{}, &EventHistoryMarket{}, &EventHistorySnapshot{})
}

type EventHistoryPrice struct {
	Name  string
	Price float64
}
type EventHistorySample struct {
	Source, EventID, Title, URL                          string
	Kind, Publisher, Summary, DiscussionURL, ArticleType string
	Coins                                                []string
	ReceivedAt                                           time.Time
	Markets                                              []EventHistoryMarketSample
}
type EventHistoryMarketSample struct {
	MarketID, Question, PriceBasis, Fingerprint string
	Prices                                      []EventHistoryPrice
}
type EventHistoryResult struct {
	FirstSeenAt time.Time
	// Baselines are keyed by market ID. Missing means insufficient history.
	Baselines map[string]EventHistorySnapshot
}

// Record atomically saves a board and selects the latest sample at/before
// ReceivedAt-window, no older than 2*window. Existing samples are immutable.
// Empty-price markets retain metadata only. First-seen survives price retention.
func (s *EventHistoryStore) Record(userID string, observedAt time.Time, samples []EventHistorySample, window time.Duration) ([]EventHistoryResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("event history unavailable")
	}
	if strings.TrimSpace(userID) == "" || observedAt.IsZero() || observedAt.Year() < 1970 || observedAt.Year() > 9999 || window <= 0 || window > 15*24*time.Hour {
		return nil, fmt.Errorf("invalid event history observation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]EventHistoryResult, len(samples))
	cleanup := s.cleanedAt.IsZero() || observedAt.Sub(s.cleanedAt) >= time.Hour
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for i, sample := range samples {
			if sample.Source == "" || sample.EventID == "" || sample.ReceivedAt.IsZero() || sample.ReceivedAt.Year() < 1970 || sample.ReceivedAt.After(observedAt) {
				return fmt.Errorf("invalid event history sample")
			}
			coins, err := json.Marshal(sample.Coins)
			if err != nil {
				return err
			}
			event := EventHistoryEvent{UserID: userID, Source: sample.Source, EventID: sample.EventID, Title: sample.Title, URL: sample.URL, CoinsJSON: string(coins), FirstSeenAt: observedAt.UTC()}
			event.Kind, event.Publisher, event.Summary, event.DiscussionURL, event.ArticleType = sample.Kind, sample.Publisher, sample.Summary, sample.DiscussionURL, sample.ArticleType
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "source"}, {Name: "event_id"}}, DoUpdates: clause.Assignments(map[string]interface{}{
				"title": event.Title, "url": event.URL, "coins_json": event.CoinsJSON,
				"kind": event.Kind, "publisher": event.Publisher, "summary": event.Summary, "discussion_url": event.DiscussionURL, "article_type": event.ArticleType,
				"first_seen_at": gorm.Expr("CASE WHEN event_history_events.first_seen_at > ? THEN ? ELSE event_history_events.first_seen_at END", event.FirstSeenAt, event.FirstSeenAt),
			})}).Create(&event).Error; err != nil {
				return err
			}
			var saved EventHistoryEvent
			if err := tx.Where("user_id = ? AND source = ? AND event_id = ?", userID, sample.Source, sample.EventID).First(&saved).Error; err != nil {
				return err
			}
			results[i] = EventHistoryResult{FirstSeenAt: saved.FirstSeenAt, Baselines: map[string]EventHistorySnapshot{}}
			for _, m := range sample.Markets {
				// Reject the entire invalid market, never retain a partial outcome vector.
				valid := m.MarketID != "" && m.Fingerprint != ""
				names := map[string]bool{}
				for _, p := range m.Prices {
					if strings.TrimSpace(p.Name) == "" || names[p.Name] || math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || p.Price < 0 || p.Price > 1 {
						valid = false
					}
					names[p.Name] = true
				}
				if !valid {
					continue
				}
				metadata := EventHistoryMarket{UserID: userID, Source: sample.Source, EventID: sample.EventID, MarketID: m.MarketID, Question: m.Question, PriceBasis: m.PriceBasis, Fingerprint: m.Fingerprint}
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&metadata).Error; err != nil {
					return err
				}
				if len(m.Prices) == 0 {
					continue
				}
				prices, err := json.Marshal(m.Prices)
				if err != nil {
					return err
				}
				snapshot := EventHistorySnapshot{UserID: userID, Source: sample.Source, EventID: sample.EventID, MarketID: m.MarketID, ReceivedAt: sample.ReceivedAt.UTC(), Fingerprint: m.Fingerprint, PricesJSON: string(prices)}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error; err != nil {
					return err
				}
				var baseline []EventHistorySnapshot
				// Select the latest sample first, then verify fingerprint. Do not skip a
				// changed schema in order to find a coincidentally matching older sample.
				if err := tx.Where("user_id = ? AND source = ? AND event_id = ? AND market_id = ? AND received_at <= ? AND received_at >= ? AND received_at < ?", userID, sample.Source, sample.EventID, m.MarketID, sample.ReceivedAt.Add(-window), sample.ReceivedAt.Add(-2*window), sample.ReceivedAt).Order("received_at DESC").Limit(1).Find(&baseline).Error; err != nil {
					return err
				}
				if len(baseline) > 0 && baseline[0].Fingerprint == m.Fingerprint {
					results[i].Baselines[m.MarketID] = baseline[0]
				}
			}
		}
		if cleanup {
			cutoff := observedAt.Add(-eventHistoryRetention)
			if err := tx.Where("received_at < ?", cutoff).Delete(&EventHistorySnapshot{}).Error; err != nil {
				return err
			}
			if err := tx.Where("first_seen_at < ?", cutoff).
				Where("NOT EXISTS (SELECT 1 FROM event_history_snapshots s WHERE s.user_id = event_history_events.user_id AND s.source = event_history_events.source AND s.event_id = event_history_events.event_id)").
				Delete(&EventHistoryEvent{}).Error; err != nil {
				return err
			}
			return tx.Where("NOT EXISTS (SELECT 1 FROM event_history_events e WHERE e.user_id = event_history_markets.user_id AND e.source = event_history_markets.source AND e.event_id = event_history_markets.event_id)").
				Delete(&EventHistoryMarket{}).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if cleanup {
		s.cleanedAt = observedAt
	}
	return results, nil
}

// PruneOlderThan deletes snapshots older than cutoff and any event/market
// metadata no longer backed by a retained snapshot. Safe to call on a timer
// even when no trader is running; keeps local storage bounded.
func (s *EventHistoryStore) PruneOlderThan(cutoff time.Time) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("event history unavailable")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("received_at < ?", cutoff).Delete(&EventHistorySnapshot{}).Error; err != nil {
			return err
		}
		if err := tx.Where("first_seen_at < ?", cutoff).
			Where("NOT EXISTS (SELECT 1 FROM event_history_snapshots s WHERE s.user_id = event_history_events.user_id AND s.source = event_history_events.source AND s.event_id = event_history_events.event_id)").
			Delete(&EventHistoryEvent{}).Error; err != nil {
			return err
		}
		return tx.Where("NOT EXISTS (SELECT 1 FROM event_history_events e WHERE e.user_id = event_history_markets.user_id AND e.source = event_history_markets.source AND e.event_id = event_history_markets.event_id)").
			Delete(&EventHistoryMarket{}).Error
	})
}
