package binance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"nofx/planning"
	"nofx/planning/core"
	"nofx/planning/live"
)

type paperBook struct{ book core.Snapshot }

func (m paperBook) Snapshot(context.Context, string, string) (core.Snapshot, error) {
	b := m.book
	b.Observed = time.Now().UTC()
	return b, nil
}

// An approved Frontier plan runs end to end on the paper account through the
// real live controller: resting entry, fill, stop and target, completion.
func TestPaperAccountRunsAnApprovedFrontierPlan(t *testing.T) {
	pt, fm := newTestPaperTrader(t, t.TempDir())
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "planning.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { sql.Close() })
	now := time.Now().UTC()
	book := core.Snapshot{Venue: "binance", Symbol: "BTCUSDT", Kind: "perpetual", Bid: "49999", Ask: "50000", BidSize: "100", AskSize: "100", Tick: "0.1", Step: "0.001", MinQty: "0.001", MinNotional: "5", Observed: now}
	idea := core.Idea{Name: "paper", Side: "long", Entry: "49000", Stop: "48000", Target: "52000"}
	plan, err := core.Compile(book, core.Constraints{Equity: "1000", MaxRisk: "10", MaxNotional: "600", MinRewardRisk: "2", FeeBPS: "10", SlippageBPS: "10"}, idea, now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := live.New(db, func(context.Context, string, string) (live.Broker, string, error) {
		return live.NOFXBroker{Trader: pt}, "paper-account", nil
	}, paperBook{book})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Shutdown)
	if err = (&planning.DraftStore{DB: db, UserID: "alice", ExchangeID: "paper", Venue: "binance"}).Add([]core.Plan{plan}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, err := c.Approve(ctx, "alice", plan.ID, plan.Hash, "APPROVE LIVE")
	if err != nil {
		t.Fatal(err)
	}
	if v, err = c.Execute(ctx, "alice", v.ID, v.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	wait := func(what string, ok func(live.View) bool) live.View {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			got, err := c.Get("alice", v.ID)
			if err == nil && ok(got) {
				return got
			}
			time.Sleep(200 * time.Millisecond)
		}
		got, _ := c.Get("alice", v.ID)
		t.Fatalf("timed out waiting for %s: state=%s error=%q orders=%+v", what, got.State, got.Error, got.Orders)
		return got
	}
	status := func(v live.View, role string) string {
		for _, o := range v.Orders {
			if o.Role == role {
				return o.Status
			}
		}
		return ""
	}
	wait("the entry to rest", func(v live.View) bool { return status(v, "entry") == "NEW" })
	setMark(pt, fm, "BTCUSDT", 48950)
	got := wait("fill and protection", func(v live.View) bool {
		return status(v, "entry") == "FILLED" && status(v, "sl") == "NEW" && status(v, "tp") == "NEW"
	})
	got = wait("a confirmed protected position", func(v live.View) bool { return v.State == "position_open" })
	if got.Error != "" {
		t.Fatalf("protected position must not need attention: %s", got.Error)
	}
	setMark(pt, fm, "BTCUSDT", 52100)
	got = wait("the target to close the trade", func(v live.View) bool { return v.State == "completed" })
	if status(got, "tp") != "FILLED" || status(got, "sl") != "EXPIRED" {
		t.Fatalf("unexpected exits: %+v", got.Orders)
	}
	account, _ := pt.AccountSnapshot(ctx, "BTCUSDT")
	if account.Position != "0.00000000" || account.OpenOrders != 0 || paperFloat(account.Equity) <= 1000 {
		t.Fatalf("winning paper trade must end flat and in profit: %+v", account)
	}
}
