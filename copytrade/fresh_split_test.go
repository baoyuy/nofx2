package copytrade

import (
	"testing"
	"time"

	"nofx/store"
)

// positionWith builds a one-position snapshot opened and last added at the
// given ages, priced at mark.
func positionWith(id string, openedAgo, addedAgo time.Duration, mark float64, now time.Time) Snapshot {
	s := Snapshot{
		Follow: store.CopyTradeFollow{PortfolioID: id, Nickname: id, MarginBalance: 10000},
		Score:  TraderScore{Score: 60}, HistoryComplete: true,
		Positions: []Position{{Symbol: "SOLUSDT", Side: "long", Qty: 10, EntryPrice: 100, EntryValue: 1000,
			OpenedAt: now.Add(-openedAgo).UnixMilli(), LastAddAt: now.Add(-addedAgo).UnixMilli()}},
	}
	s.Positions = assessHealth(s.Positions, map[string]float64{"SOLUSDT": mark}, 10000, now)
	return s
}

func TestPositionsOpenedDaysAgoAreNotNew(t *testing.T) {
	now := time.Now()
	c := SymbolsAt([]Snapshot{positionWith("opened-3d", 72*time.Hour, 72*time.Hour, 100, now)}, now)[0]
	if c.Long.Fresh != 0 || c.Long.Openers != 0 || c.Long.Added != 0 {
		t.Fatalf("a three-day-old position is not new: %+v", c.Long)
	}
	if c.Long.HoldHours < 71 {
		t.Fatalf("hold hours: %v", c.Long.HoldHours)
	}
}

func TestAddsAreCountedSeparatelyFromOpens(t *testing.T) {
	now := time.Now()
	c := SymbolsAt([]Snapshot{
		// Opened three days ago but added to an hour ago: an add, not an open.
		positionWith("opened-3d-added-1h", 72*time.Hour, time.Hour, 100, now),
		positionWith("opened-23h", 23*time.Hour, 23*time.Hour, 100, now),
	}, now)[0]
	if c.Long.Fresh != 1 || c.Long.Openers != 1 {
		t.Fatalf("only the 23h-old position is new: %+v", c.Long)
	}
	if c.Long.Added != 1 {
		t.Fatalf("the add is counted on its own: %+v", c.Long)
	}
}

func TestAddingToALoserIsNotACall(t *testing.T) {
	now := time.Now()
	// Under water by 2%: a loss, but not deep enough to count as trapped.
	loser := SymbolsAt([]Snapshot{positionWith("old-added-loser", 72*time.Hour, time.Hour, 98, now)}, now)[0]
	if loser.Shadow.LongCalls != 0 {
		t.Fatalf("adding to a losing position must not be a call: %+v", loser.Shadow)
	}

	winner := SymbolsAt([]Snapshot{positionWith("old-added-winner", 72*time.Hour, time.Hour, 102, now)}, now)[0]
	if winner.Shadow.LongCalls != 1 || winner.Rank.Basis != "fresh" {
		t.Fatalf("adding to a winning position is a weaker fresh call: %+v / %+v", winner.Shadow, winner.Rank)
	}
}

func TestAddAloneRanksBelowOpenAlone(t *testing.T) {
	now := time.Now()
	// Same coin, same single voice: opening it is stronger evidence than
	// adding to a position opened days ago.
	open := SymbolsAt([]Snapshot{positionWith("just-opened", 2*time.Hour, 2*time.Hour, 100, now)}, now)[0]
	add := SymbolsAt([]Snapshot{positionWith("old-added", 72*time.Hour, 2*time.Hour, 100, now)}, now)[0]
	if open.Shadow.Score <= add.Shadow.Score {
		t.Fatalf("an open must outscore an add: %.1f vs %.1f", open.Shadow.Score, add.Shadow.Score)
	}
}
