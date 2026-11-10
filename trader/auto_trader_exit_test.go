package trader

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"
)

type exitTestTrader struct {
	Trader
	mu          sync.Mutex
	side        string
	quantity    float64
	mark        float64
	positionErr error
	closeErr    error
	closes      int
}

func (m *exitTestTrader) GetPositions() ([]map[string]interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.positionErr != nil {
		return nil, m.positionErr
	}
	if m.quantity == 0 {
		return []map[string]interface{}{}, nil
	}
	return []map[string]interface{}{{"symbol": "SOLUSDT", "side": m.side, "positionAmt": m.quantity, "entryPrice": 95.31906, "markPrice": m.mark}}, nil
}
func (m *exitTestTrader) close(side string, qty float64) (map[string]interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if side != m.side || qty != 0 {
		return nil, fmt.Errorf("expected close-all of actual side")
	}
	if m.closeErr != nil {
		return nil, m.closeErr
	}
	m.closes++
	m.quantity = 0
	return map[string]interface{}{"orderId": int64(123)}, nil
}
func (m *exitTestTrader) CloseLong(_ string, qty float64) (map[string]interface{}, error) {
	return m.close("long", qty)
}
func (m *exitTestTrader) CloseShort(_ string, qty float64) (map[string]interface{}, error) {
	return m.close("short", qty)
}
func (m *exitTestTrader) GetMarketPrice(string) (float64, error) {
	panic("close must not need a separate quote/K-line call")
}

func TestRiskExitNeedsNoCandleService(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		m := &exitTestTrader{side: side, quantity: 23.11, mark: 94.59}
		at := &AutoTrader{trader: m, exchange: "binance_paper"}
		record := &store.DecisionAction{}
		if err := at.executeDecisionWithRecord(&kernel.Decision{Symbol: "SOLUSDT", Action: "close_" + side}, record); err != nil {
			t.Fatal(err)
		}
		if m.closes != 1 || record.Quantity != 23.11 || record.Price != 94.59 || record.OrderID != 123 {
			t.Fatalf("wrong current position/fill metadata: %+v", record)
		}
	}
}

func TestConcurrentAIExitsDoNotDuplicateClosedPosition(t *testing.T) {
	m := &exitTestTrader{side: "long", quantity: 23.11, mark: 94.59}
	at := &AutoTrader{trader: m, exchange: "binance_paper"}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- at.executeCurrentPositionClose("SOLUSDT", "long", nil) }()
	}
	a, b := <-results, <-results
	if (a == nil) == (b == nil) || m.closes != 1 {
		t.Fatalf("duplicate close: %v %v calls=%d", a, b, m.closes)
	}
}

func TestCloseValidatesCurrentExposureNotStaleContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		quantity float64
		side     string
		err      error
	}{
		{"already closed", 0, "long", nil}, {"wrong side", 1, "short", nil}, {"cannot read", 1, "long", errors.New("position source failed")}, {"invalid quantity", math.NaN(), "long", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &exitTestTrader{side: tc.side, quantity: tc.quantity, mark: 95, positionErr: tc.err}
			at := &AutoTrader{trader: m, exchange: "binance_paper"}
			if err := at.executeCurrentPositionClose("SOLUSDT", "long", nil); err == nil || m.closes != 0 {
				t.Fatalf("unexpected close: %v calls=%d", err, m.closes)
			}
		})
	}
}

func TestMissingMarkDoesNotPreventRiskReduction(t *testing.T) {
	m := &exitTestTrader{side: "long", quantity: 1, mark: math.NaN()}
	at := &AutoTrader{trader: m, exchange: "binance_paper"}
	record := &store.DecisionAction{}
	if err := at.executeCurrentPositionClose("SOLUSDT", "long", record); err != nil || m.closes != 1 || record.Price != 0 {
		t.Fatalf("missing display mark blocked close: %v %+v", err, record)
	}
}

func TestCloseErrorDoesNotClaimAnExit(t *testing.T) {
	m := &exitTestTrader{side: "long", quantity: 1, mark: 95, closeErr: errors.New("fixture rejection")}
	at := &AutoTrader{trader: m, exchange: "binance_paper"}
	if err := at.executeCurrentPositionClose("SOLUSDT", "long", nil); err == nil || m.quantity != 1 || m.closes != 0 {
		t.Fatal("rejected order falsely closed the position")
	}
}

func TestExitFixPreservesReentryCooldown(t *testing.T) {
	st, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := &AutoTrader{id: "fixture", store: st}
	order := &store.TraderOrder{TraderID: at.id, ExchangeID: "paper", ExchangeType: "binance_paper", ExchangeOrderID: "fixture-close", Symbol: "SOLUSDT", Side: "SELL", PositionSide: "LONG", Type: "MARKET", Status: "FILLED", OrderAction: "close_long", CreatedAt: time.Now().Add(-time.Hour).UnixMilli()}
	if err := st.Order().CreateOrder(order); err != nil {
		t.Fatal(err)
	}
	if r := at.tradeThrottleReason(kernel.Decision{Symbol: "SOLUSDT", Action: "open_long"}, &kernel.Context{}); r == "" {
		t.Fatal("removed reentry cooldown along with unsafe exit gate")
	}
}
