package copytrade

import (
	"context"
	"testing"
	"time"

	"nofx/store"
)

// Drain a complete due round in tests; production yields to the scheduler
// between these tasks, and never ignores their deadlines.
func refreshRound(t *testing.T, m *Monitor, id string) {
	t.Helper()
	m.mu.Lock()
	s := m.snaps[id]
	s.positionDue, s.fillsDue = time.Time{}, time.Time{}
	m.mu.Unlock()
	for i := 0; i < 1000; i++ {
		m.mu.RLock()
		_, due := s.task()
		m.mu.RUnlock()
		if due.After(time.Now()) {
			return
		}
		m.refresh(context.Background(), id)
	}
	t.Fatal("refresh tasks did not yield")
}

func TestRefreshPublishesPositionsBeforeHistoryAndProfile(t *testing.T) {
	m, fake := newTestMonitor(t)
	if _, err := m.Follow(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	m.refresh(context.Background(), "42")
	s, _ := m.Snapshot("42")
	if s.PositionsSource != "public" || len(s.Positions) != 1 || s.UpdatedAt.IsZero() {
		t.Fatalf("live positions must publish after one request: %+v", s)
	}
	if fake.calls["/v1/friendly/future/copy-trade/lead-portfolio/trade-history"] != 0 ||
		fake.calls["/v1/public/future/copy-trade/lead-portfolio/performance"] != 0 {
		t.Fatal("history/profile blocked the live position update")
	}
}

func TestHistoryPageYieldsAndPreservesSplitTimestamps(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	boundary := time.Now().Add(-time.Hour)
	// Split the same millisecond over the page boundary; no partial order
	// at that timestamp should be published before its remaining fills.
	for i := 0; i < TradePageSize+5; i++ {
		fake.add(boundary, "BUY", "LONG", 1, 100, 0)
	}
	fake.add(boundary.Add(time.Minute), "BUY", "LONG", 1, 101, 0)
	f, err := m.Follow(ctx, "42")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := m.syncFillsPage(ctx, f, nil)
	if err != nil || pending == nil || pending.page != 1 || f.FillsUntil != 0 {
		t.Fatalf("first page must yield without advancing watermark: %+v %v", pending, err)
	}
	rows, _ := m.store.Fills("42", 0)
	if len(rows) != 1 || rows[0].Price != 101 {
		t.Fatalf("latest complete fills should be immediately visible: %+v", rows)
	}
	stored, _ := m.store.GetFollow("42")
	if stored.FillsUntil != 0 {
		t.Fatal("partial history incorrectly marked as complete")
	}
	pending, err = m.syncFillsPage(ctx, f, pending)
	if err != nil || pending == nil || pending.candidate == nil {
		t.Fatalf("second page must await head validation: %+v %v", pending, err)
	}
	pending, err = m.syncFillsPage(ctx, f, pending)
	if err != nil || pending != nil || f.FillsUntil != boundary.Add(time.Minute).UnixMilli() {
		t.Fatalf("second page should complete history: %+v %v", pending, err)
	}
	rows, _ = m.store.Fills("42", 0)
	if len(rows) != TradePageSize+6 {
		t.Fatalf("split fills were lost or duplicated: got %d", len(rows))
	}
	pending, err = m.syncFillsPage(ctx, f, nil)
	if err != nil || pending != nil {
		t.Fatalf("incremental repeat: %v", err)
	}
	rows, _ = m.store.Fills("42", 0)
	if len(rows) != TradePageSize+6 {
		t.Fatalf("repeat duplicated fills: %d", len(rows))
	}
}

func TestDueLiveWorkPreemptsHistoryContinuation(t *testing.T) {
	m, _ := newTestMonitor(t)
	now := time.Now()
	m.snaps["history"] = &Snapshot{
		Follow:   store.CopyTradeFollow{PortfolioID: "history"},
		fillsDue: now, profileDue: now.Add(time.Hour),
	}
	m.snaps["live"] = &Snapshot{
		Follow:      store.CopyTradeFollow{PortfolioID: "live", PositionShow: true},
		positionDue: now.Add(-time.Second), fillsDue: now.Add(time.Hour), profileDue: now.Add(time.Hour),
	}
	id, _ := m.nextDue()
	if id != "live" {
		t.Fatalf("due live work must preempt another history page: %s", id)
	}
}

func TestHistoryFailureDoesNotBlockLiveRefresh(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	if _, err := m.Follow(ctx, "42"); err != nil {
		t.Fatal(err)
	}
	refreshRound(t, m, "42")
	m.mu.Lock()
	s := m.snaps["42"]
	s.Follow.ProfileAt = 0
	s.profileDue = time.Time{}
	m.mu.Unlock()
	fake.mu.Lock()
	fake.renamed = true // positions remain healthy while fills are malformed
	fake.mu.Unlock()
	m.mu.Lock()
	s.fillsDue = time.Time{}
	m.mu.Unlock()
	// With an empty history, this still succeeds; exercise an explicit failed
	// fills task by adding a malformed-format response through renamed fields.
	fake.add(time.Now(), "BUY", "LONG", 1, 100, 0)
	m.refresh(ctx, "42")
	m.mu.Lock()
	s.positionDue = time.Time{}
	s.profileDue = time.Now().Add(time.Hour)
	m.mu.Unlock()
	before := fake.calls["/v1/friendly/future/copy-trade/lead-data/positions"]
	m.refresh(ctx, "42")
	if fake.calls["/v1/friendly/future/copy-trade/lead-data/positions"] != before+1 {
		t.Fatal("a failed history task must not delay live positions for errorInterval")
	}
}
