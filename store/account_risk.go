package store

import (
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"time"
)

// AccountRiskState survives restarts; UTC boundaries make the risk day explicit.
// Balances must be adjusted separately for deposits before use on funded accounts.
type AccountRiskState struct {
	TraderID       string `gorm:"primaryKey;size:191"`
	Day            string
	DayStartEquity float64
	PeakEquity     float64
	UpdatedAt      int64
}

func (s *Store) ObserveAccountRisk(id string, equity float64, now time.Time) (*AccountRiskState, error) {
	if s == nil || s.gdb == nil || equity <= 0 || math.IsNaN(equity) || math.IsInf(equity, 0) || id == "" {
		return nil, fmt.Errorf("account risk requires database and positive equity")
	}
	s.accountRiskInit.Do(func() { s.accountRiskInitErr = s.gdb.AutoMigrate(&AccountRiskState{}) })
	if s.accountRiskInitErr != nil {
		return nil, s.accountRiskInitErr
	}
	var state AccountRiskState
	err := s.gdb.Transaction(func(tx *gorm.DB) error {
		seed := AccountRiskState{TraderID: id, Day: now.UTC().Format("2006-01-02"), DayStartEquity: equity, PeakEquity: equity}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, "trader_id = ?", id).Error; err != nil {
			return err
		}
		if state.Day != seed.Day {
			state.Day = seed.Day
			state.DayStartEquity = equity
		}
		if equity > state.PeakEquity {
			state.PeakEquity = equity
		}
		state.UpdatedAt = now.UnixMilli()
		return tx.Save(&state).Error
	})
	return &state, err
}
