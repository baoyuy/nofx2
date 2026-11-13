package explosive

import (
	"fmt"
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
	s.live.updatedAt, s.live.statsAt = now, now

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

func klineMsg(symbol string, start int64, closeP string, closed bool) []byte {
	return []byte(`{"e":"kline","s":"` + symbol + `","k":{"t":` +
		fmt.Sprint(start) + `,"o":"100","h":"101","l":"99","c":"` + closeP + `","v":"10","x":` +
		fmt.Sprint(closed) + `}}`)
}

func TestKlineStreamUpdatesTheFormingBar(t *testing.T) {
	l := newLiveTickers()
	l.applyKline(kline5m, klineMsg("SOLUSDT", 1000, "102", false))
	bars := l.Bars("SOLUSDT", kline5m)
	if len(bars) != 1 || bars[0].Close != 102 {
		t.Fatalf("forming bar: %+v", bars)
	}
	l.applyKline(kline5m, klineMsg("SOLUSDT", 1000, "103", false))
	if bars = l.Bars("SOLUSDT", kline5m); len(bars) != 1 || bars[0].Close != 103 {
		t.Fatalf("same bar must be replaced: %+v", bars)
	}
	l.applyKline(kline5m, klineMsg("SOLUSDT", 1300, "104", true))
	if bars = l.Bars("SOLUSDT", kline5m); len(bars) != 2 || bars[1].Close != 104 {
		t.Fatalf("closed bar must be appended: %+v", bars)
	}
	// The 1h series must not receive 5m events.
	if got := l.Bars("SOLUSDT", kline1h); len(got) != 0 {
		t.Fatalf("intervals leaked: %+v", got)
	}
}

func TestSeedIsCopied(t *testing.T) {
	l := newLiveTickers()
	seed := []Kline{{Time: 1, Close: 1}}
	l.Seed("XUSDT", kline5m, seed)
	seed[0].Close = 99
	if got := l.Bars("XUSDT", kline5m); got[0].Close != 1 {
		t.Fatalf("seed must be copied: %+v", got)
	}
}

func TestSetSymbolsDiffersTheWatchList(t *testing.T) {
	l := newLiveTickers()
	l.SetSymbols([]string{"AUSDT", "BUSDT"}, []string{"AUSDT"})
	if !l.subs[kline5m]["AUSDT"] || !l.subs[kline5m]["BUSDT"] || !l.subs[kline1h]["AUSDT"] {
		t.Fatalf("subscriptions: %+v", l.subs)
	}
	l.Seed("BUSDT", kline5m, flatBars(3, 100, 1))
	l.SetSymbols([]string{"AUSDT"}, []string{"AUSDT"})
	if l.subs[kline5m]["BUSDT"] {
		t.Fatalf("unwatched symbol kept: %+v", l.subs)
	}
	if got := l.Bars("BUSDT", kline5m); len(got) != 0 {
		t.Fatalf("unwatched bars kept: %+v", got)
	}
}

func TestFormingBarCarriesTheLivePrice(t *testing.T) {
	bars := []Kline{{Time: 0, Close: 100, Volume: 10}, {Time: 300000, Close: 101, Volume: 12}}
	// One minute into the next 5m bar: a new forming bar is appended.
	now := time.UnixMilli(660000) // one minute into the bar after the last closed one
	out := formingBar(bars, 90, 5*time.Minute, now)
	if len(out) != 3 {
		t.Fatalf("forming bar missing: %+v", out)
	}
	last := out[2]
	if last.Open != 101 || last.Close != 90 || last.High != 101 || last.Low != 90 {
		t.Fatalf("forming bar: %+v", last)
	}
	if r := volumeRatio(out); r <= 0 {
		t.Fatalf("a zero-volume forming bar must not zero the ratio: %v", r)
	}
}

func TestFormingBarUpdatesAnOpenBar(t *testing.T) {
	bars := []Kline{{Time: 0, Close: 100, Volume: 10}, {Time: 300000, Open: 100, High: 103, Low: 99, Close: 102, Volume: 5}}
	now := time.UnixMilli(300000 + 60000)
	out := formingBar(bars, 95, 5*time.Minute, now)
	if len(out) != 2 || out[1].Close != 95 || out[1].Low != 95 || out[1].High != 103 {
		t.Fatalf("open bar: %+v", out)
	}
}

func TestBuildPassesOpenInterestThrough(t *testing.T) {
	s := New()
	now := time.Now()
	s.snaps = map[string]symbolSnapshot{
		"SOLUSDT": {
			Bars5m: burst5m(flatBars(60, 100, 1000)), Bars1h: trendBars(150, 100, 0.3),
			OIChange1h: 0.02, OIKnown: true, Price: 159, Change24h: 5, Volume24h: 50e6,
		},
	}
	s.fullAt = now
	row := s.build(20).Rows[0]
	if !row.OIKnown || math.Abs(row.OIChange1h-0.02) > 1e-9 {
		t.Fatalf("open interest dropped from the scoring input: %+v", row)
	}
	if row.OpenInt <= 0.5 {
		t.Fatalf("open interest must affect the score: %+v", row)
	}
}
