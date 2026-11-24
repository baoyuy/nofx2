package store

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

func TestAccountRiskPersistsPeakAndResetsOnlyDailyBaseline(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{gdb: db}
	now := time.Date(2026, 11, 23, 23, 59, 0, 0, time.UTC)
	for _, equity := range []float64{1000, 1200, 1100} {
		if _, err = s.ObserveAccountRisk("t", equity, now); err != nil {
			t.Fatal(err)
		}
	}
	restarted := &Store{gdb: db}
	state, err := restarted.ObserveAccountRisk("t", 1050, now)
	if err != nil {
		t.Fatal(err)
	}
	if state.DayStartEquity != 1000 || state.PeakEquity != 1200 {
		t.Fatalf("restart reset state: %+v", state)
	}
	state, err = restarted.ObserveAccountRisk("t", 1060, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if state.DayStartEquity != 1060 || state.PeakEquity != 1200 {
		t.Fatalf("day reset lost peak: %+v", state)
	}
	if _, err = s.ObserveAccountRisk("t", math.NaN(), now); err == nil {
		t.Fatal("accepted NaN equity")
	}
}
