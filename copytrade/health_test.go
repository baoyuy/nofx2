package copytrade

import (
	"testing"
	"time"

	"nofx/store"
)

func TestAssessHealthFlagsTrappedFreshAndLiquidated(t *testing.T) {
	now := time.Now()
	old := now.Add(-5 * 24 * time.Hour).UnixMilli()
	recent := now.Add(-time.Hour).UnixMilli()
	marks := map[string]float64{"BTCUSDT": 90, "ETHUSDT": 110, "SOLUSDT": 50}
	got := assessHealth([]Position{
		// Long 1 at 100, mark 90: -10% price move, no leverage known.
		{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100, OpenedAt: old, Source: "inferred"},
		// Long at 100, mark 110, added an hour ago.
		{Symbol: "ETHUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100, OpenedAt: old, LastAddAt: recent, Source: "inferred"},
		// Public: Binance's PnL and margin are kept, -5% of margin.
		{Symbol: "SOLUSDT", Side: "short", Qty: 1, EntryPrice: 48, EntryValue: 48, Margin: 40, MarkPrice: 50,
			UnrealizedPnL: -2, Source: "public"},
		// No mark price.
		{Symbol: "XYZUSDT", Side: "short", Qty: 1, EntryPrice: 1, EntryValue: 1, Source: "inferred"},
	}, marks, 1000, now)
	btc, eth, sol, xyz := got[0], got[1], got[2], got[3]
	if !btc.Priced || btc.UnrealizedPnL != -10 || btc.ReturnPct != -10 || btc.PctOfMargin || !btc.Trapped || btc.Fresh {
		t.Fatalf("btc: %+v", btc)
	}
	if eth.Trapped || !eth.Fresh || eth.UnrealizedPnL != 10 {
		t.Fatalf("eth: %+v", eth)
	}
	if !sol.PctOfMargin || sol.ReturnPct != -5 || sol.Trapped || sol.UnrealizedPnL != -2 {
		t.Fatalf("sol: %+v", sol)
	}
	if xyz.Priced || xyz.Trapped {
		t.Fatalf("xyz: %+v", xyz)
	}
	if btc.Liquidated {
		t.Fatal("a 10 USDT loss on a 1000 balance is not a liquidation")
	}
	// The same loss on a 10 USDT balance wiped the account.
	gone := assessHealth([]Position{
		{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100, Source: "inferred"},
		{Symbol: "ETHUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100, Source: "inferred"},
	}, marks, 10, now)
	if !gone[0].Liquidated || gone[0].Trapped || gone[1].Liquidated {
		t.Fatalf("only the losing position is liquidated: %+v", gone)
	}
}

func TestSymbolsDiscountTrappedAndDropLiquidated(t *testing.T) {
	now := time.Now()
	snap := func(id string, balance float64, p Position) Snapshot {
		return Snapshot{Follow: store.CopyTradeFollow{PortfolioID: id, MarginBalance: balance},
			Score: TraderScore{Score: 60}, Positions: assessHealth([]Position{p}, map[string]float64{"BTCUSDT": 80}, balance, now)}
	}
	long := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100, Source: "inferred"}
	short := Position{Symbol: "BTCUSDT", Side: "short", Qty: 1, EntryPrice: 81, EntryValue: 81, Source: "inferred",
		OpenedAt: now.Add(-time.Hour).UnixMilli()}
	// Three longs down 20% (trapped), one fresh short: votes favour the short.
	sums := Symbols([]Snapshot{snap("a", 1000, long), snap("b", 1000, long), snap("c", 1000, long), snap("d", 1000, short),
		// Lost more than its balance: likely liquidated, not counted.
		snap("e", 5, long)})
	s := sums[0]
	if s.Long.Traders != 3 || s.Long.Trapped != 3 || s.Short.Traders != 1 || s.Short.Fresh != 1 || s.Liquidated != 1 {
		t.Fatalf("counts: %+v", s)
	}
	if s.Confidence.Direction != "short" || s.Confidence.Traders != 4 {
		t.Fatalf("three traders sitting on 200%%-of-margin losses must count less than one fresh short: %+v", s.Confidence)
	}
	healthy := Symbols([]Snapshot{{Follow: store.CopyTradeFollow{PortfolioID: "a", MarginBalance: 1000},
		Score: TraderScore{Score: 60}, Positions: []Position{long}}})
	if healthy[0].Confidence.Direction != "long" {
		t.Fatalf("unpriced long still votes long: %+v", healthy[0].Confidence)
	}
}

func TestConfidenceDropsWhenTheLeadingSideIsTrapped(t *testing.T) {
	now := time.Now()
	coin := func(mark float64) SymbolSummary {
		var snaps []Snapshot
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			p := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100, Source: "inferred"}
			snaps = append(snaps, Snapshot{Follow: store.CopyTradeFollow{PortfolioID: id, MarginBalance: 1000},
				Score: TraderScore{Score: 60}, Positions: assessHealth([]Position{p}, map[string]float64{"BTCUSDT": mark}, 1000, now)})
		}
		return Symbols(snaps)[0]
	}
	healthy, noisy, trapped := coin(101), coin(97), coin(93)
	if healthy.Confidence.Health != 1 || healthy.Long.Trapped != 0 {
		t.Fatalf("healthy: %+v", healthy)
	}
	// 3% against at an assumed 10x is 30% of margin: weighed down, not trapped.
	if noisy.Long.Trapped != 0 || noisy.Confidence.Score >= healthy.Confidence.Score {
		t.Fatalf("noisy: %+v vs %+v", noisy.Confidence, healthy.Confidence)
	}
	// Everyone long and 7% under water: same agreement, much lower score.
	if trapped.Long.Trapped != 5 || trapped.Confidence.Agreement != 1 || trapped.Confidence.Score > healthy.Confidence.Score*0.5 {
		t.Fatalf("trapped: %+v vs %+v", trapped.Confidence, healthy.Confidence)
	}
}

