package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"nofx/planning/core"
	"nofx/planning/market"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestPlanningTrendBoardIsFreeCachedAndDrivesEntryPolicy(t *testing.T) {
	reads := 0
	p := &planningServices{trendRead: func(context.Context) (market.TrendBoard, error) {
		reads++
		return market.TrendBoard{Coins: []market.TrendCoin{{Pair: "BTCUSDT", Score: 90}, {Pair: "ETHUSDT", Score: 80}}, Scanned: 60, FetchedAt: time.Now().UTC()}, nil
	}}
	// No store is configured: the free board must not look up any wallet.
	s := &Server{planning: p}
	s.planningOnce.Do(func() {})
	request := func(user string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user_id", user)
		c.Request = httptest.NewRequest("GET", "/api/planning/ai500", nil)
		s.handlePlanningAI500(c)
		return w
	}
	for _, user := range []string{"alice", "bob", "alice"} {
		w := request(user)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "ETHUSDT") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if reads != 1 {
		t.Fatal("public board should be built once and shared", reads)
	}
	for _, tc := range []struct {
		venue, symbol, group string
		restricted           bool
	}{
		{"binance", "ETHUSDT", "", true},
		{"binance", "XMRUSDT", "", false},
		{"hyperliquid", "ETH", "", false},
		{"binance", "QUSDT", "ai500", true},
	} {
		limits := p.planningEntryPolicy(tc.venue, tc.symbol, tc.group, core.Constraints{})
		if (limits.EntryPolicy == core.EntryAI500TrendLong) != tc.restricted {
			t.Fatalf("unexpected entry policy for %+v: %+v", tc, limits)
		}
	}
	if reads != 1 {
		t.Fatal("entry policy triggered a new scan")
	}
}

func TestPlanningTrendBoardFailureIsReportedNotInvented(t *testing.T) {
	p := &planningServices{trendRead: func(context.Context) (market.TrendBoard, error) {
		return market.TrendBoard{}, errors.New("upstream down")
	}}
	s := &Server{planning: p}
	s.planningOnce.Do(func() {})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/planning/ai500", nil)
	s.handlePlanningAI500(c)
	if w.Code == 200 || !strings.Contains(w.Body.String(), "Trend board unavailable") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if limits := p.planningEntryPolicy("binance", "BTCUSDT", "", core.Constraints{}); limits.EntryPolicy != "" {
		t.Fatal("failed board must not restrict entries")
	}
}
