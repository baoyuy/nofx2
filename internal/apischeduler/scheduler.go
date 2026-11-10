// Package apischeduler coordinates outbound REST requests across project modules.
// It never retries requests or caches private, trading, or paid responses.
package apischeduler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

var (
	ErrQueueFull        = errors.New("API scheduler queue is full")
	ErrQueueTimeout     = errors.New("API scheduler budget unavailable before request deadline")
	ErrCostExceedsLimit = errors.New("API scheduler request weight exceeds configured window limit")
)

// Limit is a sliding-window budget. MinSpacing prevents short bursts independently
// of the window. Each request must satisfy every applicable limit atomically.
type Limit struct {
	Key        string
	Capacity   int
	Window     time.Duration
	Weight     int
	MinSpacing time.Duration
}

type requestPolicy struct {
	provider      string
	limits        []Limit
	maxConcurrent int
	priority      int
	signed        bool
	cacheTTL      time.Duration
}

type usage struct {
	at     time.Time
	weight int
}
type window struct {
	events        []usage
	used          int
	last          time.Time
	pausedUntil   time.Time
	observed      int
	observedAt    time.Time
	observedUntil time.Time
}
type waiter struct {
	policy   requestPolicy
	arrived  time.Time
	ctx      context.Context
	ready    chan struct{}
	admitted bool
}

// Stats contains no credentials, account identifiers, URLs, or request payloads.
type Stats struct {
	Provider    string `json:"provider"`
	Requests    int64  `json:"requests"`
	CacheHits   int64  `json:"cache_hits"`
	Coalesced   int64  `json:"coalesced"`
	RateLimited int64  `json:"rate_limited"`
	Rejected    int64  `json:"rejected"`
	InFlight    int    `json:"in_flight"`
	Queued      int    `json:"queued"`
	WaitMillis  int64  `json:"wait_ms"`
}

// Scheduler owns shared budgets and the bounded anonymous public-data cache.
type Scheduler struct {
	cfg        Config
	mu         sync.Mutex
	windows    map[string]*window
	queue      []*waiter
	stats      map[string]*Stats
	changed    chan struct{}
	cacheMu    sync.Mutex
	cache      map[string]cacheEntry
	cacheBytes int
	flights    map[string]*flight
}

func New(cfg Config) (*Scheduler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	profiles := make(map[string]Profile, len(cfg.Profiles))
	for k, v := range cfg.Profiles {
		profiles[k] = v
	}
	cfg.Profiles = profiles
	cfg.Rules = append([]Rule(nil), cfg.Rules...)
	return &Scheduler{cfg: cfg, windows: map[string]*window{}, stats: map[string]*Stats{}, changed: make(chan struct{}), cache: map[string]cacheEntry{}, flights: map[string]*flight{}}, nil
}

func (s *Scheduler) stat(provider string) *Stats {
	st := s.stats[provider]
	if st == nil {
		st = &Stats{Provider: provider}
		s.stats[provider] = st
	}
	return st
}
func (s *Scheduler) signal() { close(s.changed); s.changed = make(chan struct{}) }

// availability must run under mu. It removes expired usage without granting any
// tokens until all simultaneous account, endpoint and IP budgets can be reserved.
func (s *Scheduler) availability(p requestPolicy, now time.Time) (time.Duration, bool) {
	wait := time.Duration(0)
	busy := s.stat(p.provider).InFlight >= p.maxConcurrent
	// Keep one transport slot available for account/order work during market scans.
	if p.priority < 2 && p.maxConcurrent > 1 && s.stat(p.provider).InFlight >= p.maxConcurrent-1 {
		busy = true
	}
	for _, l := range p.limits {
		w := s.windows[l.Key]
		if w == nil {
			continue
		}
		n := 0
		for n < len(w.events) && !w.events[n].at.Add(l.Window).After(now) {
			w.used -= w.events[n].weight
			n++
		}
		if n > 0 {
			w.events = append([]usage(nil), w.events[n:]...)
		}
		if d := w.pausedUntil.Sub(now); d > wait {
			wait = d
		}
		if d := w.last.Add(l.MinSpacing).Sub(now); d > wait {
			wait = d
		}
		if w.used+l.Weight > l.Capacity {
			total := w.used + l.Weight
			for _, ev := range w.events {
				total -= ev.weight
				if total <= l.Capacity {
					if d := ev.at.Add(l.Window).Sub(now); d > wait {
						wait = d
					}
					break
				}
			}
		}
		// Server-reported usage can include callers outside this process. Account for
		// all our reservations since that observation as well, without trusting a
		// later out-of-order response that reports lower usage.
		if now.Before(w.observedUntil) {
			used := w.observed
			for _, ev := range w.events {
				if !ev.at.Before(w.observedAt) {
					used += ev.weight
				}
			}
			if used+l.Weight > l.Capacity {
				if d := w.observedUntil.Sub(now); d > wait {
					wait = d
				}
			}
		}
	}
	return wait, busy
}

