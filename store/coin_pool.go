package store

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// MaxCoinPoolsPerUser bounds how many saved pools one user keeps.
	MaxCoinPoolsPerUser = 50
	// MaxCoinPoolCoins bounds one pool. A strategy still trades at most
	// MaxCandidateCoins of them per cycle.
	MaxCoinPoolCoins = 50
)

var coinPoolSymbol = regexp.MustCompile(`^[A-Z0-9\p{Han}]{1,20}USDT$`)

// CoinPoolCoin is one coin saved from a screener, with the screener's view
// of it at save time.
type CoinPoolCoin struct {
	Symbol    string  `json:"symbol"`
	Direction string  `json:"direction,omitempty"` // long | short | neutral
	Score     float64 `json:"score,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// CoinPool is a named list of coins a user picked on the coin pool page.
// Strategies with coin source "coin_pool" trade the coins of one pool.
type CoinPool struct {
	ID        string         `gorm:"column:id;primaryKey" json:"id"`
	UserID    string         `gorm:"column:user_id;not null;index" json:"-"`
	Name      string         `gorm:"column:name;not null" json:"name"`
	Screener  string         `gorm:"column:screener" json:"screener"` // e.g. bollinger_squeeze
	Interval  string         `gorm:"column:interval_" json:"interval"`
	CoinsJSON string         `gorm:"column:coins;type:text;not null" json:"-"`
	Coins     []CoinPoolCoin `gorm:"-" json:"coins"`
	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
}

func (CoinPool) TableName() string { return "coin_pools" }

type CoinPoolStore struct {
	db *gorm.DB
}

func NewCoinPoolStore(db *gorm.DB) *CoinPoolStore { return &CoinPoolStore{db: db} }

func (s *CoinPoolStore) initTables() error { return s.db.AutoMigrate(&CoinPool{}) }

// cleanPool normalizes symbols, drops duplicates and enforces limits.
func cleanPool(p *CoinPool) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len([]rune(p.Name)) > 60 {
		return errors.New("pool name must be 1-60 characters")
	}
	seen := map[string]bool{}
	coins := make([]CoinPoolCoin, 0, len(p.Coins))
	for _, c := range p.Coins {
		c.Symbol = strings.ToUpper(strings.TrimSpace(c.Symbol))
		if !coinPoolSymbol.MatchString(c.Symbol) || seen[c.Symbol] {
			continue
		}
		if c.Direction != "long" && c.Direction != "short" {
			c.Direction = "neutral"
		}
		if len([]rune(c.Note)) > 200 {
			c.Note = string([]rune(c.Note)[:200])
		}
		seen[c.Symbol] = true
		coins = append(coins, c)
	}
	if len(coins) == 0 {
		return errors.New("pool needs at least one coin")
	}
	if len(coins) > MaxCoinPoolCoins {
		return errors.New("pool has too many coins")
	}
	p.Coins = coins
	raw, err := json.Marshal(coins)
	if err != nil {
		return err
	}
	p.CoinsJSON = string(raw)
	return nil
}

func decodePool(p *CoinPool) {
	_ = json.Unmarshal([]byte(p.CoinsJSON), &p.Coins)
	if p.Coins == nil {
		p.Coins = []CoinPoolCoin{}
	}
}

// List returns the user's pools, most recently updated first.
func (s *CoinPoolStore) List(userID string) ([]CoinPool, error) {
	var rows []CoinPool
	if err := s.db.Where("user_id = ?", userID).Order("updated_at DESC").Limit(MaxCoinPoolsPerUser).Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		decodePool(&rows[i])
	}
	return rows, nil
}

// Get returns one pool owned by the user.
func (s *CoinPoolStore) Get(userID, id string) (*CoinPool, error) {
	var p CoinPool
	if err := s.db.Where("user_id = ? AND id = ?", userID, id).First(&p).Error; err != nil {
		return nil, err
	}
	decodePool(&p)
	return &p, nil
}

func (s *CoinPoolStore) Create(userID string, p *CoinPool) error {
	if err := cleanPool(p); err != nil {
		return err
	}
	var count int64
	if err := s.db.Model(&CoinPool{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return err
	}
	if count >= MaxCoinPoolsPerUser {
		return errors.New("too many coin pools; delete an old one first")
	}
	p.ID, p.UserID = uuid.NewString(), userID
	return s.db.Create(p).Error
}

// Update replaces the name and coins of a pool owned by the user.
func (s *CoinPoolStore) Update(userID string, p *CoinPool) error {
	if err := cleanPool(p); err != nil {
		return err
	}
	result := s.db.Model(&CoinPool{}).Where("user_id = ? AND id = ?", userID, p.ID).Updates(map[string]any{
		"name": p.Name, "screener": p.Screener, "interval_": p.Interval, "coins": p.CoinsJSON, "updated_at": time.Now(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *CoinPoolStore) Delete(userID, id string) error {
	result := s.db.Where("user_id = ? AND id = ?", userID, id).Delete(&CoinPool{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Symbols returns a pool's symbols by ID. Strategies run without a user
// context, so ownership is checked when the strategy is saved.
func (s *CoinPoolStore) Symbols(id string) ([]string, error) {
	var p CoinPool
	if err := s.db.Where("id = ?", id).First(&p).Error; err != nil {
		return nil, err
	}
	decodePool(&p)
	out := make([]string, 0, len(p.Coins))
	for _, c := range p.Coins {
		out = append(out, c.Symbol)
	}
	return out, nil
}
