package copytrade

import (
	"math"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"nofx/store"
)

func TestPlanBoardDropsBuffer(t *testing.T) {
	rankOf := map[string]int{
		"top":    1,
		"edge":   boardDropRank,     // exactly 150: keep
		"past":   boardDropRank + 1, // 151: drop
		"far":    163,
	}
	current := []string{"top", "edge", "past", "far", "gone0", "gone2"}
	prevMissed := map[string]int{"gone0": 0, "gone2": 2}
	drops, missed := planBoardDrops(current, rankOf, prevMissed)
	if drops["top"] != "" || drops["edge"] != "" {
		t.Fatalf("traders on the board in range must be kept: %v", drops)
	}
	if drops["past"] == "" || drops["far"] == "" {
		t.Fatalf("traders past %d must be dropped: %v", boardDropRank, drops)
	}
	if _, ok := drops["gone0"]; ok {
		t.Fatalf("first miss must not drop: %v", drops)
	}
	if missed["gone0"] != 1 {
		t.Fatalf("first miss count = %d, want 1", missed["gone0"])
	}
	if _, ok := drops["gone2"]; !ok {
		t.Fatalf("third miss (2+1) must drop: %v", drops)
	}
}

func TestPaperReportMirrorsBacktest(t *testing.T) {
	// One long position: open 1 @100, add 1 @100, close 2 @110. Peak 2×100=200,
	// cash 20, traded 420, fee 0.0005×420=0.21 → ret = (20−0.21)/200 = 9.895%.
	base := int64(1_700_000_000_000)
	fills := []store.CopyTradePaperFill{
		{RosterID: "r", PortfolioID: "t1", Symbol: "BTCUSDT", Side: "long", Action: "open", TradeTime: base, Qty: 1, TraderPrice: 100, SeenPrice: 100, BenchPrice: 100, DelayMs: 60_000},
		{RosterID: "r", PortfolioID: "t1", Symbol: "BTCUSDT", Side: "long", Action: "add", TradeTime: base + 1000, Qty: 1, TraderPrice: 100, SeenPrice: 100, BenchPrice: 100, DelayMs: 60_000},
		{RosterID: "r", PortfolioID: "t1", Symbol: "BTCUSDT", Side: "long", Action: "close", TradeTime: base + 2000, Qty: 2, TraderPrice: 110, SeenPrice: 110, BenchPrice: 100, DelayMs: 60_000},
	}
	rows := PaperReport(fills)
	var all *PaperRow
	for i := range rows {
		if rows[i].Table == "all" {
			all = &rows[i]
		}
	}
	if all == nil {
		t.Fatalf("no all row: %+v", rows)
	}
	if all.Trips != 1 || all.Traders != 1 || all.Win != 1 {
		t.Fatalf("all row = %+v", all)
	}
	want := (20.0 - 0.0005*420) / 200
	if math.Abs(all.Avg-want) > 1e-9 {
		t.Fatalf("avg = %v, want %v", all.Avg, want)
	}
	// Seen price == trader price here, so the price-basis rows agree.
	var seen, own *PaperRow
	for i := range rows {
		if rows[i].Table == "price" {
			switch rows[i].Label {
			case "seen prices (copy)":
				seen = &rows[i]
			case "trader own prices (not reachable)":
				own = &rows[i]
			}
		}
	}
	if seen == nil || own == nil || math.Abs(seen.Avg-own.Avg) > 1e-9 {
		t.Fatalf("price rows differ: %+v %+v", seen, own)
	}
	if all.AvgDelayMin != 1 {
		t.Fatalf("avg delay = %v min, want 1", all.AvgDelayMin)
	}
}

func TestPaperReportShortWinsWhenPriceFalls(t *testing.T) {
	base := int64(1_700_000_000_000)
	fills := []store.CopyTradePaperFill{
		{RosterID: "r", PortfolioID: "t2", Symbol: "ETHUSDT", Side: "short", Action: "open", TradeTime: base, Qty: 1, TraderPrice: 100, SeenPrice: 100, BenchPrice: 100},
		{RosterID: "r", PortfolioID: "t2", Symbol: "ETHUSDT", Side: "short", Action: "close", TradeTime: base + 1000, Qty: 1, TraderPrice: 90, SeenPrice: 90, BenchPrice: 100},
	}
	rows := PaperReport(fills)
	for _, r := range rows {
		if r.Table == "all" {
			if r.Win != 1 || r.Avg <= 0 {
				t.Fatalf("short in a fall must win: %+v", r)
			}
			return
		}
	}
	t.Fatal("no all row")
}

func TestDelayBucket(t *testing.T) {
	cases := map[int64]string{
		30_000:        "under 5 min",
		5 * 60_000:    "5-15 min",
		20 * 60_000:   "15-60 min",
		4000 * 60_000: "over 60 min",
	}
	for ms, want := range cases {
		if got := delayBucket(ms); got != want {
			t.Fatalf("delayBucket(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestPaperFillDedup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewCopyTradeStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	f := store.CopyTradePaperFill{RosterID: "r1", PortfolioID: "x", Symbol: "BTCUSDT", Side: "long",
		Action: "open", TradeTime: 1000, Qty: 1, TraderPrice: 100, SeenPrice: 100}
	if n, err := st.AddPaperFills([]store.CopyTradePaperFill{f}); err != nil || n != 1 {
		t.Fatalf("first insert = %d, %v", n, err)
	}
	if n, err := st.AddPaperFills([]store.CopyTradePaperFill{f}); err != nil || n != 0 {
		t.Fatalf("duplicate insert = %d, %v (want 0)", n, err)
	}
	fills, err := st.PaperFills("r1")
	if err != nil || len(fills) != 1 {
		t.Fatalf("stored = %d, %v", len(fills), err)
	}
	if c, _ := st.PaperCursor("r1", "x"); c != 1000 {
		t.Fatalf("cursor = %d, want 1000", c)
	}
}
