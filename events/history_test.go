package events

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"math"
	"nofx/store"
	"path/filepath"
	"testing"
	"time"
)

func historyDB(t *testing.T) (*store.EventHistoryStore, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "history.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	h := store.NewEventHistoryStore(db)
	if err := h.InitTables(); err != nil {
		t.Fatal(err)
	}
	return h, db
}

var historyOpts = HistoryOptions{Window: time.Hour, ThresholdPP: 5, EndingWithin: 24 * time.Hour}

func historyBoard(at time.Time, p float64) Board {
	end := at.Add(10 * 24 * time.Hour)
	return Board{GeneratedAt: at, Events: []Event{{ID: "polymarket:e", Source: "Polymarket", Title: "BTC", ReceivedAt: at, Markets: []Market{{ID: "m", Question: "BTC up?", PriceBasis: "outcome", Outcomes: []Outcome{{Name: "Yes", Price: p}, {Name: "No", Price: 1 - p}}}}, EndAt: &end}}}
}
func annotate(t *testing.T, h *store.EventHistoryStore, user string, b *Board) {
	t.Helper()
	if err := AnnotateHistory(h, user, b, historyOpts); err != nil {
		t.Fatal(err)
	}
}
func TestHistoryComparison(t *testing.T) {
	h, _ := historyDB(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	old := historyBoard(at, .5)
	annotate(t, h, "u", &old)
	current := historyBoard(at.Add(time.Hour), .55)
	current.Events[0].EndAt = old.Events[0].EndAt
	outcomes := current.Events[0].Markets[0].Outcomes
	outcomes[0], outcomes[1] = outcomes[1], outcomes[0]
	annotate(t, h, "u", &current)
	e := current.Events[0]
	m := e.Markets[0]
	if m.HistoryStatus != "available" || len(m.Changes) != 2 || !e.SignificantChange || !e.FirstSeenAt.Equal(at) {
		t.Fatalf("unexpected annotation: %+v %+v", e, m)
	}
	if m.Changes[1].Outcome != "Yes" || math.Abs(m.Changes[1].DeltaPP-5) > 1e-8 {
		t.Fatal(m.Changes)
	}
	isolated := current
	isolated.Events = append([]Event{}, current.Events...)
	annotate(t, h, "other", &isolated)
	if isolated.Events[0].Markets[0].HistoryStatus != "insufficient" || !isolated.Events[0].FirstSeenAt.Equal(current.GeneratedAt) {
		t.Fatal("user leaked")
	}
}
func TestHistorySafety(t *testing.T) {
	for _, tc := range []struct {
		name        string
		elapsed     time.Duration
		mutate      func(*Board)
		status      string
		significant bool
	}{
		{"too recent", 30 * time.Minute, nil, "insufficient", false},
		{"gap", 2*time.Hour + time.Second, nil, "insufficient", false},
		{"oldest boundary", 2 * time.Hour, nil, "available", true},
		{"question", time.Hour, func(b *Board) { b.Events[0].Markets[0].Question = "different" }, "insufficient", false},
		{"rules", time.Hour, func(b *Board) { b.Events[0].Markets[0].Rules = "different" }, "insufficient", false},
		{"schema", time.Hour, func(b *Board) { b.Events[0].Markets[0].Outcomes[0].Name = "Up" }, "insufficient", false},
		{"basis", time.Hour, func(b *Board) { b.Events[0].Markets[0].PriceBasis = "bid" }, "insufficient", false},
		{"below threshold", time.Hour, func(b *Board) { b.Events[0].Markets[0].Outcomes = []Outcome{{"Yes", .54999}, {"No", .45001}} }, "available", false},
		{"stale", time.Hour, func(b *Board) { b.GeneratedAt = b.GeneratedAt.Add(16 * time.Minute) }, "stale", false},
		{"expired", time.Hour, func(b *Board) { end := b.GeneratedAt.Add(-time.Second); b.Events[0].Markets[0].EndAt = &end }, "stale", false},
		{"repeat cache", time.Hour, func(b *Board) { b.Events[0].ReceivedAt = b.Events[0].ReceivedAt.Add(-time.Hour) }, "stale", false},
		{"empty", time.Hour, func(b *Board) { b.Events[0].Markets[0].Outcomes = nil }, "insufficient", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := historyDB(t)
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			old := historyBoard(at, .5)
			annotate(t, h, "u", &old)
			b := historyBoard(at.Add(tc.elapsed), .6)
			b.Events[0].EndAt = old.Events[0].EndAt
			if tc.mutate != nil {
				tc.mutate(&b)
			}
			annotate(t, h, "u", &b)
			if b.Events[0].Markets[0].HistoryStatus != tc.status || b.Events[0].SignificantChange != tc.significant {
				t.Fatalf("%+v", b.Events[0])
			}
			if b.Events[0].Markets[0].Changes == nil {
				t.Fatal("nil changes")
			}
		})
	}
}
func TestHistoryInvalidAndFailure(t *testing.T) {
	for _, p := range []float64{math.NaN(), math.Inf(1), -.01, 1.01} {
		h, db := historyDB(t)
		b := historyBoard(time.Now().UTC(), p)
		annotate(t, h, "u", &b)
		var count int64
		db.Model(&store.EventHistorySnapshot{}).Count(&count)
		if count != 0 {
			t.Fatal("invalid market partially retained")
		}
		if b.Events[0].Markets[0].HistoryStatus != "unavailable" {
			t.Fatal("invalid available")
		}
	}
	b := historyBoard(time.Now().UTC(), .5)
	if AnnotateHistory(nil, "u", &b, historyOpts) == nil || b.Events[0].Markets[0].Changes == nil || b.Events[0].Markets[0].HistoryStatus != "unavailable" {
		t.Fatal("failure not surfaced")
	}
}
func TestHistoryDoesNotMutateCachedMarkets(t *testing.T) {
	h, _ := historyDB(t)
	b := historyBoard(time.Now().UTC(), .5)
	cached := b.Events[0].Markets
	annotate(t, h, "u", &b)
	if cached[0].HistoryStatus != "" || cached[0].Changes != nil {
		t.Fatal("cache mutated")
	}
}

