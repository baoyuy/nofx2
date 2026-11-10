package copytrade

import (
	"context"
	"testing"
	"time"
)

func TestNewFillsBetweenPagesRestartWithoutDuplicates(t *testing.T) {
	for _, insertAfterFetch := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_page", true: "after_page"}[insertAfterFetch], func(t *testing.T) {
			m, fake := newTestMonitor(t)
			ctx := context.Background()
			start := time.Now().Add(-time.Hour)
			for i := 0; i < TradePageSize+1; i++ {
				fake.add(start.Add(time.Duration(i)*time.Millisecond), "BUY", "LONG", 1, 100, 0)
			}
			f, err := m.Follow(ctx, "42")
			if err != nil {
				t.Fatal(err)
			}
			pending, err := m.syncFillsPage(ctx, f, nil)
			if err != nil {
				t.Fatal(err)
			}
			if insertAfterFetch {
				pending, err = m.syncFillsPage(ctx, f, pending)
				if err != nil {
					t.Fatal(err)
				}
			}
			fake.add(start.Add(time.Second), "BUY", "LONG", 1, 101, 0)
			for i := 0; i < 10 && pending != nil; i++ {
				pending, err = m.syncFillsPage(ctx, f, pending)
				if err != nil {
					t.Fatal(err)
				}
			}
			if pending != nil {
				t.Fatal("history did not converge after head stabilized")
			}
			rows, err := m.store.Fills("42", 0)
			if err != nil || len(rows) != TradePageSize+2 {
				t.Fatalf("offset shift duplicated or lost fills: %d %v", len(rows), err)
			}
			if f.FillsUntil != start.Add(time.Second).UnixMilli() {
				t.Fatal("watermark did not follow realigned head")
			}
		})
	}
}

func TestHistoryContinuesBeyondOldPageCap(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	const count = 25*TradePageSize + 1
	for i := 0; i < count; i++ {
		fake.add(start.Add(time.Duration(i)*time.Millisecond), "BUY", "LONG", 1, 100, 0)
	}
	f, err := m.Follow(ctx, "42")
	if err != nil {
		t.Fatal(err)
	}
	var pending *fillSync
	for i := 0; i < 60; i++ {
		pending, err = m.syncFillsPage(ctx, f, pending)
		if err != nil {
			t.Fatal(err)
		}
		if pending == nil {
			break
		}
		if f.FillsUntil != 0 {
			t.Fatal("watermark advanced before reaching retained history")
		}
	}
	if pending != nil {
		t.Fatal("history did not complete")
	}
	rows, err := m.store.Fills("42", 0)
	if err != nil || len(rows) != count {
		t.Fatalf("old page cap silently skipped history: %d %v", len(rows), err)
	}
}
