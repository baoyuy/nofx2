package copytrade

import (
	"math"
	"testing"
	"time"

	"nofx/store"
)

func TestHoldFactor(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		p    Position
		want float64
	}{
		{"just opened", Position{OpenedAt: now.Add(-10 * time.Minute).UnixMilli()}, 0.6},
		{"two hours", Position{OpenedAt: now.Add(-2 * time.Hour).UnixMilli()}, 0.8},
		{"a day", Position{OpenedAt: now.Add(-24 * time.Hour).UnixMilli()}, 1},
		{"five days", Position{OpenedAt: now.Add(-5 * 24 * time.Hour).UnixMilli()}, 0.6},
		{"two weeks", Position{OpenedAt: now.Add(-14 * 24 * time.Hour).UnixMilli()}, 0.3},
		{"opened before the fills", Position{PreExisting: true}, 0.3},
		{"unknown", Position{}, 0.3},
	}
	for _, c := range cases {
		if got := holdFactor(c.p, now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func weightSnaps(positions ...Position) []Snapshot {
	now := time.Now()
	var out []Snapshot
	for i, p := range positions {
		out = append(out, Snapshot{
			Follow:    store.CopyTradeFollow{PortfolioID: string(rune('a' + i)), MarginBalance: 1000},
			Score:     TraderScore{Score: 60},
			Positions: assessHealth([]Position{p}, map[string]float64{"BTCUSDT": 100}, 1000, now),
		})
	}
	return out
}

// Adding to a position must not make its vote weigh more than the size it was
// opened with: otherwise a trader who keeps scaling in counts double.
func TestAddingDoesNotRaiseTheVote(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour).UnixMilli()
	// Opened with 100, then added 300: the current value is four times as big.
	long := Position{Symbol: "BTCUSDT", Side: "long", Qty: 4, EntryPrice: 100, EntryValue: 400,
		FirstEntry: 100, FirstValue: 100, OpenedAt: old, Source: "inferred"}
	// Opened with 100 and left alone.
	short := Position{Symbol: "BTCUSDT", Side: "short", Qty: 1, EntryPrice: 100, EntryValue: 100,
		FirstEntry: 100, FirstValue: 100, OpenedAt: old, Source: "inferred"}
	got := Symbols(weightSnaps(long, short))[0]
	if math.Abs(got.Confidence.LongShare-0.5) > 0.02 {
		t.Fatalf("both committed 100 at entry: %+v", got.Confidence)
	}
}

// A position opened minutes ago is a snapshot; one held for a day is a view.
func TestShortHoldsCountLess(t *testing.T) {
	long := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100,
		FirstEntry: 100, FirstValue: 100, OpenedAt: time.Now().Add(-10 * time.Minute).UnixMilli(), Source: "inferred"}
	short := Position{Symbol: "BTCUSDT", Side: "short", Qty: 1, EntryPrice: 100, EntryValue: 100,
		FirstEntry: 100, FirstValue: 100, OpenedAt: time.Now().Add(-48 * time.Hour).UnixMilli(), Source: "inferred"}
	if got := Symbols(weightSnaps(long, short))[0]; got.Confidence.Direction != "short" {
		t.Fatalf("a 10-minute old long must weigh less than a two-day short: %+v", got.Confidence)
	}
	// Held as long as the short, it counts the same again.
	long.OpenedAt = short.OpenedAt
	if got := Symbols(weightSnaps(long, short))[0]; math.Abs(got.Confidence.LongShare-0.5) > 0.02 {
		t.Fatalf("equal holds, equal weight: %+v", got.Confidence)
	}
}

// A week-old position is mostly a loss being sat out: a fresh short outvotes
// an equal long held for two weeks.
func TestOldHoldsFade(t *testing.T) {
	long := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100,
		FirstEntry: 100, FirstValue: 100, OpenedAt: time.Now().Add(-14 * 24 * time.Hour).UnixMilli(), Source: "inferred"}
	short := Position{Symbol: "BTCUSDT", Side: "short", Qty: 1, EntryPrice: 100, EntryValue: 100,
		FirstEntry: 100, FirstValue: 100, OpenedAt: time.Now().Add(-12 * time.Hour).UnixMilli(), Source: "inferred"}
	if got := Symbols(weightSnaps(long, short))[0]; got.Confidence.Direction != "short" || got.Confidence.LongShare > 0.3 {
		t.Fatalf("a two-week hold must weigh well under a fresh one: %+v", got.Confidence)
	}
}

// A new auto-follow pick votes half until its probation ends.
func TestProbationHalvesTheVote(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour).UnixMilli()
	long := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100,
		FirstEntry: 100, FirstValue: 100, OpenedAt: old, Source: "inferred"}
	short := long
	short.Side = "short"
	snaps := weightSnaps(long, short)
	snaps[0].Follow.Source, snaps[0].Follow.PickedAt = "auto", time.Now().Add(-2*24*time.Hour).UnixMilli()
	got := Symbols(snaps)[0].Confidence
	if got.Direction != "short" || math.Abs(got.LongShare-1.0/3) > 0.02 {
		t.Fatalf("probation long must count half: %+v", got)
	}
}
