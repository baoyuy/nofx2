package api

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"nofx/kernel"
	"nofx/store"
)

type eventRuntimeStub struct {
	running    bool
	symbols    []string
	err        error
	calls      int
	stopOnRead bool
}

func (s *eventRuntimeStub) IsRunning() bool { return s.running }
func (s *eventRuntimeStub) GetCandidateCoins() ([]kernel.CandidateCoin, error) {
	s.calls++
	if s.stopOnRead {
		s.running = false
	}
	coins := []kernel.CandidateCoin{}
	for _, symbol := range s.symbols {
		coins = append(coins, kernel.CandidateCoin{Symbol: symbol})
	}
	return coins, s.err
}
func TestEventWatchesRuntimeScope(t *testing.T) {
	a := &eventRuntimeStub{running: true, symbols: []string{"BTCUSDT", "ETHUSDT"}}
	b := &eventRuntimeStub{running: true, symbols: []string{"BTCUSDT"}}
	stopped := &eventRuntimeStub{running: false, symbols: []string{"SOLUSDT"}}
	failed := &eventRuntimeStub{running: true, err: errors.New("provider failed")}
	runtimes := map[string]*eventRuntimeStub{"a": a, "b": b, "stopped": stopped, "failed": failed, "other-user": {running: true, symbols: []string{"DOGEUSDT"}}}
	records := []*store.Trader{{ID: "a", Name: "Alice A", StrategyID: "sa"}, {ID: "b", Name: "Alice B", StrategyID: "sb"}, {ID: "stopped"}, {ID: "failed"}, {ID: "unloaded"}}
	lookup := func(id string) eventRuntime {
		if r := runtimes[id]; r != nil {
			return r
		}
		return nil
	}
	watches, running, warnings := eventWatches(records, lookup, func(id string) string { return "Strategy " + id })
	if running != 3 || len(warnings) != 1 || len(watches) != 2 || len(watches[0].Traders) != 2 || stopped.calls != 0 || runtimes["other-user"].calls != 0 {
		t.Fatalf("scope %+v running=%d warnings=%v", watches, running, warnings)
	}
	if watches[0].Traders[0].StrategyName != "Strategy sa" {
		t.Fatal(watches)
	}
	// Changing dynamic candidates is reflected, not a saved static list.
	a.symbols = []string{"SUIUSDT"}
	b.running = false
	failed.running = false
	watches, running, _ = eventWatches(records, lookup, func(string) string { return "" })
	if running != 1 || len(watches) != 1 || watches[0].Coin != "SUI" {
		t.Fatal(watches, running)
	}
	a.stopOnRead = true
	watches, running, _ = eventWatches(records, lookup, func(string) string { return "" })
	if running != 0 || len(watches) != 0 {
		t.Fatal("stop during resolution retained", watches, running)
	}
}
func TestEventsRejectMissingUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	s := &Server{}
	router.GET("/events", s.handleEvents)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/events", nil))
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestEventHistoryOptions(t *testing.T) {
	for _, tc := range []struct {
		query                     string
		window, threshold, ending float64
		valid                     bool
	}{
		{"", 60, 5, 24, true},
		{"?window_minutes=15&threshold_pp=1&ending_hours=6", 15, 1, 6, true},
		{"?window_minutes=360&threshold_pp=10&ending_hours=72", 360, 10, 72, true},
		{"?window_minutes=0", 0, 0, 0, false},
		{"?window_minutes=NaN", 0, 0, 0, false},
		{"?threshold_pp=Inf", 0, 0, 0, false},
		{"?threshold_pp=0", 0, 0, 0, false},
		{"?ending_hours=-1", 0, 0, 0, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/events"+tc.query, nil)
		opts, err := eventHistoryOptions(c)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.query, err)
		}
		if tc.valid && (opts.Window.Minutes() != tc.window || opts.ThresholdPP != tc.threshold || opts.EndingWithin.Hours() != tc.ending) {
			t.Fatalf("%s: %+v", tc.query, opts)
		}
	}
}

func TestEventsRejectInvalidHistoryOptionsBeforeReadingStore(t *testing.T) {
	router := gin.New()
	s := &Server{}
	router.GET("/events", func(c *gin.Context) { c.Set("user_id", "alice"); s.handleEvents(c) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/events?threshold_pp=NaN", nil))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}
