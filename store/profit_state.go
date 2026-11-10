package store

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

// PositionProfitState is deliberately separate from the legacy prompt peak cache.
// One active row per slot; Lifecycle + EntryPrice validate every observation.
// Config is captured ONLY by the confirmed-open hook, never by discovery.
type PositionProfitState struct {
	TraderID       string `gorm:"primaryKey;size:191"`
	Symbol         string `gorm:"primaryKey;size:100"`
	Side           string `gorm:"primaryKey;size:10"`
	Lifecycle      string `gorm:"not null"`
	EntryPrice     float64
	PeakPricePct   float64
	LegacyArmed    bool
	Eligible       bool
	BreakevenArmed bool
	TrailingArmed  bool
	StopPrice      float64
	ConfigJSON     string
	UpdatedAt      int64 `gorm:"autoUpdateTime:false"`
}

func (PositionProfitState) TableName() string { return "position_profit_states" }

type ProfitStateStore struct {
	db *gorm.DB
	mu sync.Mutex
}
type profitStoreInit struct {
	once  sync.Once
	store *ProfitStateStore
	err   error
}

var profitStores sync.Map

// ProfitState initializes its independent table once per Store, not each monitor
// tick. It does not migrate strategy JSON or import untrusted legacy peaks.
func (s *Store) ProfitState() (*ProfitStateStore, error) {
	if s == nil || s.gdb == nil {
		return nil, fmt.Errorf("profit state requires a database")
	}
	v, _ := profitStores.LoadOrStore(s, &profitStoreInit{})
	init := v.(*profitStoreInit)
	init.once.Do(func() {
		init.err = s.gdb.AutoMigrate(&PositionProfitState{})
		if init.err == nil {
			init.store = &ProfitStateStore{db: s.gdb}
		}
	})
	return init.store, init.err
}

func (s *ProfitStateStore) Get(traderID, symbol, side string) (*PositionProfitState, error) {
	var state PositionProfitState
	err := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, symbol, side).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &state, err
}

// Observe starts a clean lifecycle when identity or entry cost changes. opened
// must only be true immediately after a confirmed new open, not on startup.
func (s *ProfitStateStore) Observe(seed PositionProfitState, opened bool) (*PositionProfitState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.Get(seed.TraderID, seed.Symbol, seed.Side)
	if err != nil {
		return nil, false, err
	}
	fresh := state == nil || state.Lifecycle != seed.Lifecycle || state.EntryPrice != seed.EntryPrice
	if fresh {
		state = &seed
		if !opened {
			state.Eligible = false
			state.ConfigJSON = ""
		}
	} else {
		// Retrying an open hook must not reset arms, loosen stops or enable a
		// lifecycle first observed as pre-existing/disabled.
		if seed.PeakPricePct > state.PeakPricePct {
			state.PeakPricePct = seed.PeakPricePct
		}
	}
	state.UpdatedAt = time.Now().UnixMilli()
	err = s.db.Save(state).Error
	return state, fresh, err
}

// Save requires the same lifecycle to still exist (a concurrent close/reopen
// must not resurrect an old row). Callers serialize a slot for the whole pass.
func (s *ProfitStateStore) Save(state *PositionProfitState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.UpdatedAt = time.Now().UnixMilli()
	result := s.db.Model(&PositionProfitState{}).
		Where("trader_id = ? AND symbol = ? AND side = ? AND lifecycle = ? AND entry_price = ?", state.TraderID, state.Symbol, state.Side, state.Lifecycle, state.EntryPrice).
		Select("peak_price_pct", "legacy_armed", "breakeven_armed", "trailing_armed", "stop_price", "updated_at").Updates(state)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("profit lifecycle changed before save")
	}
	return nil
}

func (s *ProfitStateStore) Clear(traderID, symbol, side, lifecycle string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, symbol, side)
	if lifecycle != "" {
		q = q.Where("lifecycle = ?", lifecycle)
	}
	return q.Delete(&PositionProfitState{}).Error
}

// Prune removes slots absent from a successful complete position snapshot, but
// never a new open recorded after that snapshot started.
func (s *ProfitStateStore) Prune(traderID string, active map[string]bool, snapshotStarted int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var states []PositionProfitState
	if err := s.db.Where("trader_id = ? AND updated_at < ?", traderID, snapshotStarted).Find(&states).Error; err != nil {
		return err
	}
	for _, state := range states {
		if active[state.Symbol+"_"+state.Side] {
			continue
		}
		if err := s.db.Where("trader_id = ? AND symbol = ? AND side = ? AND lifecycle = ? AND updated_at < ?", traderID, state.Symbol, state.Side, state.Lifecycle, snapshotStarted).Delete(&PositionProfitState{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// Open force-creates the just-confirmed lifecycle. Unlike Observe this method
// is explicitly a lifecycle hook, so it may replace a pre-open discovery row.
func (s *ProfitStateStore) Open(state PositionProfitState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.UpdatedAt = time.Now().UnixMilli()
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trader_id = ? AND symbol = ? AND side = ?", state.TraderID, state.Symbol, state.Side).Delete(&PositionProfitState{}).Error; err != nil {
			return err
		}
		return tx.Create(&state).Error
	})
}
