package mirror

import (
	"context"
	"math"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"nofx/store"
)

type rig struct {
	t        *testing.T
	e        *Engine
	st       *store.MirrorStore
	leader   *fakeAccount
	follower *fakeAccount
	r        *runner
	link     store.MirrorLink
}

func newRig(t *testing.T, link store.MirrorLink) *rig {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMirrorStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, st: st, leader: newFake(10000), follower: newFake(1000)}
	g.e = New(st, Deps{
		Leader:   func(string) (Account, error) { return g.leader, nil },
		Follower: func(string, string) (Account, error) { return g.follower, nil },
		Busy:     func(store.MirrorLink) string { return "" },
	})
	link.ID, link.UserID, link.LeaderID, link.ExchangeID, link.Running = "L1", "u", "ai", "acc", true
	if err := st.SaveLink(&link); err != nil {
		t.Fatal(err)
	}
	g.link = link
	g.r = &runner{link: "L1", errors: map[string]errorSeen{}, first: true}
	return g
}

// price sets a coin's price on both accounts.
func (g *rig) price(symbol string, p float64) {
	g.leader.setPrice(symbol, p)
	g.follower.setPrice(symbol, p)
}

// tick runs one pass; the copies' last orders are moved back so the
// follower check does not wait.
func (g *rig) tick() bool {
	g.t.Helper()
	states, _ := g.st.Positions("L1")
	for _, s := range states {
		s.LinkID, s.OrderAt = "L1", s.OrderAt-goneAfterMs
		_ = g.st.SavePosition(&s)
	}
	g.r.protectAt = g.r.protectAt.Add(-protectInterval)
	return g.e.tick(context.Background(), g.r)
}

func (g *rig) state(symbol, side string) store.MirrorPosition {
	states, _ := g.st.Positions("L1")
	for _, s := range states {
		if s.Symbol == symbol && s.Side == side {
			return s
		}
	}
	return store.MirrorPosition{}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

func TestEngineCopiesTheAIsLifecycle(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 50, LeverageMode: "follow",
		MaxLeverage: 5, FollowProtect: true})
	g.price("BTCUSDT", 50000)
	g.price("ETHUSDT", 2000)
	_, _ = g.leader.OpenLong("BTCUSDT", 0.1, 10)

	// The BTC long was open before the link: tracked, never copied.
	g.tick()
	if g.follower.qty("BTCUSDT", "long") != 0 || g.state("BTCUSDT", "long").Skip != SkipBeforeStart {
		t.Fatalf("old position copied: %v %+v", g.follower.orders, g.state("BTCUSDT", "long"))
	}
	if l, _ := g.st.LinkByID("L1"); g.r.status.Equity != 1000 || l.StartEquity != 1000 {
		t.Fatalf("status = %+v", g.r.status)
	}

	// A new short: 50 USDT margin at the 5x cap = 250 USDT = 0.125 ETH, with the AI's stops.
	_, _ = g.leader.OpenShort("ETHUSDT", 1, 20)
	_ = g.leader.SetStopLoss("ETHUSDT", "SHORT", 1, 2100)
	_ = g.leader.SetTakeProfit("ETHUSDT", "SHORT", 1, 1800)
	g.tick()
	if q := g.follower.qty("ETHUSDT", "short"); !near(q, 0.125) {
		t.Fatalf("copy = %v (%v)", q, g.follower.orders)
	}
	if g.follower.positions[key("ETHUSDT", "short")].Leverage != 5 {
		t.Fatalf("leverage not capped")
	}
	if g.follower.stops[key("ETHUSDT", "short")] != 2100 || g.follower.takes[key("ETHUSDT", "short")] != 1800 {
		t.Fatalf("stops = %v %v", g.follower.stops, g.follower.takes)
	}

	// The AI adds half: the copy adds half.
	_, _ = g.leader.OpenShort("ETHUSDT", 0.5, 20)
	g.tick()
	if q := g.follower.qty("ETHUSDT", "short"); !near(q, 0.1875) {
		t.Fatalf("after add = %v", q)
	}
	// Adding wiped the follower's stops on a real account; they are back.
	if g.follower.stops[key("ETHUSDT", "short")] != 2100 {
		t.Fatalf("stops after add = %v", g.follower.stops)
	}

	// The AI closes a third and moves its stop: the copy follows both.
	_, _ = g.leader.CloseShort("ETHUSDT", 0.5)
	_ = g.leader.CancelStopOrders("ETHUSDT")
	_ = g.leader.SetStopLoss("ETHUSDT", "SHORT", 1, 2050)
	g.tick()
	if q := g.follower.qty("ETHUSDT", "short"); !near(q, 0.125) {
		t.Fatalf("after reduce = %v", q)
	}
	if g.follower.stops[key("ETHUSDT", "short")] != 2050 || g.follower.takes[key("ETHUSDT", "short")] != 0 {
		t.Fatalf("moved stop = %v, take %v", g.follower.stops, g.follower.takes)
	}

	// The AI closes: the copy closes and its stops go.
	_, _ = g.leader.CloseShort("ETHUSDT", 0)
	g.tick()
	if g.follower.qty("ETHUSDT", "short") != 0 || g.state("ETHUSDT", "short").Symbol != "" {
		t.Fatalf("not closed: %v", g.follower.orders)
	}
	if len(g.follower.stops) != 0 {
		t.Fatalf("stops left: %v", g.follower.stops)
	}
	// The old BTC long closing needs nothing, and its state goes.
	_, _ = g.leader.CloseLong("BTCUSDT", 0)
	g.tick()
	if states, _ := g.st.Positions("L1"); len(states) != 0 {
		t.Fatalf("states left: %+v", states)
	}
	ev, _ := g.st.Events("L1", 50)
	if len(ev) == 0 || ev[0].Action != "close" {
		t.Fatalf("events = %+v", ev)
	}
}

