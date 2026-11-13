package explosive

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"nofx/store"
)

func testRecorder(t *testing.T) *Recorder {
	t.Helper()
	dsn := "file:rec" + strconv.FormatInt(time.Now().UnixNano(), 10) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewExplosiveStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	return NewRecorder(st)
}

func TestSnapshotStoresTheBoardOnce(t *testing.T) {
	r := testRecorder(t)
	now := time.Now()
	r.now = func() time.Time { return now }
	r.board = func(context.Context) (Board, error) {
		return Board{Rows: []Row{
			{Symbol: "AUSDT", Price: 10, Score: 70, Strength: 72, Risk: 0.97, State: StateAccelerating,
				Change24h: 5, Tags: []string{TagBreakout}},
			{Symbol: "BUSDT", Price: 2, Score: 40, Strength: 44, Risk: 0.9, State: StateStarting},
		}}, nil
	}
	if err := r.snapshot(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	bucket := now.Truncate(snapshotEvery).Unix()
	done, err := r.store.HasSignals(bucket)
	if err != nil || !done {
		t.Fatalf("snapshot not stored: done=%v err=%v", done, err)
	}
	rows, err := r.store.SignalsSince(bucket - 1)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d err=%v", len(rows), err)
	}
	if rows[0].Symbol != "AUSDT" || rows[0].Strength != 72 || rows[0].Tags != `["breakout"]` {
		t.Fatalf("stored row: %+v", rows[0])
	}
	// The same bucket must not be written twice.
	r.board = func(context.Context) (Board, error) { return Board{Rows: []Row{{Symbol: "CUSDT"}}}, nil }
	if err := r.snapshot(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows, _ = r.store.SignalsSince(bucket - 1)
	if len(rows) != 2 {
		t.Fatalf("bucket recorded twice: %d rows", len(rows))
	}
}

func TestEvaluateFillsForwardReturns(t *testing.T) {
	r := testRecorder(t)
	now := time.Now()
	ts := now.Add(-2 * time.Hour).Unix()
	err := r.store.SaveSignals([]store.ExplosiveSignal{
		{TS: ts, Symbol: "AUSDT", Price: 100, Rank: 1, Strength: 80, Score: 80, Done24h: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.rangeBars = func(_ context.Context, symbol, _ string, startMs, endMs int64) ([]Kline, error) {
		if symbol == "BTCUSDT" {
			return []Kline{
				{Time: startMs, Close: 100},
				{Time: endMs, Close: 100},
			}, nil
		}
		return []Kline{
			{Time: startMs, Low: 95, Close: 105},
			{Time: endMs - 300000, Low: 90, Close: 110},
		}, nil
	}
	if err := r.evaluate(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows, _ := r.store.SignalsSince(ts - 1)
	if len(rows) != 1 || !rows[0].Done1h {
		t.Fatalf("1h not filled: %+v", rows)
	}
	row := rows[0]
	if row.Ret1h == nil || math.Abs(*row.Ret1h-0.10) > 1e-9 {
		t.Fatalf("ret_1h = %v", row.Ret1h)
	}
	if row.MaxDD1h == nil || math.Abs(*row.MaxDD1h-(-0.10)) > 1e-9 {
		t.Fatalf("max_dd_1h = %v", row.MaxDD1h)
	}
}

func TestAccuracyGroupsByStrength(t *testing.T) {
	r := testRecorder(t)
	now := time.Now()
	ts := now.Add(-time.Hour).Unix()
	ret := 0.02
	dd := -0.01
	bench := 0.0
	neg := -0.02
	mk := func(symbol string, strength float64, r *float64) store.ExplosiveSignal {
		return store.ExplosiveSignal{TS: ts, Symbol: symbol, Strength: strength, Rank: 1,
			Ret4h: r, Bench4h: &bench, MaxDD4h: &dd, Done4h: true, Done1h: true, Ret1h: r}
	}
	rows := []store.ExplosiveSignal{
		mk("A", 85, &ret), mk("B", 85, &ret), mk("C", 85, &neg),
		mk("D", 45, &neg),
	}
	if err := r.store.SaveSignals(rows); err != nil {
		t.Fatal(err)
	}
	acc, err := r.Accuracy(30, now)
	if err != nil {
		t.Fatal(err)
	}
	high := acc.Bands[0]
	if high.Band != "≥80" || high.Samples != 3 {
		t.Fatalf("high band: %+v", high)
	}
	if math.Abs(high.Up4h-2.0/3) > 1e-9 || math.Abs(high.Sustained-2.0/3) > 1e-9 {
		t.Fatalf("high band stats: %+v", high)
	}
	low := acc.Bands[3]
	if low.Band != "<40" || low.Samples != 0 {
		t.Fatalf("empty band must stay empty: %+v", low)
	}
	if acc.Total != 4 {
		t.Fatalf("total = %d", acc.Total)
	}
	if len(acc.Top) != 2 || acc.Top[0].Samples != 4 {
		t.Fatalf("top/rest: %+v", acc.Top)
	}
}