func (s *Scheduler) dispatch(now time.Time) time.Duration {
	next := time.Hour
	for {
		best := -1
		for i, w := range s.queue {
			if w.ctx != nil && w.ctx.Err() != nil {
				continue
			}
			d, busy := s.availability(w.policy, now)
			if d > 0 {
				if d < next {
					next = d
				}
				continue
			}
			if busy {
				continue
			}
			if best < 0 || effectivePriority(w, now) > effectivePriority(s.queue[best], now) {
				best = i
			}
		}
		if best < 0 {
			return next
		}
		w := s.queue[best]
		s.queue = append(s.queue[:best], s.queue[best+1:]...)
		for _, l := range w.policy.limits {
			state := s.windows[l.Key]
			if state == nil {
				state = &window{}
				s.windows[l.Key] = state
			}
			state.events = append(state.events, usage{at: now, weight: l.Weight})
			state.used += l.Weight
			state.last = now
		}
		st := s.stat(w.policy.provider)
		st.InFlight++
		st.Queued--
		st.Requests++
		st.WaitMillis += now.Sub(w.arrived).Milliseconds()
		w.admitted = true
		close(w.ready)
	}
}
func effectivePriority(w *waiter, now time.Time) int {
	return w.policy.priority + int(now.Sub(w.arrived)/(5*time.Second))
}

func (s *Scheduler) acquire(ctx context.Context, p requestPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, l := range p.limits {
		if l.Weight > l.Capacity {
			return ErrCostExceedsLimit
		}
	}
	wait := s.cfg.MaxQueueWait
	if p.signed && wait > 2*time.Second {
		wait = 2 * time.Second
	} // never let already-signed timestamps sit in a long queue
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	s.mu.Lock()
	if len(s.queue) >= s.cfg.MaxQueue {
		s.stat(p.provider).Rejected++
		s.mu.Unlock()
		return ErrQueueFull
	}
	w := &waiter{policy: p, arrived: time.Now(), ready: make(chan struct{}), ctx: ctx}
	s.queue = append(s.queue, w)
	s.stat(p.provider).Queued++
	for {
		delay := s.dispatch(time.Now())
		changed := s.changed
		if w.admitted {
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-w.ready:
		case <-changed:
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
		s.mu.Lock()
		if w.admitted {
			s.mu.Unlock()
			return nil
		}
		if err := ctx.Err(); err != nil {
			for i, q := range s.queue {
				if q == w {
					s.queue = append(s.queue[:i], s.queue[i+1:]...)
					break
				}
			}
			s.stat(p.provider).Queued--
			s.stat(p.provider).Rejected++
			s.signal()
			s.mu.Unlock()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
				if parentErr := context.Cause(ctx); parentErr != nil {
					return errors.Join(ErrQueueTimeout, parentErr)
				}
			}
			return err
		}
	}
}
func (s *Scheduler) release(p requestPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stat(p.provider)
	if st.InFlight > 0 {
		st.InFlight--
	}
	s.dispatch(time.Now())
	s.signal()
}

// Snapshot returns a copy of per-provider scheduling counters for diagnostics.
func (s *Scheduler) Snapshot() []Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Stats, 0, len(s.stats))
	for _, v := range s.stats {
		out = append(out, *v)
	}
	return out
}

// Transport retains the original RoundTripper, including proxies, TLS validation
// and any anti-replay settings on a single-use payment transport.
type Transport struct {
	base      http.RoundTripper
	scheduler *Scheduler
}

func (s *Scheduler) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if t, ok := base.(*Transport); ok && t.scheduler == s {
		return base
	}
	return &Transport{base: base, scheduler: s}
}
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("API scheduler requires a request URL")
	}
	p := t.scheduler.policy(req)
	if p.provider == "" {
		return t.base.RoundTrip(req)
	}
	if key := cacheKey(req, t.base, p.cacheTTL); key != "" {
		return t.scheduler.cachedRoundTrip(req, t, key, p)
	}
	return t.roundTrip(req, p)
}
func (t *Transport) roundTrip(req *http.Request, p requestPolicy) (*http.Response, error) {
	if err := t.scheduler.acquire(req.Context(), p); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if err := req.Context().Err(); err != nil {
		t.scheduler.release(p)
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.scheduler.release(p)
		return resp, err
	}
	t.scheduler.feedback(req, p, resp)
	if resp == nil || resp.Body == nil {
		t.scheduler.release(p)
		return resp, nil
	}
	resp.Body = &scheduledBody{ReadCloser: resp.Body, done: func() { t.scheduler.release(p) }}
	return resp, nil
}
func (t *Transport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type scheduledBody struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (b *scheduledBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.done)
	}
	return n, err
}
func (b *scheduledBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.done); return err }
