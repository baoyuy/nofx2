package store

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestDirectionResearchKeepsFirstBoardPerUserAndHour(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := NewCopyTradeStore(db)
	if err = st.InitTables(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []DirectionResearch{{UserID: "a", Slot: 1, At: 1000, Payload: "first"}, {UserID: "a", Slot: 1, At: 2000, Payload: "second"}, {UserID: "b", Slot: 1, At: 1000, Payload: "other"}} {
		if err = st.SaveDirectionResearch(&r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.PendingDirectionResearch(20000000)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	for _, r := range rows {
		if r.Payload == "second" {
			t.Fatal("overwrote original board")
		}
		if err = st.FinishDirectionResearch(r.ID, "[]"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = st.PendingDirectionResearch(20000000)
	if err != nil || len(rows) != 0 {
		t.Fatal("finished board still pending")
	}
}
