package store

import "gorm.io/gorm/clause"

// DirectionResearch preserves the actual selection at decision time. Separate
// from strategy state and orders; never used to execute trades.
type DirectionResearch struct {
	ID      int64  `gorm:"primaryKey"`
	UserID  string `gorm:"uniqueIndex:idx_direction_research,priority:1"`
	Slot    int64  `gorm:"uniqueIndex:idx_direction_research,priority:2"`
	At      int64  `gorm:"index"`
	Payload string
	Results string
	Done    bool `gorm:"index"`
}

func (DirectionResearch) TableName() string { return "copytrade_direction_research" }
func (s *CopyTradeStore) SaveDirectionResearch(r *DirectionResearch) error {
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(r).Error
}
func (s *CopyTradeStore) PendingDirectionResearch(now int64) ([]DirectionResearch, error) {
	out := []DirectionResearch{}
	err := s.db.Where("done = ? AND at <= ?", false, now-4*3600000).Order("at").Limit(200).Find(&out).Error
	return out, err
}
func (s *CopyTradeStore) FinishDirectionResearch(id int64, results string) error {
	return s.db.Model(&DirectionResearch{}).Where("id = ?", id).Updates(map[string]any{"results": results, "done": true}).Error
}
