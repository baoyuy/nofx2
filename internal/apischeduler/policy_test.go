package apischeduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func weightOf(p requestPolicy) int {
	for _, l := range p.limits {
		if strings.HasSuffix(l.Key, ":weight") {
			return l.Weight
		}
	}
	return 0
}
func TestBinanceWeightVariantsAndAliasBudget(t *testing.T) {
	s := testScheduler(t)
	cases := []struct {
		path   string
		weight int
	}{{"/fapi/v1/klines?limit=99", 1}, {"/fapi/v1/klines?limit=499", 2}, {"/fapi/v1/klines?limit=500", 5}, {"/fapi/v1/klines?limit=1500", 10}, {"/fapi/v1/ticker/24hr", 40}, {"/fapi/v1/ticker/24hr?symbol=BTCUSDT", 1}, {"/fapi/v1/depth?limit=1000", 20}, {"/fapi/v1/openOrders", 40}}
	for _, tc := range cases {
		p := s.policy(request(t, "GET", "https://fapi.binance.com"+tc.path))
		if weightOf(p) != tc.weight {
			t.Errorf("%s got %d want %d", tc.path, weightOf(p), tc.weight)
		}
	}
	a := s.policy(request(t, "GET", "https://fapi.binance.com/fapi/v1/time"))
	b := s.policy(request(t, "GET", "https://fapi.binance.me/fapi/v1/time"))
	if a.provider != b.provider {
		t.Fatal("aliases bypass IP quota")
	}
}
func TestHyperliquidBodyWeightDoesNotConsumeBody(t *testing.T) {
	s := testScheduler(t)
	for _, tc := range []struct {
		typ    string
		weight int
	}{{"allMids", 2}, {"meta", 20}, {"userRole", 60}, {"candleSnapshot", 120}} {
		raw := `{"type":"` + tc.typ + `"}`
		r, e := http.NewRequest("POST", "https://api.hyperliquid.xyz/info", strings.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		p := s.policy(r)
		got, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if string(got) != raw || weightOf(p) != tc.weight {
			t.Fatalf("body/weight for %s changed: %s %+v", tc.typ, got, p)
		}
	}
}
func TestResponseWeightObservationsAndBybitReset(t *testing.T) {
	s := testScheduler(t)
	r := request(t, "GET", "https://fapi.binance.com/fapi/v1/time")
	p := s.policy(r)
	resp := reply(200, "{}")
	defer resp.Body.Close()
	resp.Header.Set("X-MBX-USED-WEIGHT-1M", "1200")
	s.feedback(r, p, resp)
	s.mu.Lock()
	d, _ := s.availability(p, time.Now())
	s.mu.Unlock()
	if d <= 0 {
		t.Fatal("ignored server IP usage")
	}
	resp.Header.Set("X-MBX-USED-WEIGHT-1M", "1")
	s.feedback(r, p, resp)
	s.mu.Lock()
	d, _ = s.availability(p, time.Now())
	s.mu.Unlock()
	if d <= 0 {
		t.Fatal("out-of-order lower usage reset quota")
	}
	r = request(t, "GET", "https://api.bybit.com/v5/position/list")
	p = s.policy(r)
	resp.Header = http.Header{"X-Bapi-Limit-Status": {"0"}}
	s.feedback(r, p, resp)
	s.mu.Lock()
	d, _ = s.availability(p, time.Now())
	s.mu.Unlock()
	if d < 900*time.Millisecond {
		t.Fatal("ignored Bybit quota exhaustion")
	}
}
func TestCacheExpiryAndProxyIsolation(t *testing.T) {
	s := testScheduler(t)
	calls := 0
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) { calls++; return reply(200, "{}"), nil }}
	c := &http.Client{Transport: s.Transport(base)}
	url := "https://fapi.binance.com/fapi/v1/klines?symbol=ETHUSDT"
	resp, e := c.Get(url)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	s.cacheMu.Lock()
	for key, v := range s.cache {
		v.expires = time.Now().Add(-time.Second)
		s.cache[key] = v
	}
	s.cacheMu.Unlock()
	resp, e = c.Get(url)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if calls != 2 {
		t.Fatal("stale cache reused")
	}
	different := &fakeTransport{fn: base.fn}
	resp, e = (&http.Client{Transport: s.Transport(different)}).Get(url)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if calls != 3 {
		t.Fatal("different transport/proxy bypassed")
	}
}
func TestWrapPreservesTransportTimeoutAndRedirectPolicy(t *testing.T) {
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) { return reply(200, "{}"), nil }}
	redirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	original := &http.Client{Transport: base, Timeout: 3 * time.Second, CheckRedirect: redirect}
	wrapped := Wrap(original)
	twice := Wrap(wrapped)
	if original.Transport != base || wrapped.Timeout != original.Timeout || wrapped.CheckRedirect == nil {
		t.Fatal("original client mutated")
	}
	if wrapped.Transport != twice.Transport {
		t.Fatal("double wrapping charges twice")
	}
	if e := wrapped.CheckRedirect(nil, nil); e != http.ErrUseLastResponse {
		t.Fatal("redirect safety changed")
	}
}
func TestConfigurationValidationAndImmutability(t *testing.T) {
	cfg := DefaultConfig()
	s, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Profiles["default"] = Profile{100, 1, 1, 1}
	if s.cfg.Profiles["default"].WeightPerMinute == 100 {
		t.Fatal("scheduler retains mutable configuration")
	}
	cfg = DefaultConfig()
	cfg.Profiles["default"] = Profile{}
	if _, e = New(cfg); e == nil {
		t.Fatal("invalid budget accepted")
	}
	cfg = DefaultConfig()
	cfg.Rules = []Rule{{Provider: "default", Method: "GET", Path: "/data?api_key=secret", Weight: 1}}
	if _, e = New(cfg); e == nil {
		t.Fatal("query in endpoint rule accepted")
	}
	s = testScheduler(t)
	p := testPolicy("one", 1, 2, time.Minute)
	if e = s.acquire(context.Background(), p); !errors.Is(e, ErrCostExceedsLimit) {
		t.Fatal(e)
	}
}
func TestFailedTransportReleasesConcurrency(t *testing.T) {
	s := testScheduler(t)
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) { return nil, errors.New("network failure") }}
	_, e := (&http.Client{Transport: s.Transport(base)}).Get("https://example.test/data")
	if e == nil {
		t.Fatal("expected failure")
	}
	if s.Snapshot()[0].InFlight != 0 {
		t.Fatal("slot leaked on network failure")
	}
}

func TestCandleWeightUsesRequestedRange(t *testing.T) {
	for _, tc := range []struct {
		bars int
		want int
	}{{5, 21}, {200, 24}, {5000, 104}} {
		body := map[string]any{"req": map[string]any{"interval": "1m", "startTime": float64(1000), "endTime": float64(1000 + tc.bars*60000)}}
		if got := hyperliquidCandleWeight(body); got != tc.want {
			t.Fatalf("%d bars: got %d want %d", tc.bars, got, tc.want)
		}
	}
	if hyperliquidCandleWeight(nil) != 120 {
		t.Fatal("unclassified request undercounted")
	}
}