func TestHistoryBaselineSelectionAndNewLifetime(t *testing.T) {
	h, _ := historyDB(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := historyBoard(at, .4)
	annotate(t, h, "u", &base)
	for _, offset := range []time.Duration{30 * time.Minute, 90 * time.Minute} {
		b := historyBoard(at.Add(offset), .5)
		b.Events[0].EndAt = base.Events[0].EndAt
		annotate(t, h, "u", &b)
	}
	// At 100m the target is 40m: select 30m, not the newer 90m sample.
	b := historyBoard(at.Add(100*time.Minute), .6)
	b.Events[0].EndAt = base.Events[0].EndAt
	annotate(t, h, "u", &b)
	changes := b.Events[0].Markets[0].Changes
	if len(changes) != 2 || !changes[0].FromAt.Equal(at.Add(30*time.Minute)) {
		t.Fatal(changes)
	}
	// A fresh user's first observation uses board time even for cached data.
	b.GeneratedAt = b.GeneratedAt.Add(5 * time.Minute)
	annotate(t, h, "fresh", &b)
	if !b.Events[0].FirstSeenAt.Equal(b.GeneratedAt) {
		t.Fatal("cached timestamp used as first seen")
	}
	later := historyBoard(at.Add(25*time.Hour), .6)
	later.Events[0].EndAt = base.Events[0].EndAt
	annotate(t, h, "u", &later)
	if later.Events[0].IsNew || !later.Events[0].FirstSeenAt.Equal(at) {
		t.Fatal("new lifetime incorrect")
	}
}

func TestArticleHistoryMetadataWithoutPrices(t *testing.T) {
	h, db := historyDB(t)
	at := time.Now().UTC()
	e := Event{ID: "free-crypto-news:a", Source: "Free Crypto News", Kind: "news", Title: "Bitcoin", Publisher: "Publisher", Summary: "Unverified report", ArticleType: "report", URL: "https://example.com/a", ReceivedAt: at, Markets: []Market{}}
	b := Board{GeneratedAt: at, Events: []Event{e}}
	annotate(t, h, "u", &b)
	if b.Events[0].FirstSeenAt == nil || !b.Events[0].IsNew || b.Events[0].SignificantChange {
		t.Fatal(b)
	}
	b.GeneratedAt = at.Add(time.Hour)
	annotate(t, h, "u", &b)
	if !b.Events[0].FirstSeenAt.Equal(at) {
		t.Fatal("first seen changed")
	}
	var saved store.EventHistoryEvent
	if err := db.First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Kind != e.Kind || saved.Publisher != e.Publisher || saved.Summary != e.Summary || saved.ArticleType != e.ArticleType {
		t.Fatal(saved)
	}
	var count int64
	db.Model(&store.EventHistorySnapshot{}).Count(&count)
	if count != 0 {
		t.Fatal("synthetic snapshot", count)
	}
	if e.FirstSeenAt != nil || e.Markets == nil {
		t.Fatal("provider cache mutated")
	}
}
