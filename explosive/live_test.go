package explosive

import (
	"math"
	"testing"
	"time"
)

func TestLiveTickerParsesTheStream(t *testing.T) {
	l := newLiveTickers()
	l.apply([]byte(`[{"e":"24hrMiniTicker","s":"BTCUSDT","c":"100","o":"90","q":"12345.6"}]`))
	tk, ok := l.Get("BTCUSDT")
	if !ok {
		t.Fatal("ticker missing")
	}
	if tk.Price != 100 || tk.QuoteVolume != 12345.6 {
		t.Fatalf("parsed: %+v", tk)
	}
	if math.Abs(tk.ChangePct-11.1111) > 0.01 {
		t.Fatalf("change pct: %v", tk.ChangePct)
	}
	if !l.Fresh(time.Minute) {
		t.Fatal("a just-applied message must be fresh")
	}
	if !l.Fresh(liveMaxAge) {
		t.Fatal("freshness window")
	}
}

func TestLiveTickerIgnoresGarbage(t *testing.T) {
	l := newLiveTickers()
	l.apply([]byte(`not json`))
	l.apply([]byte(`[{"s":"BAD","c":"","o":"1","q":"1"}]`))
	if _, ok := l.Get("BAD"); ok {
		t.Fatal("invalid rows must be skipped")
	}
	if l.Fresh(time.Minute) {
		t.Fatal("no message must not look fresh")
	}
}

func TestBuildRecomputesWithLivePrices(t *testing.T) {
	s := New()
	now := time.Now()
	s.snaps = map[string]symbolSnapshot{
		"SOLUSDT": {
			Bars5m: burst5m(flatBars(60, 100, 1000)), Bars1h: trendBars(150, 100, 0.3),
			OIChange1h: 0.03, OIKnown: true, Price: 159, Change24h: 5, Volume24h: 50e6,
		},
	}
	s.fullAt = now
	s.live.m = map[string]liveTicker{"SOLUSDT": {Price: 160, ChangePct: 25, QuoteVolume: 60e6, At: now}}
	s.live.updatedAt = now

	board := s.build(20)
	if len(board.Rows) != 1 {
		t.Fatalf("rows: %d", len(board.Rows))
	}
	row := board.Rows[0]
	if row.Price != 160 || row.Change24h != 25 || row.Volume24h != 60e6 {
		t.Fatalf("live fields not applied: %+v", row)
	}
	if board.Stale || board.Refreshing || board.AgeSeconds != 0 {
		t.Fatalf("fresh board flags: %+v", board)
	}
}

func TestBuildFallsBackToSnapshotWhenFeedIsOld(t *testing.T) {
	s := New()
	now := time.Now()
	s.snaps = map[string]symbolSnapshot{
		"SOLUSDT": {
			Bars5m: burst5m(flatBars(60, 100, 1000)), Bars1h: trendBars(150, 100, 0.3),
			OIChange1h: 0.03, OIKnown: true, Price: 159, Change24h: 5, Volume24h: 50e6,
		},
	}
	s.fullAt = now
	s.live.m = map[string]liveTicker{"SOLUSDT": {Price: 160, ChangePct: 25, QuoteVolume: 60e6, At: now.Add(-time.Minute)}}
	s.live.updatedAt = now.Add(-time.Minute) // older than liveMaxAge

	row := s.build(20).Rows[0]
	if row.Price != 159 || row.Change24h != 5 {
		t.Fatalf("stale feed must not be used: %+v", row)
	}
}

func TestBuildReportsStaleness(t *testing.T) {
	s := New()
	s.snaps = map[string]symbolSnapshot{}
	s.fullAt = time.Now().Add(-2 * boardTTL)
	board := s.build(20)
	if !board.Stale {
		t.Fatalf("old snapshot must be stale: %+v", board)
	}
	if board.AgeSeconds < int(boardTTL.Seconds()) {
		t.Fatalf("age: %d", board.AgeSeconds)
	}
}
