package store

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxMarketAnalysesPerUser bounds how many symbols keep a saved analysis.
const MaxMarketAnalysesPerUser = 30

// MarketAnalysis is the latest AI analysis a user ran for one symbol on the
// market page. Re-analyzing the same symbol replaces it.
type MarketAnalysis struct {
	UserID     string    `gorm:"column:user_id;primaryKey" json:"user_id"`
	Symbol     string    `gorm:"column:symbol;primaryKey" json:"symbol"`
	Result     string    `gorm:"column:result;type:text;not null" json:"result"` // JSON from /api/market/analyze
	AnalyzedAt int64     `gorm:"column:analyzed_at;not null;index" json:"analyzed_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (MarketAnalysis) TableName() string { return "market_analyses" }

type MarketAnalysisStore struct {
	db *gorm.DB
}

func NewMarketAnalysisStore(db *gorm.DB) *MarketAnalysisStore {
	return &MarketAnalysisStore{db: db}
}

func (s *MarketAnalysisStore) initTables() error {
	return s.db.AutoMigrate(&MarketAnalysis{})
}

// Save upserts the analysis for (user, symbol) and trims the user's oldest
// entries beyond MaxMarketAnalysesPerUser.
func (s *MarketAnalysisStore) Save(userID, symbol, resultJSON string, analyzedAt int64) error {
	row := MarketAnalysis{UserID: userID, Symbol: symbol, Result: resultJSON, AnalyzedAt: analyzedAt}
	if err := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "symbol"}},
		DoUpdates: clause.AssignmentColumns([]string{"result", "analyzed_at", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return err
	}
	var keep []string
	if err := s.db.Model(&MarketAnalysis{}).Where("user_id = ?", userID).
		Order("analyzed_at DESC").Limit(MaxMarketAnalysesPerUser).Pluck("symbol", &keep).Error; err != nil {
		return err
	}
	return s.db.Where("user_id = ? AND symbol NOT IN ?", userID, keep).Delete(&MarketAnalysis{}).Error
}

// List returns the user's saved analyses, newest first.
func (s *MarketAnalysisStore) List(userID string) ([]MarketAnalysis, error) {
	var rows []MarketAnalysis
	err := s.db.Where("user_id = ?", userID).Order("analyzed_at DESC").
		Limit(MaxMarketAnalysesPerUser).Find(&rows).Error
	return rows, err
}
