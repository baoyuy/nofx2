package trader

import (
	"errors"
	"testing"
	"time"

	"nofx/store"
)

// paperBook is a fake paper venue: GetPositions returns exactly what it holds,
// in the same shape PaperTrader uses (shorts carry a negative positionAmt).
type paperBook struct {
	Trader
	positions []map[string]interface{}
	err       error
}

func (p *paperBook) GetPositions() ([]map[string]interface{}, error) {
	return p.positions, p.err
}

func heldPos(symbol, side string, qty float64) map[string]interface{} {
	amt := qty
	if side == "short" {
		amt = -qty // Binance hedge-mode convention, as PaperTrader reports it
	}
	return map[string]interface{}{"symbol": symbol, "side": side, "positionAmt": amt}
}

func openRow(t *testing.T, st *store.Store, exchangeID, symbol, side string, qty float64, at int64) int64 {
	t.Helper()
	row := &store.TraderPosition{TraderID: "ai", ExchangeID: exchangeID, Symbol: symbol, Side: side,
		Quantity: qty, EntryQuantity: qty, EntryPrice: 100, EntryTime: at, Status: "OPEN"}
	if err := st.Position().Create(row); err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func rowStatus(t *testing.T, st *store.Store, exchangeID string) map[int64]string {
	t.Helper()
	open, err := st.Position().GetOpenPositionsByExchange(exchangeID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]string{}
	for _, r := range open {
		out[r.ID] = "OPEN"
	}
	return out
}

func paperFixture(t *testing.T) (*store.Store, *AutoTrader, *paperBook) {
	t.Helper()
	st, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	book := &paperBook{}
	at := &AutoTrader{id: "ai", exchange: "binance_paper", exchangeID: "acct", store: st, trader: book}
	return st, at, book
}

// The server case: the paper account holds only the ETH short, but the
// database still shows two UNI longs and a ZEC short that were
// emergency-closed. Reconciling must close those three and keep the ETH short.
func TestPaperReconcileClosesEmergencyClosedRowsAndKeepsTheLiveShort(t *testing.T) {
	st, at, book := paperFixture(t)
	now := time.Now().UnixMilli()
	uni1 := openRow(t, st, "acct", "UNIUSDT", "LONG", 43, now-3*3600e3)
	uni2 := openRow(t, st, "acct", "UNIUSDT", "LONG", 44, now-2*3600e3)
	zec := openRow(t, st, "acct", "ZECUSDT", "SHORT", 0.218, now-3600e3)
	eth := openRow(t, st, "acct", "ETHUSDT", "SHORT", 0.177, now-1800e3)
	book.positions = []map[string]interface{}{heldPos("ETHUSDT", "short", 0.177)}

	if err := at.reconcilePaperPositions(); err != nil {
		t.Fatal(err)
	}
	open := rowStatus(t, st, "acct")
	for _, id := range []int64{uni1, uni2, zec} {
		if open[id] == "OPEN" {
			t.Fatalf("row %d is not held by the paper account and must be closed; open=%v", id, open)
		}
	}
	if open[eth] != "OPEN" {
		t.Fatalf("the live ETH short must stay OPEN (a negative positionAmt is still a holding); open=%v", open)
	}
}

// A short is reported with a negative amount. Treating "qty <= 0" as "not held"
// (the long-only reading) would close every live short; guard it explicitly.
func TestLiveQtyTreatsNegativeShortAmountAsHeld(t *testing.T) {
	live, err := liveQtyFromPositions([]map[string]interface{}{
		heldPos("ETHUSDT", "short", 0.177),
		heldPos("BTCUSDT", "long", 0.01),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := live[store.LivePositionKey("ETHUSDT", "SHORT")]; got != 0.177 {
		t.Fatalf("short quantity = %v, want 0.177", got)
	}
	if got := live[store.LivePositionKey("BTCUSDT", "LONG")]; got != 0.01 {
		t.Fatalf("long quantity = %v, want 0.01", got)
	}
}

// If the venue cannot be read, nothing is reconciled: treating a failed read as
// "holds nothing" would close every open row.
func TestPaperReconcileDoesNothingWhenTheVenueCannotBeRead(t *testing.T) {
	st, at, book := paperFixture(t)
	id := openRow(t, st, "acct", "ETHUSDT", "SHORT", 0.177, time.Now().UnixMilli())
	book.err = errors.New("fixture outage")

	if err := at.reconcilePaperPositions(); err == nil {
		t.Fatal("a failed read must be reported, not silently treated as an empty account")
	}
	if rowStatus(t, st, "acct")[id] != "OPEN" {
		t.Fatal("a failed read must leave every row as it was")
	}
}

func TestPaperReconcileRefusesAnUnreadablePosition(t *testing.T) {
	st, at, book := paperFixture(t)
	id := openRow(t, st, "acct", "ETHUSDT", "SHORT", 0.177, time.Now().UnixMilli())
	book.positions = []map[string]interface{}{{"symbol": "ETHUSDT", "side": "short"}} // no positionAmt

	if err := at.reconcilePaperPositions(); err == nil {
		t.Fatal("a position without a readable quantity must stop the reconcile")
	}
	if rowStatus(t, st, "acct")[id] != "OPEN" {
		t.Fatal("an unreadable venue state must leave rows untouched")
	}
}

// Reconciliation is scoped to this paper account; another account's rows are
// never closed because this account does not hold them.
func TestPaperReconcileOnlyTouchesItsOwnAccount(t *testing.T) {
	st, at, book := paperFixture(t)
	other := openRow(t, st, "other-acct", "SOLUSDT", "LONG", 3, time.Now().UnixMilli())
	book.positions = nil // this account holds nothing

	if err := at.reconcilePaperPositions(); err != nil {
		t.Fatal(err)
	}
	if rowStatus(t, st, "other-acct")[other] != "OPEN" {
		t.Fatal("rows on a different exchange account must not be reconciled against this one")
	}
}
