package store

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EventLabel contains only validated structured analysis, never raw responses,
// prompts, event text, API keys or endpoint URLs.
type EventLabel struct {
	UserID       string `gorm:"primaryKey"`
	EventID      string `gorm:"primaryKey"`
	ModelID      string `gorm:"primaryKey"`
	Fingerprint  string `gorm:"primaryKey"`
	InputHash    string `gorm:"primaryKey"`
	AnalysisJSON string
	AnalyzedAt   time.Time
}
type EventLabelGate struct {
	UserID     string `gorm:"primaryKey"`
	Token      string
	LeaseUntil time.Time
}
type EventLabelAttempt struct {
	ID        string    `gorm:"primaryKey"`
	UserID    string    `gorm:"index:idx_label_attempt_user_time"`
	StartedAt time.Time `gorm:"index:idx_label_attempt_user_time"`
}

const (
	eventLabelCooldown    = 10 * time.Second
	eventLabelHourlyLimit = 120
	eventLabelLease       = 5 * time.Minute
)

type EventLabelStore struct{ db *gorm.DB }

func NewEventLabelStore(db *gorm.DB) *EventLabelStore { return &EventLabelStore{db} }
func (s *EventLabelStore) InitTables() error {
	return s.db.AutoMigrate(&EventLabel{}, &EventLabelGate{}, &EventLabelAttempt{})
}
func (s *EventLabelStore) Latest(user, event, model, fp string) (*EventLabel, error) {
	var row EventLabel
	err := s.db.Where("user_id = ? AND event_id = ? AND model_id = ? AND fingerprint = ?", user, event, model, fp).Order("analyzed_at DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}
func (s *EventLabelStore) exact(tx *gorm.DB, key EventLabel) (*EventLabel, error) {
	var row EventLabel
	err := tx.Where("user_id = ? AND event_id = ? AND model_id = ? AND fingerprint = ? AND input_hash = ?", key.UserID, key.EventID, key.ModelID, key.Fingerprint, key.InputHash).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

type EventLabelLimit struct {
	Busy       bool
	RetryAfter int
}

func (e *EventLabelLimit) Error() string { return "event label request limited" }
func retrySeconds(d time.Duration) int {
	n := int((d + time.Second - 1) / time.Second)
	if n < 1 {
		return 1
	}
	return n
}

// Reserve serializes only database work using a DB-backed per-user lease. The
// atomic UPDATE also serializes reservations across processes. Failures count;
// cache hits bypass quota, and a crashed worker's lease expires after five minutes.
func (s *EventLabelStore) Reserve(key EventLabel, now time.Time) (cached *EventLabel, token string, err error) {
	cached, err = s.exact(s.db, key)
	if err != nil || cached != nil {
		return
	}
	token = uuid.NewString()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&EventLabelGate{UserID: key.UserID, LeaseUntil: time.Unix(0, 0).UTC()}).Error; err != nil {
			return err
		}
		update := tx.Model(&EventLabelGate{}).Where("user_id = ? AND lease_until <= ?", key.UserID, now).Updates(map[string]any{"token": token, "lease_until": now.Add(eventLabelLease)})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return &EventLabelLimit{Busy: true, RetryAfter: 5}
		}
		var e error
		cached, e = s.exact(tx, key)
		if e != nil {
			return e
		}
		if cached != nil {
			return nil
		}
		var attempts []EventLabelAttempt
		if e = tx.Where("user_id = ? AND started_at > ?", key.UserID, now.Add(-time.Hour)).Order("started_at ASC").Find(&attempts).Error; e != nil {
			return e
		}
		if len(attempts) > 0 {
			last := attempts[len(attempts)-1].StartedAt
			if now.Sub(last) < eventLabelCooldown {
				return &EventLabelLimit{RetryAfter: retrySeconds(last.Add(eventLabelCooldown).Sub(now))}
			}
		}
		if len(attempts) >= eventLabelHourlyLimit {
			return &EventLabelLimit{RetryAfter: retrySeconds(attempts[len(attempts)-eventLabelHourlyLimit].StartedAt.Add(time.Hour).Sub(now))}
		}
		// Retention of attempts need only cover the rolling budget window.
		if e = tx.Where("user_id = ? AND started_at <= ?", key.UserID, now.Add(-time.Hour)).Delete(&EventLabelAttempt{}).Error; e != nil {
			return e
		}
		return tx.Create(&EventLabelAttempt{ID: token, UserID: key.UserID, StartedAt: now.UTC()}).Error
	})
	if err == nil && cached != nil {
		_ = s.Finish(key, token, "", now)
	}
	return
}

// Finish accepts only caller-validated JSON. A lease token fences late workers;
// an expired/replaced reservation can never overwrite a newer result.
func (s *EventLabelStore) Finish(key EventLabel, token, validatedJSON string, now time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&EventLabelGate{}).Where("user_id = ? AND token = ? AND lease_until > ?", key.UserID, token, now).Updates(map[string]any{"token": "", "lease_until": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return errors.New("event label lease expired")
		}
		if validatedJSON == "" {
			return nil
		}
		key.AnalysisJSON = validatedJSON
		key.AnalyzedAt = now.UTC()
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&key).Error
	})
}

// DeleteOlderThan drops cached labels and stale attempts older than cutoff so
// the label tables stay bounded by retention.
func (s *EventLabelStore) DeleteOlderThan(cutoff time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("analyzed_at < ?", cutoff).Delete(&EventLabel{}).Error; err != nil {
			return err
		}
		return tx.Where("started_at < ?", cutoff).Delete(&EventLabelAttempt{}).Error
	})
}