func TestHighFrequencyTradersCountLessAndCanBeHidden(t *testing.T) {
	events := []Event{{Time: 300}, {Time: 200}, {Time: 100}}
	if n := ordersSince(events, 200); n != 2 {
		t.Fatalf("orders since 200: %d", n)
	}
	long := []Position{{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 100, EntryValue: 100, Source: "inferred"}}
	short := []Position{{Symbol: "BTCUSDT", Side: "short", Qty: 1, EntryPrice: 100, EntryValue: 100, Source: "inferred"}}
	snap := func(id string, hf bool, p []Position) Snapshot {
		return Snapshot{Follow: store.CopyTradeFollow{PortfolioID: id, MarginBalance: 1000}, Score: TraderScore{Score: 60},
			Positions: p, HighFreq: hf, Events: []Event{{Time: 1, Symbol: "BTCUSDT", Side: p[0].Side, Action: "open", Value: 100}}}
	}
	// Two bots long, one manual trader short: the bots' 2 × 0.4 loses to 1.
	snaps := []Snapshot{snap("bot1", true, long), snap("bot2", true, long), snap("human", false, short)}
	s := Symbols(snaps)[0]
	if s.HighFreq != 2 || s.Long.Traders != 2 || s.Confidence.Direction != "short" {
		t.Fatalf("high-frequency votes are weighted down: %+v", s)
	}
	if items, total := FeedPage(snaps, FeedFilter{HideHighFreq: true}); total != 1 || items[0].PortfolioID != "human" {
		t.Fatalf("hidden from the feed: %+v", items)
	}
	if _, total := FeedPage(snaps, FeedFilter{HideHighFreq: true, PortfolioID: "bot1"}); total != 1 {
		t.Fatal("picking a high-frequency trader shows them anyway")
	}
}

// Historical entry deviation is reference information, not current loss.
func TestHistoricalEntryDoesNotSetTrappedOrPenalizeConfidence(t *testing.T) {
	now := time.Now()
	p := Position{Symbol: "BTCUSDT", Side: "long", Qty: 1, EntryPrice: 96, EntryValue: 96, FirstEntry: 100,
		Leverage: 20, Margin: 4.8, MarkPrice: 94, UnrealizedPnL: -2, Source: "public",
		LastAddAt: now.Add(-time.Hour).UnixMilli()}
	got := assessHealth([]Position{p}, nil, 1000, now)[0]
	if got.Trapped || !got.HistoricalEntryWarning || got.ReturnPct != -41.66666666666667 ||
		got.FirstEntryReturnPct != -120 || !got.FirstEntryPctOfMargin || got.FirstEntryThresholdPct != 50 {
		t.Fatalf("current loss and historical reference must stay separate: %+v", got)
	}
	p.FirstEntry = p.EntryPrice
	plain := assessHealth([]Position{p}, nil, 1000, now)[0]
	if plain.Trapped || plain.HistoricalEntryWarning || healthFactor(got) != healthFactor(plain) {
		t.Fatalf("historical entry must not change confidence weight: %+v vs %+v", got, plain)
	}
	sums := Symbols([]Snapshot{{Follow: store.CopyTradeFollow{PortfolioID: "a"},
		Score: TraderScore{Score: 60}, Positions: []Position{got}}})
	if sums[0].Long.Trapped != 0 {
		t.Fatal("historical warning incorrectly counted as trapped")
	}
}

