package copytrade

import (
	"math"
	"testing"
)

func fill(t int64, side, ps string, qty, price, pnl float64) Fill {
	return Fill{Time: t, Symbol: "BTCUSDT", Side: side, PositionSide: ps, Qty: qty, Price: price, RealizedPnL: pnl}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestReplayHedgeOpenAddReduceClose(t *testing.T) {
	positions, events := Replay([]Fill{
		fill(1000, "BUY", "LONG", 1, 100, 0),
		fill(60000, "BUY", "LONG", 0.5, 112, 0), // one order reported as two fills
		fill(60100, "BUY", "LONG", 0.5, 112, 0),
		fill(90000, "SELL", "LONG", 1, 120, 16),
	})
	if len(positions) != 1 {
		t.Fatalf("positions = %+v", positions)
	}
	p := positions[0]
	// Entry 106 (1 @100 + 1 @112); half closed keeps the average.
	if p.Side != "long" || !approx(p.Qty, 1) || !approx(p.EntryPrice, 106) || p.OpenedAt != 1000 || p.Source != "inferred" {
		t.Fatalf("position = %+v", p)
	}
	want := []string{"open", "add", "reduce"}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for i, a := range want {
		if events[i].Action != a {
			t.Fatalf("event %d = %s, want %s (%+v)", i, events[i].Action, a, events)
		}
	}
	if !approx(events[1].Qty, 1) || !approx(events[1].Price, 112) {
		t.Fatalf("split fills must merge into one add: %+v", events[1])
	}

	_, events = Replay(append([]Fill{fill(95000, "SELL", "LONG", 2, 125, 38)}, fill(1000, "BUY", "LONG", 2, 106, 0)))
	if events[len(events)-1].Action != "close" || events[len(events)-1].PositionQty != 0 {
		t.Fatalf("selling all must close: %+v", events)
	}
}

func TestReplayOneWayFlipAndUntrackedClose(t *testing.T) {
	// Long 1 @100, then sell 3 @110: closing 1 realizes +10, the other 2 open a short.
	positions, events := Replay([]Fill{
		fill(1000, "BUY", "BOTH", 1, 100, 0),
		fill(5000, "SELL", "BOTH", 3, 110, 10),
	})
	if len(positions) != 1 || positions[0].Side != "short" || !approx(positions[0].Qty, 2) || !approx(positions[0].EntryPrice, 110) {
		t.Fatalf("flip must leave a 2 short @110: %+v", positions)
	}
	if len(events) != 3 || events[1].Action != "close" || events[2].Action != "open" || events[2].Side != "short" {
		t.Fatalf("events = %+v", events)
	}

	// A sell with realized PnL and no tracked position closes something opened
	// before monitoring: reported, but no short is invented.
	positions, events = Replay([]Fill{fill(1000, "SELL", "BOTH", 2, 120, 40)})
	if len(positions) != 0 || len(events) != 1 || !events[0].Untracked || events[0].Side != "long" {
		t.Fatalf("untracked close: %+v %+v", positions, events)
	}

	// Selling more than tracked with PnL that does not fit a flip means the
	// long was larger than what monitoring saw: close, do not open a short.
	positions, _ = Replay([]Fill{
		fill(1000, "BUY", "BOTH", 1, 100, 0),
		fill(5000, "SELL", "BOTH", 3, 110, 30),
	})
	if len(positions) != 0 {
		t.Fatalf("over-close must not invent a short: %+v", positions)
	}
}

func TestReplayHedgeShortAndUnknownReduce(t *testing.T) {
	positions, _ := Replay([]Fill{
		fill(1000, "SELL", "SHORT", 2, 100, 0),
		fill(2000, "BUY", "SHORT", 3, 90, 25), // closes more than tracked
		fill(3000, "BUY", "SHORT", 1, 95, 5),  // reduces a short opened before monitoring
	})
	if len(positions) != 0 {
		t.Fatalf("hedge bucket must not flip: %+v", positions)
	}
}

func TestLivePositionsUseRebuiltOpenTime(t *testing.T) {
	live := []LivePosition{{Symbol: "ETHUSDT", PositionSide: "BOTH", PositionAmount: -4, EntryPrice: 2292.5, MarkPrice: 2253, Leverage: 20}}
	out := LivePositions(live, []Position{{Symbol: "ETHUSDT", Side: "short", OpenedAt: 777}})
	p := out[0]
	if p.Side != "short" || p.Qty != 4 || !approx(p.EntryValue, 9170) || !approx(p.Margin, 458.5) || p.OpenedAt != 777 || p.Source != "public" {
		t.Fatalf("live = %+v", p)
	}
}

// A position opened before the stored fills begins: its first fill we see is
// an add, not a new open, and its holding time is unknown.
func TestReplayPreOpenedPosition(t *testing.T) {
	fills := []Fill{
		fill(1000, "BUY", "BOTH", 2, 90, 0), // opened before monitoring
		fill(5000, "BUY", "BOTH", 1, 80, 0), // averaged down, seen
		fill(6000, "BUY", "BOTH", 1, 70, 0), // averaged down again, seen
	}
	pre := map[string]bool{PreKey("BTCUSDT", "long"): true}
	positions, events := ReplayKnown(fills, pre)
	if len(positions) != 1 {
		t.Fatalf("positions = %+v", positions)
	}
	p := positions[0]
	if !p.PreExisting || p.OpenedAt != 0 {
		t.Fatalf("position must be marked pre-existing with no open time: %+v", p)
	}
	// We saw 4 contracts enter at 90, 80 and 70; the size below them is unseen.
	if !approx(p.EntryPrice, 82.5) || !approx(p.FirstEntry, 90) || p.Qty != 4 {
		t.Fatalf("averages = %+v", p)
	}
	for _, e := range events {
		if e.Action == "open" {
			t.Fatalf("nothing here opened the position: %+v", events)
		}
	}
	if events[0].Action != "add" {
		t.Fatalf("first fill adds: %+v", events[0])
	}
	// Without the hint the same fills look like a new position.
	plain, _ := Replay(fills)
	if plain[0].OpenedAt != 1000 || plain[0].PreExisting {
		t.Fatalf("without the hint it is an open: %+v", plain[0])
	}
}

// Binance showing more than the fills explain is what marks a position as
// opened before the stored fills.
func TestPreOpenedBooksFromLivePositions(t *testing.T) {
	live := []LivePosition{
		{Symbol: "BTCUSDT", PositionSide: "BOTH", PositionAmount: 3, EntryPrice: 80},    // exactly what the fills say
		{Symbol: "ETHUSDT", PositionSide: "LONG", PositionAmount: 5, EntryPrice: 2000},  // 5 held, 2 seen
		{Symbol: "SOLUSDT", PositionSide: "SHORT", PositionAmount: -1, EntryPrice: 100}, // exactly seen
	}
	inferred := []Position{
		{Symbol: "BTCUSDT", Side: "long", Qty: 3},
		{Symbol: "ETHUSDT", Side: "long", Qty: 2},
		{Symbol: "SOLUSDT", Side: "short", Qty: 1},
	}
	pre := preOpenedBooks(live, inferred)
	if len(pre) != 1 || !pre[PreKey("ETHUSDT", "long")] {
		t.Fatalf("pre-opened = %+v", pre)
	}
}

func TestSymbolsAveragesEntryBySize(t *testing.T) {
	snaps := []Snapshot{
		{Positions: []Position{{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100}}},
		{Positions: []Position{{Symbol: "BTCUSDT", Side: "long", Qty: 3, EntryPrice: 120, EntryValue: 360}}},
		{Positions: []Position{{Symbol: "BTCUSDT", Side: "short", Qty: 1, EntryPrice: 130, EntryValue: 130}}},
	}
	s := Symbols(snaps)[0]
	if s.Long.Traders != 2 || !approx(s.Long.Value, 460) || !approx(s.Long.AvgEntry, 115) || s.Short.Traders != 1 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestFeedPages(t *testing.T) {
	var events []Event
	for i := 0; i < 25; i++ {
		events = append(events, Event{Time: int64(100 - i), Symbol: "BTCUSDT", Side: "long", Action: "open", Value: 10})
	}
	snaps := []Snapshot{{Events: events}}
	page, total := FeedPage(snaps, FeedFilter{Limit: 10, Offset: 20})
	if total != 25 || len(page) != 5 || page[0].Time != 80 {
		t.Fatalf("page 3 = %d items from %v, total %d", len(page), page[0].Time, total)
	}
	if page, total := FeedPage(snaps, FeedFilter{Limit: 10, Offset: 40}); total != 25 || len(page) != 0 {
		t.Fatalf("past the end = %d items, total %d", len(page), total)
	}
}

// Every round trip records its own opening size, so adding to a reopened
// position does not make the vote weigh more (FirstValue fell to 0 before).
func TestReplayOpeningSizePerRoundTrip(t *testing.T) {
	positions, _ := Replay([]Fill{
		fill(1000, "BUY", "LONG", 1, 100, 0),
		fill(2000, "SELL", "LONG", 1, 110, 10), // first trip closed
		fill(10000, "BUY", "LONG", 1, 100, 0),  // reopened: one order,
		fill(11000, "BUY", "LONG", 2, 100, 0),  // two fills
		fill(60000, "BUY", "LONG", 9, 90, 0),   // averaged down later
	})
	if len(positions) != 1 {
		t.Fatalf("positions = %+v", positions)
	}
	p := positions[0]
	if !approx(p.FirstEntry, 100) || !approx(p.FirstValue, 300) || !approx(p.EntryValue, 1110) {
		t.Fatalf("opening size of the second trip: %+v", p)
	}
	if got := openedValue(p); !approx(got, 300) {
		t.Fatalf("vote size must be the opening size, got %v", got)
	}
}
