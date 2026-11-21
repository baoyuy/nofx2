package store

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openEventCards(t *testing.T) (*EventCardStore, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cards.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	s := NewEventCardStore(db)
	if err := s.InitTables(); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestEventCardReplaceAndList(t *testing.T) {
	s, _ := openEventCards(t)
	now := time.Now().UTC()
	card := EventCard{UserID: "u", TraderID: "t", Coin: "BTC", CardID: "c1", Type: "earnings", Title: "财报超预期", Direction: "bullish", Surprise: "above", Importance: 80, TaggedAt: now}
	if err := s.ReplaceForTraderCoin("u", "t", "BTC", []EventCard{card}); err != nil {
		t.Fatal(err)
	}
	// Replace with a different set: old card must be gone.
	card2 := EventCard{UserID: "u", TraderID: "t", Coin: "BTC", CardID: "c2", Type: "etf", Title: "ETF 通过", Direction: "bullish", Importance: 90, TaggedAt: now}
	if err := s.ReplaceForTraderCoin("u", "t", "BTC", []EventCard{card2}); err != nil {
		t.Fatal(err)
	}
	cards, err := s.ListByUser("u")
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].CardID != "c2" {
		t.Fatalf("expected only c2, got %+v", cards)
	}
	// Replace with empty set clears all.
	if err := s.ReplaceForTraderCoin("u", "t", "BTC", nil); err != nil {
		t.Fatal(err)
	}
	cards, _ = s.ListByUser("u")
	if len(cards) != 0 {
		t.Fatalf("expected empty after clear, got %+v", cards)
	}
}

func TestEventCardDeleteOlderThan(t *testing.T) {
	s, _ := openEventCards(t)
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)
	card := EventCard{UserID: "u", TraderID: "t", Coin: "ETH", CardID: "c1", Type: "macro", TaggedAt: old}
	if err := s.ReplaceForTraderCoin("u", "t", "ETH", []EventCard{card}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOlderThan(time.Now().UTC().Add(-4 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	cards, _ := s.ListByUser("u")
	if len(cards) != 0 {
		t.Fatalf("expected prune, got %+v", cards)
	}
}

func TestEventCardUpsertAndExpire(t *testing.T) {
	s, _ := openEventCards(t)
	now := time.Now().UTC()
	c1 := EventCard{UserID: "u", TraderID: "t", Coin: "BTC", CardID: "c1", Type: "earnings", Title: "v1", TaggedAt: now}
	if err := s.UpsertMany([]EventCard{c1}); err != nil {
		t.Fatal(err)
	}
	c1.Title = "v2"
	if err := s.UpsertMany([]EventCard{c1}); err != nil {
		t.Fatal(err)
	}
	cards, _ := s.ListByUser("u")
	if len(cards) != 1 || cards[0].Title != "v2" {
		t.Fatalf("upsert should update in place, got %+v", cards)
	}
	old := EventCard{UserID: "u", TraderID: "t", Coin: "ETH", CardID: "c2", Type: "macro", Title: "old", TaggedAt: now.Add(-10 * 24 * time.Hour)}
	if err := s.UpsertMany([]EventCard{old}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExpired(now, 48*time.Hour, 6*time.Hour); err != nil {
		t.Fatal(err)
	}
	cards, _ = s.ListByUser("u")
	if len(cards) != 1 || cards[0].CardID != "c1" {
		t.Fatalf("expired card should be removed, kept fresh one, got %+v", cards)
	}
}