func TestTrappedUsesDisplayedCurrentReturnBasis(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name                string
		p                   Position
		marks               map[string]float64
		trapped, historical bool
		threshold           float64
	}{
		{"short minus 9.4 with worse history", Position{Side: "short", EntryPrice: 100, FirstEntry: 95, MarkPrice: 100.47, Margin: 5, Leverage: 20, UnrealizedPnL: -0.47, Source: "public"}, nil, false, true, 50},
		{"short minus 30", Position{Side: "short", EntryPrice: 100, FirstEntry: 100, MarkPrice: 103, Margin: 10, Leverage: 10, UnrealizedPnL: -3, Source: "public"}, nil, false, false, 50},
		{"margin exactly 50", Position{Side: "long", EntryPrice: 100, MarkPrice: 95, Margin: 10, UnrealizedPnL: -5, Source: "public"}, nil, true, false, 50},
		{"margin below 50", Position{Side: "long", EntryPrice: 100, MarkPrice: 95, Margin: 10, UnrealizedPnL: -4.9, Source: "public"}, nil, false, false, 50},
		{"price exactly 5", Position{Symbol: "BTCUSDT", Side: "long", EntryPrice: 100, Qty: 1, Source: "inferred"}, map[string]float64{"BTCUSDT": 95}, true, false, 5},
		{"price below 5 with worse history", Position{Symbol: "BTCUSDT", Side: "long", EntryPrice: 96, FirstEntry: 100, Qty: 1, Source: "inferred"}, map[string]float64{"BTCUSDT": 94}, false, true, 5},
		{"no historical entry difference", Position{Side: "short", EntryPrice: 100, FirstEntry: 100, MarkPrice: 106, Margin: 10, Leverage: 10, UnrealizedPnL: -6, Source: "public"}, nil, true, false, 50},
		{"better historical entry", Position{Side: "short", EntryPrice: 100, FirstEntry: 110, MarkPrice: 106, Margin: 10, Leverage: 10, UnrealizedPnL: -6, Source: "public"}, nil, true, false, 50},
		{"no mark", Position{Side: "long", EntryPrice: 100, FirstEntry: 110, Source: "public"}, nil, false, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := assessHealth([]Position{tt.p}, tt.marks, 1000, now)[0]
			if p.Trapped != tt.trapped || p.HistoricalEntryWarning != tt.historical || p.TrappedThresholdPct != tt.threshold {
				t.Fatalf("inconsistent current loss classification: %+v", p)
			}
		})
	}
}

// A trader who keeps adding to losses looks heavily invested; that size is a
// symptom, so their votes count 0.3 and the other side can win.
func TestMartingaleVotesCountLess(t *testing.T) {
	now := time.Now()
	// Three traders long, one short, each with the same size and score.
	snaps := func(kind string) []Snapshot {
		pos := func(side string, id string, style string) Snapshot {
			p := Position{Symbol: "BTCUSDT", Side: side, Qty: 1, EntryPrice: 100, EntryValue: 100, Source: "inferred",
				OpenedAt: now.Add(-48 * time.Hour).UnixMilli()}
			return Snapshot{Follow: store.CopyTradeFollow{PortfolioID: id, MarginBalance: 1000},
				Score: TraderScore{Score: 60}, Style: Style{Kind: style},
				Positions: assessHealth([]Position{p}, map[string]float64{"BTCUSDT": 101}, 1000, now)}
		}
		return []Snapshot{pos("long", "a", kind), pos("long", "b", kind), pos("long", "c", kind),
			pos("short", "d", StyleSwing)}
	}
	if got := Symbols(snaps(StyleSwing))[0]; got.Confidence.Direction != "long" {
		t.Fatalf("three against one leans long: %+v", got.Confidence)
	}
	got := Symbols(snaps(StyleMartingale))[0]
	if got.Confidence.Direction != "short" || got.Confidence.Score >= 60 {
		t.Fatalf("martingale votes at 0.3 let the lone short win: %+v", got.Confidence)
	}
}
