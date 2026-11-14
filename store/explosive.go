package store

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ExplosiveSignal is one 15-minute reading of a coin on the explosive board,
// with what the price did afterwards. The rows are the record the board is
// judged and tuned on: without them every weight change is a guess.
type ExplosiveSignal struct {
	ID     int64  `gorm:"column:id;primaryKey;autoIncrement"`
	TS     int64  `gorm:"column:ts;not null;uniqueIndex:idx_explosive_signal,priority:1;index"`
	Symbol string `gorm:"column:symbol;not null;uniqueIndex:idx_explosive_signal,priority:2"`

	Rank     int     `gorm:"column:rank"`
	Price    float64 `gorm:"column:price"`
	Score    float64 `gorm:"column:score"`
	Strength float64 `gorm:"column:strength"`
	Risk     float64 `gorm:"column:risk"`
	// ScoreVersion marks the weight set that produced Score.
	ScoreVersion int `gorm:"column:score_version"`

	Change24h float64 `gorm:"column:change_24h"`
	Change5m  float64 `gorm:"column:change_5m"`
	Change15m float64 `gorm:"column:change_15m"`
	Change1h  float64 `gorm:"column:change_1h"`
	Change4h  float64 `gorm:"column:change_4h"`

	// Beijing calendar-day features and the prediction the board ranks on.
	DayGain            float64 `gorm:"column:day_gain"`
	ElapsedH           float64 `gorm:"column:elapsed_hours"`
	VolumeSlope        float64 `gorm:"column:volume_slope"`
	PredictedRemaining float64 `gorm:"column:predicted_remaining"`
	PredictedFinal     float64 `gorm:"column:predicted_final"`
	VolSpike1h         float64 `gorm:"column:vol_spike_1h"`
	RadarScore         float64 `gorm:"column:radar_score"`
	Radar              bool    `gorm:"column:radar"`
	Tradability        string  `gorm:"column:tradability"`

	VolRatio     float64 `gorm:"column:vol_ratio"`
	OIChange1h   float64 `gorm:"column:oi_change_1h"`
	Funding      float64 `gorm:"column:funding"`
	ATRPct       float64 `gorm:"column:atr_pct"`
	DistanceHigh float64 `gorm:"column:distance_high"`

	EMA20    float64 `gorm:"column:ema20"`
	EMA60    float64 `gorm:"column:ema60"`
	EMA120   float64 `gorm:"column:ema120"`
	EMASlope float64 `gorm:"column:ema_slope"`
	// TakerBuyRatio is aggressive buying as a share of volume, 0-1.
	TakerBuyRatio float64 `gorm:"column:taker_buy_ratio"`
	// RiseEfficiency is net move over the path travelled: a straight rise
	// scores near 1, a choppy one well below.
	RiseEfficiency float64 `gorm:"column:rise_efficiency"`

	Tags  string `gorm:"column:tags"` // JSON array
	State string `gorm:"column:state"`

	// Forward results, nil until the horizon has passed and klines were read.
	Ret1h    *float64 `gorm:"column:ret_1h"`
	Ret4h    *float64 `gorm:"column:ret_4h"`
	Ret24h   *float64 `gorm:"column:ret_24h"`
	Bench1h  *float64 `gorm:"column:bench_1h"`
	Bench4h  *float64 `gorm:"column:bench_4h"`
	Bench24h *float64 `gorm:"column:bench_24h"`
	MaxDD1h  *float64 `gorm:"column:max_dd_1h"`
	MaxDD4h  *float64 `gorm:"column:max_dd_4h"`
	MaxDD24h *float64 `gorm:"column:max_dd_24h"`

	Done1h  bool `gorm:"column:done_1h;index"`
	Done4h  bool `gorm:"column:done_4h;index"`
	Done24h bool `gorm:"column:done_24h;index"`
}

func (ExplosiveSignal) TableName() string { return "explosive_signals" }

// ExplosiveStore keeps the explosive board's readings and their outcomes.
type ExplosiveStore struct{ db *gorm.DB }

func NewExplosiveStore(db *gorm.DB) *ExplosiveStore { return &ExplosiveStore{db: db} }

// InitTables creates the table (exported for tests).
func (s *ExplosiveStore) InitTables() error { return s.initTables() }

func (s *ExplosiveStore) initTables() error {
	return s.db.AutoMigrate(&ExplosiveSignal{})
}

// SaveSignals inserts a bucket's rows, skipping duplicates.
func (s *ExplosiveStore) SaveSignals(rows []ExplosiveSignal) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// HasSignals tells whether the bucket was already recorded.
func (s *ExplosiveStore) HasSignals(ts int64) (bool, error) {
	var n int64
	err := s.db.Model(&ExplosiveSignal{}).Where("ts = ?", ts).Count(&n).Error
	return n > 0, err
}

// DueSignals returns rows whose horizon has passed but not been filled in.
func (s *ExplosiveStore) DueSignals(horizon string, dueBefore int64, limit int) ([]ExplosiveSignal, error) {
	done := "done_1h"
	switch horizon {
	case "4h":
		done = "done_4h"
	case "24h":
		done = "done_24h"
	}
	var out []ExplosiveSignal
	err := s.db.Where(done+" = ? AND ts <= ?", false, dueBefore).Order("ts").Limit(limit).Find(&out).Error
	return out, err
}

// UpdateSignal writes one row's forward results.
func (s *ExplosiveStore) UpdateSignal(row *ExplosiveSignal) error {
	return s.db.Model(&ExplosiveSignal{}).Where("id = ?", row.ID).
		Updates(map[string]any{
			"ret_1h": row.Ret1h, "ret_4h": row.Ret4h, "ret_24h": row.Ret24h,
			"bench_1h": row.Bench1h, "bench_4h": row.Bench4h, "bench_24h": row.Bench24h,
			"max_dd_1h": row.MaxDD1h, "max_dd_4h": row.MaxDD4h, "max_dd_24h": row.MaxDD24h,
			"done_1h": row.Done1h, "done_4h": row.Done4h, "done_24h": row.Done24h,
		}).Error
}

// SignalsSince returns every row recorded at or after ts, oldest first.
func (s *ExplosiveStore) SignalsSince(ts int64) ([]ExplosiveSignal, error) {
	var out []ExplosiveSignal
	err := s.db.Where("ts >= ?", ts).Order("ts").Find(&out).Error
	return out, err
}

// PruneSignals deletes rows older than before.
func (s *ExplosiveStore) PruneSignals(before int64) error {
	return s.db.Where("ts < ?", before).Delete(&ExplosiveSignal{}).Error
}
