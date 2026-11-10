package binance

import (
	"context"
	"testing"
	"time"

	"nofx/trader/execution"
)

// setMark moves the fake Binance price and drops the simulator's cache.
func setMark(pt *PaperTrader, fm *fakeMarket, symbol string, price float64) {
	fm.set(symbol, price)
	pt.priceMu.Lock()
	delete(pt.priceCache, symbol)
	pt.priceMu.Unlock()
}

func execOrder(t *testing.T, pt *PaperTrader, n byte, role, side, price, qty string) execution.Order {
	t.Helper()
	id := []byte("00000000000000000000000000000000")
	id[31] = '0' + n
	o := execution.Order{ClientID: string(id), Role: role, Symbol: "BTCUSDT", Side: side, Price: execution.Decimal(price), Quantity: execution.Decimal(qty), Status: "submitting", Filled: "0"}
	route, err := pt.RouteClientID(o.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	o.VenueClientID = route
	return o
}

func TestPaperFrontierLimitEntryRestsThenFillsAndStopCloses(t *testing.T) {
	pt, fm := newTestPaperTrader(t, t.TempDir())
	ctx := context.Background()
	if err := pt.PrepareExecutionLeverage(ctx, "BTCUSDT", 5); err != nil {
		t.Fatal(err)
	}
	entry, err := pt.SubmitOrder(ctx, execOrder(t, pt, 1, "entry", "long", "49000", "0.01"))
	if err != nil || entry.Status != "NEW" || entry.OrderID == "" || entry.Filled != "0" {
		t.Fatalf("limit below the market must rest: %+v %v", entry, err)
	}
	account, _ := pt.AccountSnapshot(ctx, "BTCUSDT")
	if account.OpenOrders != 1 || account.Position != "0.00000000" {
		t.Fatalf("resting entry not reported: %+v", account)
	}
	if paperFloat(account.Available) > 1000-49000*0.01/5 {
		t.Fatalf("resting entry must reserve margin: %+v", account)
	}
	setMark(pt, fm, "BTCUSDT", 48900)
	entry, err = pt.LookupOrder(ctx, entry)
	if err != nil || entry.Status != "FILLED" || entry.Filled != "0.01" || paperFloat(entry.Average) != 49000 {
		t.Fatalf("limit must fill at its price once reached: %+v %v", entry, err)
	}
	account, _ = pt.AccountSnapshot(ctx, "BTCUSDT")
	if account.Position != "0.01000000" || account.OpenOrders != 0 {
		t.Fatalf("filled entry must become a position: %+v", account)
	}
	stop, err := pt.SubmitOrder(ctx, execOrder(t, pt, 2, "sl", "long", "48000", "0.01"))
	if err != nil || stop.Status != "NEW" {
		t.Fatalf("stop must rest: %+v %v", stop, err)
	}
	target, _ := pt.SubmitOrder(ctx, execOrder(t, pt, 3, "tp", "long", "52000", "0.01"))
	setMark(pt, fm, "BTCUSDT", 47900)
	stop, err = pt.LookupOrder(ctx, stop)
	if err != nil || stop.Status != "FILLED" || stop.Filled != "0.01" {
		t.Fatalf("stop must close the position: %+v %v", stop, err)
	}
	target, _ = pt.LookupOrder(ctx, target)
	if target.Status != "EXPIRED" {
		t.Fatalf("protection on a flat position must expire, got %s", target.Status)
	}
	account, _ = pt.AccountSnapshot(ctx, "BTCUSDT")
	if account.Position != "0.00000000" || account.OpenOrders != 0 || paperFloat(account.Equity) >= 1000 {
		t.Fatalf("stopped-out trade must leave a flat, smaller account: %+v", account)
	}
}

func TestPaperFrontierMarketEntryAndReduceOnlyClose(t *testing.T) {
	pt, _ := newTestPaperTrader(t, t.TempDir())
	ctx := context.Background()
	o := execOrder(t, pt, 1, "entry", "short", "50000", "0.01")
	o.Type = "market"
	got, err := pt.SubmitOrder(ctx, o)
	if err != nil || got.Status != "FILLED" || paperFloat(got.Average) >= 50000 {
		t.Fatalf("market short must fill below the mark: %+v %v", got, err)
	}
	closer := execOrder(t, pt, 2, "entry", "long", "50000", "0.02")
	closer.Type, closer.ReduceOnly = "market", true
	got, err = pt.SubmitOrder(ctx, closer)
	if err != nil || got.Status != "EXPIRED" || got.Filled != "0.01000000" {
		t.Fatalf("reduce-only must close only the open quantity: %+v %v", got, err)
	}
	if account, _ := pt.AccountSnapshot(ctx, "BTCUSDT"); account.Position != "0.00000000" {
		t.Fatalf("position must be flat: %+v", account)
	}
}

func TestPaperFrontierReceiptsForDeterministicOutcomes(t *testing.T) {
	pt, _ := newTestPaperTrader(t, t.TempDir())
	ctx := context.Background()
	big := execOrder(t, pt, 1, "entry", "long", "49000", "1")
	if got, err := pt.SubmitOrder(ctx, big); err != nil || got.Status != "REJECTED" || got.OrderID == "" {
		t.Fatalf("insufficient margin must be an explicit rejection: %+v %v", got, err)
	}
	post := execOrder(t, pt, 2, "entry", "long", "51000", "0.01")
	post.PostOnly = true
	if got, _ := pt.SubmitOrder(ctx, post); got.Status != "EXPIRED" {
		t.Fatalf("crossing post-only must expire, got %s", got.Status)
	}
	if got, _ := pt.SubmitOrder(ctx, execOrder(t, pt, 3, "sl", "long", "51000", "0.01")); got.Status != "REJECTED" {
		t.Fatalf("stop above the mark would trigger immediately, got %s", got.Status)
	}
	if _, err := pt.SubmitOrder(ctx, execOrder(t, pt, 1, "entry", "long", "49000", "1")); err == nil {
		t.Fatal("duplicate client ID must not create a second order")
	}
	unrouted := execOrder(t, pt, 4, "entry", "long", "49000", "0.01")
	unrouted.VenueClientID = ""
	if _, err := pt.SubmitOrder(ctx, unrouted); err == nil {
		t.Fatal("an order without its persisted route must be refused")
	}
	missing := execOrder(t, pt, 9, "entry", "long", "49000", "0.01")
	if _, err := pt.LookupOrder(ctx, missing); err == nil {
		t.Fatal("unknown order must be unconfirmed, never assumed filled")
	}
}

func TestPaperFrontierCancelReleasesMarginAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	pt, _ := newTestPaperTrader(t, dir)
	ctx := context.Background()
	entry, _ := pt.SubmitOrder(ctx, execOrder(t, pt, 1, "entry", "long", "49000", "0.01"))
	if err := pt.CancelExecutionOrder(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := pt.CancelExecutionOrder(ctx, entry); err == nil {
		t.Fatal("cancelling a finished order must not report success")
	}
	reloaded := &PaperTrader{market: pt.market, statePath: pt.statePath, priceCache: map[string]paperPrice{}, precision: map[string]int{}}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.LookupOrder(ctx, entry)
	if err != nil || got.Status != "CANCELED" {
		t.Fatalf("order state must persist: %+v %v", got, err)
	}
	if account, _ := reloaded.AccountSnapshot(ctx, "BTCUSDT"); account.OpenOrders != 0 || paperFloat(account.Available) != 1000 {
		t.Fatalf("cancel must release reserved margin: %+v", account)
	}
}

func TestPaperFrontierBackgroundMatchingWaitsForQuietOrders(t *testing.T) {
	pt, fm := newTestPaperTrader(t, t.TempDir())
	ctx := context.Background()
	entry, _ := pt.SubmitOrder(ctx, execOrder(t, pt, 1, "entry", "long", "49000", "0.01"))
	setMark(pt, fm, "BTCUSDT", 48000)
	pt.checkTriggers()
	if account, _ := pt.AccountSnapshot(ctx, "BTCUSDT"); account.Position != "0.00000000" {
		t.Fatal("an actively polled order must be matched by its lookup, not in the background")
	}
	pt.mu.Lock()
	pt.state.ExecOrders[0].Polled = time.Now().Add(-time.Minute)
	pt.mu.Unlock()
	pt.checkTriggers()
	if got, _ := pt.LookupOrder(ctx, entry); got.Status != "FILLED" {
		t.Fatalf("an unattended order must still fill in the background, got %s", got.Status)
	}
}
