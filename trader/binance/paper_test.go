package binance

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeMarket struct {
	mu     sync.Mutex
	prices map[string]float64
}

func (f *fakeMarket) set(symbol string, price float64) {
	f.mu.Lock()
	f.prices[symbol] = price
	f.mu.Unlock()
}

// newTestPaperTrader builds a simulator against a local fake Binance server.
func newTestPaperTrader(t *testing.T, dir string) (*PaperTrader, *fakeMarket) {
	t.Helper()
	t.Setenv("PAPER_INITIAL_BALANCE", "1000")
	fm := &fakeMarket{prices: map[string]float64{"BTCUSDT": 50000, "ETHUSDT": 3000}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/ticker/price"):
			symbol := r.URL.Query().Get("symbol")
			fm.mu.Lock()
			price := fm.prices[symbol]
			fm.mu.Unlock()
			fmt.Fprintf(w, `{"symbol":%q,"price":"%f"}`, symbol, price)
		case strings.HasSuffix(r.URL.Path, "/exchangeInfo"):
			fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSDT","filters":[{"filterType":"LOT_SIZE","stepSize":"0.001"}]},`+
				`{"symbol":"ETHUSDT","filters":[{"filterType":"LOT_SIZE","stepSize":"0.001"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	market := NewExecutionTrader("", "", "")
	market.client.BaseURL = srv.URL
	market.client.HTTPClient = srv.Client()
	pt := &PaperTrader{
		market:     market,
		statePath:  filepath.Join(dir, "acct.json"),
		priceCache: map[string]paperPrice{},
		precision:  map[string]int{},
	}
	if err := pt.load(); err != nil {
		t.Fatal(err)
	}
	return pt, fm
}

// expire forces the next read to fetch a fresh price.
func (p *PaperTrader) expirePrices() {
	p.priceMu.Lock()
	p.priceCache = map[string]paperPrice{}
	p.priceMu.Unlock()
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestPaperLongRoundTripAccountsFeesAndPnL(t *testing.T) {
	pt, fm := newTestPaperTrader(t, t.TempDir())

	res, err := pt.OpenLong("BTCUSDT", 0.01, 5)
	if err != nil {
		t.Fatal(err)
	}
	entry := 50000 * (1 + paperSlippageRate)
	openFee := 0.01 * entry * paperTakerFeeRate

	positions, _ := pt.GetPositions()
	if len(positions) != 1 || positions[0]["side"] != "long" || !near(positions[0]["positionAmt"].(float64), 0.01) {
		t.Fatalf("unexpected positions: %+v", positions)
	}

	fm.set("BTCUSDT", 51000)
	pt.expirePrices()
	if _, err := pt.CloseLong("BTCUSDT", 0); err != nil {
		t.Fatal(err)
	}
	exit := 51000 * (1 - paperSlippageRate)
	closeFee := 0.01 * exit * paperTakerFeeRate
	want := 1000 + (exit-entry)*0.01 - openFee - closeFee

	bal, _ := pt.GetBalance()
	if got := bal["totalWalletBalance"].(float64); !near(got, want) {
		t.Fatalf("wallet = %.6f, want %.6f", got, want)
	}
	if positions, _ := pt.GetPositions(); len(positions) != 0 {
		t.Fatalf("position should be closed: %+v", positions)
	}

	status, err := pt.GetOrderStatus("BTCUSDT", fmt.Sprint(res["orderId"]))
	if err != nil || status["status"] != "FILLED" || !near(status["avgPrice"].(float64), entry) {
		t.Fatalf("order status = %+v, err=%v", status, err)
	}
	closed, _ := pt.GetClosedPnL(time.Now().Add(-time.Minute), 10)
	if len(closed) != 1 || closed[0].CloseType != "manual" {
		t.Fatalf("closed records = %+v", closed)
	}
}

func TestPaperShortProfitsWhenPriceFalls(t *testing.T) {
	pt, fm := newTestPaperTrader(t, t.TempDir())
	if _, err := pt.OpenShort("ETHUSDT", 0.5, 3); err != nil {
		t.Fatal(err)
	}
	fm.set("ETHUSDT", 2900)
	pt.expirePrices()
	positions, _ := pt.GetPositions()
	if len(positions) != 1 || positions[0]["positionAmt"].(float64) >= 0 {
		t.Fatalf("short should have negative positionAmt: %+v", positions)
	}
	if upnl := positions[0]["unRealizedProfit"].(float64); upnl <= 0 {
		t.Fatalf("short should be in profit, got %.4f", upnl)
	}
}

func TestPaperStopLossTriggersAndQueuesCloseEvent(t *testing.T) {
	pt, fm := newTestPaperTrader(t, t.TempDir())
	if _, err := pt.OpenLong("BTCUSDT", 0.01, 5); err != nil {
		t.Fatal(err)
	}
	if err := pt.SetStopLoss("BTCUSDT", "LONG", 0.01, 49000); err != nil {
		t.Fatal(err)
	}
	if orders, _ := pt.GetOpenOrders("BTCUSDT"); len(orders) != 1 || orders[0].Type != "STOP_MARKET" {
		t.Fatalf("open orders = %+v", orders)
	}

	fm.set("BTCUSDT", 48900)
	pt.expirePrices()
	pt.checkTriggers()

	if positions, _ := pt.GetPositions(); len(positions) != 0 {
		t.Fatalf("stop-loss should close the position: %+v", positions)
	}
	events := pt.DrainCloseEvents()
	if len(events) != 1 || events[0].Reason != "stop_loss" || events[0].Side != "long" {
		t.Fatalf("events = %+v", events)
	}
	if again := pt.DrainCloseEvents(); len(again) != 0 {
		t.Fatalf("events must be drained once, got %+v", again)
	}
}

func TestPaperLiquidationLosesRoughlyTheMargin(t *testing.T) {
	pt, fm := newTestPaperTrader(t, t.TempDir())
	if _, err := pt.OpenLong("BTCUSDT", 0.1, 10); err != nil {
		t.Fatal(err)
	}
	fm.set("BTCUSDT", 44000) // -12% at 10x
	pt.expirePrices()
	pt.checkTriggers()

	events := pt.DrainCloseEvents()
	if len(events) != 1 || events[0].Reason != "liquidation" {
		t.Fatalf("expected liquidation, got %+v", events)
	}
	bal, _ := pt.GetBalance()
	margin := 0.1 * 50000 * (1 + paperSlippageRate) / 10
	if lost := 1000 - bal["totalWalletBalance"].(float64); lost < margin*0.9 || lost > margin*1.1 {
		t.Fatalf("lost %.2f, expected about the %.2f margin", lost, margin)
	}
}

func TestPaperRejectsOrdersBeyondVirtualBalance(t *testing.T) {
	pt, _ := newTestPaperTrader(t, t.TempDir())
	if _, err := pt.OpenLong("BTCUSDT", 1, 2); err == nil || !strings.Contains(err.Error(), "insufficient virtual balance") {
		t.Fatalf("expected insufficient balance error, got %v", err)
	}
	if _, err := pt.OpenLong("BTCUSDT", 0.0001, 5); err == nil {
		t.Fatal("expected min-notional / rounding error for a 5 USDT order")
	}
}

func TestPaperStatePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	pt, _ := newTestPaperTrader(t, dir)
	if _, err := pt.OpenLong("BTCUSDT", 0.01, 5); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := newTestPaperTrader(t, dir)
	positions, _ := reloaded.GetPositions()
	if len(positions) != 1 || positions[0]["symbol"] != "BTCUSDT" {
		t.Fatalf("state was not persisted: %+v", positions)
	}
}