func TestEngineRatioSizingUsesTheAIsShareOfEquity(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "ratio", Ratio: 0.5, LeverageMode: "fixed", Leverage: 4})
	g.price("SOLUSDT", 100)
	g.tick()
	// The AI puts 1000 of its 10000 equity up as margin (100 SOL at 10x):
	// 10% × 0.5 × 1000 = 50 USDT margin, at the fixed 4x = 200 USDT = 2 SOL.
	_, _ = g.leader.OpenLong("SOLUSDT", 100, 10)
	g.tick()
	if q := g.follower.qty("SOLUSDT", "long"); !near(q, 2) {
		t.Fatalf("copy = %v (%v)", q, g.follower.orders)
	}
}

func TestEngineDoesNotReopenACopyClosedOutside(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 100, LeverageMode: "fixed", Leverage: 2})
	g.price("BNBUSDT", 500)
	g.tick()
	_, _ = g.leader.OpenLong("BNBUSDT", 1, 5)
	g.tick()
	// The user closes the copy by hand; the AI keeps its position and adds.
	_, _ = g.follower.CloseLong("BNBUSDT", 0)
	g.tick()
	if g.state("BNBUSDT", "long").Skip != SkipFollowerClosed {
		t.Fatalf("state = %+v", g.state("BNBUSDT", "long"))
	}
	_, _ = g.leader.OpenLong("BNBUSDT", 1, 5)
	g.tick()
	if g.follower.qty("BNBUSDT", "long") != 0 {
		t.Fatalf("reopened: %v", g.follower.orders)
	}
	_, _ = g.leader.CloseLong("BNBUSDT", 0)
	g.tick()
	if g.state("BNBUSDT", "long").Symbol != "" {
		t.Fatalf("state kept after the AI closed")
	}
}

func TestEngineStopsAtTheLossCapAndClosesTheCopies(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 200, LeverageMode: "fixed", Leverage: 10, MaxLoss: 100})
	g.price("XRPUSDT", 1)
	g.tick()
	_, _ = g.leader.OpenLong("XRPUSDT", 1000, 10)
	g.tick()                 // 2000 XRP copied
	g.price("XRPUSDT", 0.94) // the copy is down 120
	if !g.tick() {
		t.Fatal("link kept running past the loss cap")
	}
	l, _ := g.st.LinkByID("L1")
	if l.Running || l.StopReason != "max_loss" || g.follower.qty("XRPUSDT", "long") != 0 {
		t.Fatalf("link = %+v, follower %v", l, g.follower.orders)
	}
	if g.leader.qty("XRPUSDT", "long") != 1000 {
		t.Fatal("the AI's own position was touched")
	}
}

func TestEngineSkipsFailedOpensAndRetriesCloses(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 50, LeverageMode: "fixed", Leverage: 5})
	g.price("DOGEUSDT", 0.1)
	g.tick()
	g.follower.failOpen = errString("insufficient margin")
	_, _ = g.leader.OpenLong("DOGEUSDT", 1000, 5)
	g.tick()
	g.follower.failOpen = nil
	g.tick()
	if g.follower.qty("DOGEUSDT", "long") != 0 || g.state("DOGEUSDT", "long").Skip != SkipOpenFailed {
		t.Fatalf("failed open retried: %v", g.follower.orders)
	}
	_, _ = g.leader.OpenShort("DOGEUSDT", 1000, 5)
	g.tick()
	g.follower.failClose = errString("timeout")
	_, _ = g.leader.CloseShort("DOGEUSDT", 0)
	g.tick()
	g.tick()
	if g.follower.qty("DOGEUSDT", "short") == 0 {
		t.Fatal("close went through while failing")
	}
	ev, _ := g.st.Events("L1", 50)
	errorsLogged := 0
	for _, e := range ev {
		if e.Action == "error" {
			errorsLogged++
		}
	}
	if errorsLogged != 1 {
		t.Fatalf("errors logged %d times, want once", errorsLogged)
	}
	g.follower.failClose = nil
	g.tick()
	if g.follower.qty("DOGEUSDT", "short") != 0 {
		t.Fatal("close not retried")
	}
}

