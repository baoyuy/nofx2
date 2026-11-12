package store

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Rows written before the new signal columns existed hold NULL in them: they
// must still load, as zero values, or the whole hit-rate history is lost.
func TestSignalRowsFromBeforeTheNewColumnsStillLoad(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE copytrade_signals (id integer primary key autoincrement, hour integer not null,
		symbol text not null, direction text, confidence real, traders integer, price real, ret_4h real, ret_24h real,
		done_4h numeric, done_24h numeric)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO copytrade_signals (hour, symbol, direction, confidence, traders, price, ret_4h, done_4h, done_24h)
		VALUES (3600, 'BTCUSDT', 'long', 50, 3, 100, 0.02, 1, 0)`).Error; err != nil {
		t.Fatal(err)
	}
	st := NewCopyTradeStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	for name, load := range map[string]func() ([]CopyTradeSignal, error){
		"since":   func() ([]CopyTradeSignal, error) { return st.SignalsSince(0) },
		"at":      func() ([]CopyTradeSignal, error) { return st.SignalsAt(3600) },
		"pending": func() ([]CopyTradeSignal, error) { return st.PendingSignals(1 << 40) },
	} {
		rows, err := load()
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: old rows did not load: %d %v", name, len(rows), err)
		}
		r := rows[0]
		if r.Features != 0 || r.LongShare != 0 || r.ShadowDirection != "" || !r.Ranked || !r.Done72h || r.Ret4h == nil {
			t.Fatalf("%s: %+v", name, r)
		}
	}
}
