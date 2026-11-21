package store

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openTradeTags(t *testing.T) (*EventTradeTagStore, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tags.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	s := NewEventTradeTagStore(db)
	if err := s.InitTables(); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestEventTradeTagUpsertListDelete(t *testing.T) {
	s, _ := openTradeTags(t)
	now := time.Now().UTC()
	if err := s.Upsert(EventTradeTag{UserID: "u", TraderID: "t", Coin: "BTC", Bias: "bearish", Confidence: 70, Reason: "r1", TaggedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Upsert same key overwrites.
	if err := s.Upsert(EventTradeTag{UserID: "u", TraderID: "t", Coin: "BTC", Bias: "bullish", Confidence: 80, Reason: "r2", TaggedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	tags, err := s.ListByUser("u")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Bias != "bullish" || tags[0].Confidence != 80 {
		t.Fatalf("unexpected tags: %+v", tags)
	}
	if err := s.DeleteOlderThan(now.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	tags, _ = s.ListByUser("u")
	if len(tags) != 0 {
		t.Fatalf("expected prune to delete, got %+v", tags)
	}
}
