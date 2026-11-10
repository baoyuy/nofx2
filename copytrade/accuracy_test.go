package copytrade

import (
	"context"
	"testing"
	"time"

	"nofx/store"
)

// hourly makes bars from t0 (s) with one close per hour.
func hourly(t0 int64, closes ...float64) []Bar {
	out := make([]Bar, len(closes))
	for i, c := range closes {
		out[i] = Bar{Time: t0 + int64(i)*3600, Close: c}
	}
	return out
}

func TestCallsOfCountsARunOfAddsOnce(t *testing.T) {
	h := int64(3600 * 1000)
	events := []Event{
		{Time: 0, Symbol: "BTC", Side: "long", Action: "open", Price: 100},
		{Time: 2 * h, Symbol: "BTC", Side: "long", Action: "add", Price: 95},  // same call
		{Time: 20 * h, Symbol: "BTC", Side: "long", Action: "add", Price: 90}, // still the run
		{Time: 30 * h, Symbol: "BTC", Side: "long", Action: "reduce", Price: 99},
		{Time: 50 * h, Symbol: "BTC", Side: "long", Action: "add", Price: 98}, // 30h later: new call
		{Time: 51 * h, Symbol: "BTC", Side: "short", Action: "open", Price: 97},
	}
	got := callsOf(events)
	if len(got) != 3 || got[1].at != 50*h || got[2].side != "short" {
		t.Fatalf("calls = %+v", got)
	}
}

func TestScoreCalls(t *testing.T) {
	t0 := int64(1_700_000_000) // s, top of an hour is not needed
	bars := map[string][]Bar{"BTC": hourly(t0, make([]float64, 60)...)}
	for i := range bars["BTC"] {
		bars["BTC"][i].Close = 100 + float64(i) // rises an hour at a time
	}
	ms := t0 * 1000
	calls := []call{
		{symbol: "BTC", side: "long", at: ms, price: 100},               // 24h later 124: right
		{symbol: "BTC", side: "short", at: ms + 3600_000, price: 101},   // wrong
		{symbol: "BTC", side: "long", at: ms + 50*3600_000, price: 150}, // horizon not passed
		{symbol: "ETH", side: "long", at: ms, price: 1},                 // no bars
	}
	now := time.Unix(t0+55*3600, 0)
	a := scoreCalls(calls, bars, now)
	if a.Calls != 2 || a.Hits != 1 || a.Rate != 0.5 {
		t.Fatalf("accuracy = %+v", a)
	}
}

func TestAccuracyFactorAndDropRule(t *testing.T) {
	if f := accuracyFactor(Accuracy{Calls: 5, Hits: 5}); f != 1 {
		t.Fatalf("too few calls must not move the vote: %v", f)
	}
	good, bad := accuracyFactor(Accuracy{Calls: 40, Hits: 30}), accuracyFactor(Accuracy{Calls: 40, Hits: 10})
	if good <= 1.3 || bad >= 0.7 || good > 1.5 || bad < 0.5 {
		t.Fatalf("75%% → %v, 25%% → %v", good, bad)
	}
	if (Accuracy{Calls: 40, Hits: 14}).inaccurate() != true || (Accuracy{Calls: 20, Hits: 2}).inaccurate() {
		t.Fatal("drop needs 30+ calls clearly under 50%")
	}
	s := reviewed("a", 50)
	s.Accuracy = Accuracy{Calls: 40, Hits: 12}
	if got := DropReasons(s, time.Now()); len(got) != 1 || got[0] != RuleInaccurate {
		t.Fatalf("reasons = %v", got)
	}
}

// An accurate trader outvotes an equal but wrong one.
func TestAccuracyWeighsTheVote(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour).UnixMilli()
	long := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100,
		FirstEntry: 100, FirstValue: 100, OpenedAt: old, Source: "inferred"}
	short := long
	short.Side = "short"
	snaps := weightSnaps(long, short)
	snaps[0].Accuracy = Accuracy{Calls: 40, Hits: 30}
	snaps[1].Accuracy = Accuracy{Calls: 40, Hits: 10}
	if got := Symbols(snaps)[0].Confidence; got.Direction != "long" || got.LongShare < 0.65 {
		t.Fatalf("the accurate long must lead: %+v", got)
	}
}

// Openers count fresh calls only: not trapped, not averaging down.
func TestOpeners(t *testing.T) {
	now := time.Now()
	fresh := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100,
		OpenedAt: now.Add(-2 * time.Hour).UnixMilli(), LastAddAt: now.Add(-2 * time.Hour).UnixMilli(), Source: "inferred"}
	old := fresh
	old.OpenedAt, old.LastAddAt = now.Add(-5*24*time.Hour).UnixMilli(), now.Add(-5*24*time.Hour).UnixMilli()
	snaps := weightSnaps(fresh, fresh, fresh, old)
	snaps[1].Style.Kind = StyleMartingale
	snaps[2].Positions = assessHealth([]Position{fresh}, map[string]float64{"BTCUSDT": 80}, 1000, now) // trapped
	got := Symbols(snaps)[0].Long
	if got.Traders != 4 || got.Fresh != 3 || got.Openers != 1 {
		t.Fatalf("long side = %+v", got)
	}
}

// ScoreAccuracy fetches bars for the called coins and stores the result.
func TestScoreAccuracyStoresPerTrader(t *testing.T) {
	m, _ := newTestMonitor(t)
	t0 := time.Now().Add(-48 * time.Hour).Truncate(time.Hour)
	m.Bars = func(_ context.Context, symbol string, limit int) ([]Bar, error) {
		closes := make([]float64, 50)
		for i := range closes {
			closes[i] = 100 + float64(i)
		}
		return hourly(t0.Unix(), closes...), nil
	}
	m.mu.Lock()
	m.snaps["x"] = &Snapshot{Follow: store.CopyTradeFollow{PortfolioID: "x"},
		calls: []call{{symbol: "BTCUSDT", side: "long", at: t0.UnixMilli(), price: 100}}}
	m.mu.Unlock()
	m.ScoreAccuracy(context.Background(), time.Now())
	if a, _ := m.Snapshot("x"); a.Accuracy.Calls != 1 || a.Accuracy.Hits != 1 {
		t.Fatalf("accuracy = %+v", a.Accuracy)
	}
}
