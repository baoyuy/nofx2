package store

import (
	"errors"

	"gorm.io/gorm"
)

// EventLabelSettings stores the per-account event labeling preference.
type EventLabelSettings struct {
	UserID    string `gorm:"primaryKey;column:user_id" json:"-"`
	ModelID   string `gorm:"column:model_id;not null;default:''" json:"model_id"`
	AutoLabel bool   `gorm:"column:auto_label;not null;default:false" json:"auto_label"`
}

func (EventLabelSettings) TableName() string { return "event_label_settings" }

type EventLabelSettingsStore struct{ db *gorm.DB }

func NewEventLabelSettingsStore(db *gorm.DB) *EventLabelSettingsStore {
	return &EventLabelSettingsStore{db: db}
}

func (s *EventLabelSettingsStore) initTables() error {
	return s.db.AutoMigrate(&EventLabelSettings{})
}

// Get returns account settings, or the disabled empty default when unset.
func (s *EventLabelSettingsStore) Get(userID string) (EventLabelSettings, error) {
	var settings EventLabelSettings
	err := s.db.Where("user_id = ?", userID).First(&settings).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EventLabelSettings{UserID: userID}, nil
	}
	return settings, err
}

// Set replaces account settings. The caller validates model ownership and
// enabled state before persisting the preference.
func (s *EventLabelSettingsStore) Set(userID, modelID string, autoLabel bool) error {
	settings := EventLabelSettings{UserID: userID, ModelID: modelID, AutoLabel: autoLabel}
	return s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&EventLabelSettings{}).Where("user_id = ?", userID).Updates(map[string]any{"model_id": modelID, "auto_label": autoLabel})
		if result.Error != nil || result.RowsAffected > 0 {
			return result.Error
		}
		return tx.Create(&settings).Error
	})
}
