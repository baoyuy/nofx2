package apischeduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f.fn(r) }
func reply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func testScheduler(t *testing.T) *Scheduler {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Profiles["default"] = Profile{10000, 1000, 8, 1}
	cfg.MaxQueueWait = 300 * time.Millisecond
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(t *testing.T, method, url string) *http.Request {
	t.Helper()
	r, e := http.NewRequest(method, url, nil)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for !fn() {
		if time.Now().After(until) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
func testPolicy(provider string, capacity, weight int, window time.Duration) requestPolicy {
	return requestPolicy{provider: provider, maxConcurrent: 4, priority: 2, limits: []Limit{{Key: provider + ":weight", Capacity: capacity, Weight: weight, Window: window}}}
}
func TestWeightedSlidingWindowAndProviderIsolation(t *testing.T) {
	s := testScheduler(t)
	p := testPolicy("one", 3, 2, 60*time.Millisecond)
	if err := s.acquire(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	s.release(p)
	// Another upstream is never blocked by one's exhausted budget.
	if err := s.acquire(context.Background(), testPolicy("two", 1, 1, time.Minute)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := s.acquire(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 45*time.Millisecond {
		t.Fatalf("weighted budget bypassed: %v", elapsed)
	}
	s.release(p)
}
func TestMultipleLimitsReservedAtomically(t *testing.T) {
	s := testScheduler(t)
	p := testPolicy("one", 100, 1, time.Minute)
	p.limits = append(p.limits, Limit{Key: "endpoint", Capacity: 1, Weight: 1, Window: time.Minute})
	if e := s.acquire(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	s.release(p)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if e := s.acquire(ctx, p); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.windows["one:weight"].used != 1 {
		t.Fatal("blocked request partially consumed IP budget")
	}
}
func TestConcurrencyHeldUntilBodyClosedAndCancellation(t *testing.T) {
	s := testScheduler(t)
	cfg := s.cfg
	cfg.Profiles["default"] = Profile{10000, 1000, 1, 1}
	s, _ = New(cfg)
	var calls atomic.Int32
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) { calls.Add(1); return reply(200, "{}"), nil }}
	// Separate HTTP clients use the same scheduler budget.
	a := &http.Client{Transport: s.Transport(base)}
	b := &http.Client{Transport: s.Transport(base)}
	first, e := a.Do(request(t, "GET", "https://example.test/data"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, e = b.Do(request(t, "GET", "https://example.test/data").WithContext(ctx))
	if !errors.Is(e, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("concurrency/cancel: calls=%d err=%v", calls.Load(), e)
	}
	first.Body.Close()
	first.Body.Close()
	r, e := b.Do(request(t, "GET", "https://example.test/data"))
	if e != nil {
		t.Fatal(e)
	}
	io.ReadAll(r.Body)
	r.Body.Close()
	stats := s.Snapshot()
	if stats[0].Queued != 0 || stats[0].InFlight != 0 {
		t.Fatalf("leaked slot: %+v", stats)
	}
}
func TestBoundedQueueAndCancelledWaiterIsNotDispatched(t *testing.T) {
	s := testScheduler(t)
	s.cfg.MaxQueue = 1
	p := testPolicy("one", 100, 1, time.Minute)
	p.maxConcurrent = 1
	if e := s.acquire(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.acquire(ctx, p) }()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.queue) == 1 })
	if e := s.acquire(context.Background(), p); !errors.Is(e, ErrQueueFull) {
		t.Fatal(e)
	}
	cancel()
	s.release(p)
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	stats := s.Snapshot()[0]
	if stats.Requests != 1 || stats.Queued != 0 || stats.InFlight != 0 {
		t.Fatalf("cancelled admission: %+v", stats)
	}
}
func TestPriorityAndExpiredWaiters(t *testing.T) {
	s := testScheduler(t)
	now := time.Now()
	low := testPolicy("one", 2, 1, time.Minute)
	low.maxConcurrent = 1
	low.priority = 0
	high := low
	high.priority = 3
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	a := &waiter{policy: low, arrived: now, ready: make(chan struct{}), ctx: context.Background()}
	b := &waiter{policy: high, arrived: now, ready: make(chan struct{}), ctx: context.Background()}
	dead := &waiter{policy: high, arrived: now, ready: make(chan struct{}), ctx: expired}
	s.mu.Lock()
	s.queue = []*waiter{dead, a, b}
	s.stat("one").Queued = 3
	s.dispatch(now)
	s.mu.Unlock()
	if !b.admitted || a.admitted || dead.admitted {
		t.Fatal("priority/cancellation not respected")
	}
}
func TestRateLimitFeedbackDoesNotRetryOrAffectOtherProvider(t *testing.T) {
	s := testScheduler(t)
	var calls atomic.Int32
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		v := reply(429, `{"error":"limited"}`)
		v.Header.Set("Retry-After", "120")
		return v, nil
	}}
	client := &http.Client{Transport: s.Transport(base)}
	r, e := client.Do(request(t, "POST", "https://api.one.test/orders"))
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if calls.Load() != 1 {
		t.Fatal("scheduler retried a write")
	}
	p := s.policy(request(t, "GET", "https://api.one.test/data"))
	s.mu.Lock()
	d, _ := s.availability(p, time.Now())
	s.mu.Unlock()
	if d < 119*time.Second {
		t.Fatalf("Retry-After not respected: %v", d)
	}
	other := testPolicy("other", 10, 1, time.Minute)
	if e = s.acquire(context.Background(), other); e != nil {
		t.Fatal(e)
	}
	s.release(other)
}
func TestPublicRequestsCoalesceAndReuseFreshCacheAcrossClients(t *testing.T) {
	s := testScheduler(t)
	var calls atomic.Int32
	release := make(chan struct{})
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		select {
		case <-release:
			return reply(200, `{"price":42}`), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}}
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &http.Client{Transport: s.Transport(base)}
			r, e := c.Get("https://fapi.binance.com/fapi/v1/klines?symbol=BTCUSDT&interval=1m&limit=10")
			if e != nil {
				errs <- e
				return
			}
			raw, e := io.ReadAll(r.Body)
			r.Body.Close()
			if e != nil {
				errs <- e
			} else if string(raw) != `{"price":42}` {
				errs <- errors.New("body mismatch")
			}
		}()
	}
	waitFor(t, func() bool {
		for _, st := range s.Snapshot() {
			if st.Coalesced == n-1 {
				return true
			}
		}
		return false
	})
	close(release)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	for i := 0; i < 2; i++ {
		r, e := (&http.Client{Transport: s.Transport(base)}).Get("https://fapi.binance.com/fapi/v1/klines?limit=10&interval=1m&symbol=BTCUSDT")
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 22 callers to use 1 upstream request, got %d", calls.Load())
	}
	stats := s.Snapshot()[0]
	if stats.Requests != 1 || stats.CacheHits != 2 || stats.InFlight != 0 {
		t.Fatalf("bad counters: %+v", stats)
	}
	t.Logf("22 requests across clients -> %d upstream request (19 coalesced, 2 cache hits)", calls.Load())
}
func TestCancelledConsumerDoesNotCancelSharedFetch(t *testing.T) {
	s := testScheduler(t)
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return reply(200, "{}"), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}}
	client := &http.Client{Transport: s.Transport(base)}
	url := "https://fapi.binance.com/fapi/v1/klines?symbol=BTCUSDT"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() {
		r, e := client.Do(request(t, "GET", url).WithContext(ctx))
		if r != nil {
			r.Body.Close()
		}
		first <- e
	}()
	<-started
	go func() {
		r, e := client.Get(url)
		if r != nil {
			r.Body.Close()
		}
		second <- e
	}()
	waitFor(t, func() bool {
		for _, st := range s.Snapshot() {
			if st.Coalesced == 1 {
				return true
			}
		}
		return false
	})
	cancel()
	if e := <-first; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(release)
	if e := <-second; e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("one cancelled consumer caused duplicate requests")
	}
}
func TestPrivatePaidWritesAndExecutionReadsNeverCached(t *testing.T) {
	cases := []struct{ method, url, header string }{
		{"GET", "https://fapi.binance.com/fapi/v1/klines", "X-MBX-APIKEY"},
		{"GET", "https://fapi.binance.com/fapi/v1/klines?signature=private", ""},
		{"POST", "https://fapi.binance.com/fapi/v1/order", "X-MBX-APIKEY"},
		{"GET", "https://claw402.ai/api/v1/nofx/ai500/list", ""},
		{"GET", "https://fapi.binance.com/fapi/v1/depth", ""},
		{"GET", "https://fapi.binance.com/fapi/v1/ticker/price?symbol=BTCUSDT", ""},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			s := testScheduler(t)
			var calls atomic.Int32
			base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) { calls.Add(1); return reply(200, "{}"), nil }}
			c := &http.Client{Transport: s.Transport(base)}
			for i := 0; i < 2; i++ {
				r := request(t, tc.method, tc.url)
				if tc.header != "" {
					r.Header.Set(tc.header, "not-a-real-key")
				}
				resp, e := c.Do(r)
				if e != nil {
					t.Fatal(e)
				}
				resp.Body.Close()
			}
			if calls.Load() != 2 {
				t.Fatalf("unsafe caching: %d", calls.Load())
			}
		})
	}
}
func TestErrorResponseIsNotRetriedOrCached(t *testing.T) {
	s := testScheduler(t)
	var calls atomic.Int32
	base := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return reply(500, `{"error":"upstream"}`), nil
	}}
	c := &http.Client{Transport: s.Transport(base)}
	for i := 0; i < 2; i++ {
		r, e := c.Get("https://fapi.binance.com/fapi/v1/klines?symbol=BTCUSDT")
		if e != nil {
			t.Fatal(e)
		}
		if r.StatusCode != 500 {
			t.Fatal(r.StatusCode)
		}
		r.Body.Close()
	}
	if calls.Load() != 2 {
		t.Fatalf("error retried or cached: %d", calls.Load())
	}
}
