package copytrade

import (
	"context"
	"strings"
	"testing"
	"time"

	"nofx/store"
)

func TestPositionRefreshDoesNotFreshenHistoryOrClearItsError(t *testing.T) {
	m, _ := newTestMonitor(t)
	if _, err := m.Follow(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.snaps["42"]
	s.FillsError = "trade history unavailable"
	s.fillsDue, s.profileDue = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	m.mu.Unlock()
	m.refresh(context.Background(), "42")
	snap, _ := m.Snapshot("42")
	if snap.PositionsUpdatedAt.IsZero() || !snap.FillsUpdatedAt.IsZero() || snap.HistoryComplete ||
		!strings.Contains(snap.Error, "trade history unavailable") || snap.FillsError == "" {
		t.Fatalf("position success must not overwrite history state: %+v", snap)
	}
	if len(Fresh([]Snapshot{snap}, 30*time.Minute, time.Now())) != 0 {
		t.Fatal("partial history counted as complete AI data")
	}
}

func TestCompletedFillTimestampSurvivesProfileAndPartialWrites(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	fake.add(time.Now().Add(-time.Hour), "BUY", "LONG", 1, 100, 0)
	if _, err := m.Follow(ctx, "42"); err != nil {
		t.Fatal(err)
	}
	refreshRound(t, m, "42")
	snap, _ := m.Snapshot("42")
	if !snap.Quality(30*time.Minute, time.Now()).Complete() {
		t.Fatalf("completed snapshot not ready: %+v", snap)
	}
	stored, err := m.store.GetFollow("42")
	if err != nil || stored.FillsSyncedAt == 0 {
		t.Fatalf("missing durable completion: %+v %v", stored, err)
	}
	at := stored.FillsSyncedAt
	stored.ProfileAt = time.Now().Add(time.Minute).UnixMilli()
	if err := m.store.SaveFollow(stored); err != nil {
		t.Fatal(err)
	}
	// Partial persistence must not advance the full completion timestamp.
	if err := m.store.ReplaceFills("42", stored.FillsUntil+1, nil, stored.FillsUntil); err != nil {
		t.Fatal(err)
	}
	again := NewMonitor(m.store)
	again.Load()
	restored, _ := again.Snapshot("42")
	if restored.FillsUpdatedAt.UnixMilli() != at || restored.HistoryComplete {
		t.Fatalf("partial history must retain its timestamp but not completeness after restart: %+v", restored)
	}
	if len(Fresh([]Snapshot{restored}, time.Hour, time.Now())) != 0 {
		t.Fatal("partial persistence became complete AI data after restart")
	}
	if !restored.PositionsUpdatedAt.IsZero() {
		t.Fatal("restart claimed public positions were freshly fetched")
	}
}

func TestLegacyRowUpdateIsNotACompletedHistorySync(t *testing.T) {
	m, _ := newTestMonitor(t)
	f := store.CopyTradeFollow{PortfolioID: "legacy", FillsUntil: time.Now().UnixMilli(), ProfileAt: time.Now().UnixMilli()}
	if err := m.store.SaveFollow(&f); err != nil {
		t.Fatal(err)
	}
	m.Load()
	s, _ := m.Snapshot("legacy")
	if !s.FillsUpdatedAt.IsZero() || s.HistoryComplete || len(Fresh([]Snapshot{s}, time.Hour, time.Now())) != 0 {
		t.Fatalf("legacy row update time fabricated fresh history: %+v", s)
	}
}

func TestSnapshotQualityChecksEachComponentAndAge(t *testing.T) {
	now := time.Now()
	base := Snapshot{PositionsUpdatedAt: now.Add(-time.Minute), FillsUpdatedAt: now.Add(-time.Minute), ProfileUpdatedAt: now.Add(-time.Hour), HistoryComplete: true}
	if !base.Quality(30*time.Minute, now).Complete() {
		t.Fatal("valid snapshot not ready")
	}
	for _, mutate := range []func(*Snapshot){
		func(s *Snapshot) { s.FillsError = "history failed" },
		func(s *Snapshot) { s.PositionsError = "positions failed" },
		func(s *Snapshot) { s.ProfileError = "profile failed" },
		func(s *Snapshot) { s.FillsUpdatedAt = now.Add(-time.Hour) },
		func(s *Snapshot) { s.ProfileUpdatedAt = now.Add(-8 * time.Hour) },
		func(s *Snapshot) { s.HistoryComplete = false },
		func(s *Snapshot) { s.PositionsUpdatedAt = now.Add(time.Minute) },
	} {
		s := base
		mutate(&s)
		if s.Quality(30*time.Minute, now).Complete() {
			t.Fatalf("invalid component accepted: %+v", s)
		}
	}
}