func TestEngineClosesOnlyTheCopyWhenTheUserHoldsMore(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 100, LeverageMode: "fixed", Leverage: 1})
	g.price("ADAUSDT", 1)
	g.tick()
	_, _ = g.leader.OpenLong("ADAUSDT", 10, 1)
	g.tick()                                     // 100 ADA copied
	_, _ = g.follower.OpenLong("ADAUSDT", 50, 1) // the user's own
	_, _ = g.leader.CloseLong("ADAUSDT", 0)
	g.tick()
	if q := g.follower.qty("ADAUSDT", "long"); !near(q, 50) {
		t.Fatalf("left %v, want the user's 50", q)
	}
}

func TestEngineResumeDoesNotChaseAddsMadeWhileStopped(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 100, LeverageMode: "fixed", Leverage: 1})
	g.price("LTCUSDT", 100)
	g.tick()
	_, _ = g.leader.OpenLong("LTCUSDT", 1, 1)
	g.tick() // 1 LTC copied
	// Stopped; the AI adds; started again.
	_, _ = g.leader.OpenLong("LTCUSDT", 1, 1)
	g.r.first = true
	g.tick()
	if q := g.follower.qty("LTCUSDT", "long"); !near(q, 1) {
		t.Fatalf("chased the add: %v", q)
	}
	// From here on the AI's halving is copied as a halving.
	_, _ = g.leader.CloseLong("LTCUSDT", 1)
	g.tick()
	if q := g.follower.qty("LTCUSDT", "long"); !near(q, 0.5) {
		t.Fatalf("after reduce = %v", q)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestStartAndStopLinkClosingTheCopies(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 100, LeverageMode: "fixed", Leverage: 1})
	g.link.Running = false
	_ = g.st.SaveLink(&g.link)
	g.price("BTCUSDT", 100)
	_, _ = g.follower.OpenLong("BTCUSDT", 1, 1)
	_ = g.st.SavePosition(&store.MirrorPosition{LinkID: "L1", Symbol: "BTCUSDT", Side: "long", LeaderQty: 1, FollowerQty: 1})
	if err := g.e.StartLink(context.Background(), g.link); err != nil {
		t.Fatal(err)
	}
	if !g.e.Running("L1") {
		t.Fatal("not running")
	}
	failed, err := g.e.StopLink("u", "L1", true)
	if err != nil || len(failed) != 0 {
		t.Fatalf("stop: %v %v", err, failed)
	}
	l, _ := g.st.LinkByID("L1")
	if g.e.Running("L1") || l.Running || l.StopReason != "manual" || g.follower.qty("BTCUSDT", "long") != 0 {
		t.Fatalf("link %+v, follower %v", l, g.follower.orders)
	}
}

func TestStatusIsHiddenUntilTheFirstTick(t *testing.T) {
	g := newRig(t, store.MirrorLink{SizeMode: "fixed", FixedMargin: 10, LeverageMode: "fixed", Leverage: 1})
	g.e.runners["L1"] = g.r
	if _, ok := g.e.Status("L1"); ok {
		t.Fatal("status before any tick")
	}
	g.tick()
	st, ok := g.e.Status("L1")
	if !ok || st.Positions == nil {
		t.Fatalf("status = %+v %v", st, ok)
	}
}

func TestFixedLeverageIsNotCappedByTheFollowCap(t *testing.T) {
	l := Position{Leverage: 5}
	fixed := store.MirrorLink{LeverageMode: "fixed", Leverage: 20, MaxLeverage: 10}
	if got := leverageFor(fixed, l); got != 20 {
		t.Fatalf("fixed 20x opened at %dx", got)
	}
	follow := store.MirrorLink{LeverageMode: "follow", Leverage: 20, MaxLeverage: 10}
	if got := leverageFor(follow, Position{Leverage: 25}); got != 10 {
		t.Fatalf("follow capped at 10 opened at %dx", got)
	}
	if got := leverageFor(follow, l); got != 5 {
		t.Fatalf("follow 5x opened at %dx", got)
	}
}
