package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestMarketAnalysisSaveReplacesPerSymbolAndIsUserScoped(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "nofx.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := st.MarketAnalysis()

	if err := m.Save("u1", "BTCUSDT", `{"v":"old"}`, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Save("u1", "BTCUSDT", `{"v":"new"}`, 2); err != nil {
		t.Fatal(err)
	}
	if err := m.Save("u2", "BTCUSDT", `{"v":"other user"}`, 3); err != nil {
		t.Fatal(err)
	}

	rows, err := m.List("u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Result != `{"v":"new"}` || rows[0].AnalyzedAt != 2 {
		t.Fatalf("u1 rows = %+v", rows)
	}
	if rows, _ := m.List("u2"); len(rows) != 1 || rows[0].Result != `{"v":"other user"}` {
		t.Fatalf("u2 rows = %+v", rows)
	}
}

func TestMarketAnalysisKeepsNewestPerUser(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "nofx.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := st.MarketAnalysis()

	for i := 0; i < MaxMarketAnalysesPerUser+5; i++ {
		if err := m.Save("u1", fmt.Sprintf("C%dUSDT", i), "{}", int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := m.List("u1")
	if len(rows) != MaxMarketAnalysesPerUser {
		t.Fatalf("kept %d rows, want %d", len(rows), MaxMarketAnalysesPerUser)
	}
	if rows[0].Symbol != fmt.Sprintf("C%dUSDT", MaxMarketAnalysesPerUser+4) {
		t.Fatalf("newest first expected, got %s", rows[0].Symbol)
	}
	var count int64
	st.GormDB().Model(&MarketAnalysis{}).Where("user_id = ?", "u1").Count(&count)
	if count != MaxMarketAnalysesPerUser {
		t.Fatalf("table still holds %d rows for u1", count)
	}
}
