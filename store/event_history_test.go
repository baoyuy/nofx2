package store

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openEventHistory(t *testing.T, path string) (*EventHistoryStore, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	h := NewEventHistoryStore(db)
	if err := h.InitTables(); err != nil {
		t.Fatal(err)
	}
	return h, db
}
func eventHistorySample(at time.Time) EventHistorySample {
	return EventHistorySample{Source: "Kalshi", EventID: "e", Title: "BTC", URL: "https://kalshi.com/markets/e", Coins: []string{"BTC"}, ReceivedAt: at, Markets: []EventHistoryMarketSample{{MarketID: "m", Question: "Up?", PriceBasis: "last", Fingerprint: "f", Prices: []EventHistoryPrice{{Name: "Yes", Price: .5}}}}}
}
func TestEventHistoryPersistenceDuplicatesAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	h, db := openEventHistory(t, path)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sample := eventHistorySample(at)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.Record("u", at, []EventHistorySample{sample}, time.Hour); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int64
	db.Model(&EventHistorySnapshot{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicates: %d", count)
	}
	reopened, _ := openEventHistory(t, path)
	later := sample
	later.ReceivedAt = at.Add(time.Hour)
	result, err := reopened.Record("u", later.ReceivedAt, []EventHistorySample{later}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !result[0].FirstSeenAt.Equal(at) || len(result[0].Baselines) != 1 {
		t.Fatal(result)
	}
	result, err = reopened.Record("other", later.ReceivedAt, []EventHistorySample{later}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !result[0].FirstSeenAt.Equal(later.ReceivedAt) || len(result[0].Baselines) != 0 {
		t.Fatal("user isolation", result)
	}
}
func TestEventHistoryRetentionAndMetadata(t *testing.T) {
	h, db := openEventHistory(t, filepath.Join(t.TempDir(), "history.db"))
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sample := eventHistorySample(at)
	if _, err := h.Record("u", at, []EventHistorySample{sample}, time.Hour); err != nil {
		t.Fatal(err)
	}
	sample.ReceivedAt = at.Add(31 * 24 * time.Hour)
	sample.Markets[0].Prices = nil
	result, err := h.Record("u", sample.ReceivedAt, []EventHistorySample{sample}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&EventHistorySnapshot{}).Count(&count)
	if count != 0 {
		t.Fatal("retention", count)
	}
	// 4-day retention now also prunes event/market metadata that has no
	// retained snapshot, so local storage cannot grow without bound.
	db.Model(&EventHistoryMarket{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected old market metadata pruned, got %d", count)
	}
	db.Model(&EventHistoryEvent{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected old event metadata pruned, got %d", count)
	}
	_ = result
}
func TestEventHistoryInvalidTimestampRollback(t *testing.T) {
	h, db := openEventHistory(t, filepath.Join(t.TempDir(), "history.db"))
	at := time.Now().UTC()
	good := eventHistorySample(at)
	bad := eventHistorySample(at.Add(time.Hour))
	if _, err := h.Record("u", at, []EventHistorySample{good, bad}, time.Hour); err == nil {
		t.Fatal("accepted future")
	}
	var count int64
	db.Model(&EventHistoryEvent{}).Count(&count)
	if count != 0 {
		t.Fatal("not atomic")
	}
}

func TestEventHistoryPruneOlderThan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prune.db")
	h, _ := openEventHistory(t, path)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := h.Record("u", old, []EventHistorySample{eventHistorySample(old)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	// Prune everything before old+1h.
	if err := h.PruneOlderThan(old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var snaps, evts int64
	h.db.Model(&EventHistorySnapshot{}).Count(&snaps)
	h.db.Model(&EventHistoryEvent{}).Count(&evts)
	if snaps != 0 || evts != 0 {
		t.Fatalf("expected prune to remove old rows, snaps=%d evts=%d", snaps, evts)
	}
}
