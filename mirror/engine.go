package mirror

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
	"nofx/trader/types"
)

// Account is an exchange account the engine reads and trades.
type Account = types.Trader

// Deps connect the engine to the traders without importing the manager.
type Deps struct {
	// Leader returns the account of a loaded AI trader.
	Leader func(traderID string) (Account, error)
	// Follower builds the account of a Binance or Binance paper exchange.
	Follower func(userID, exchangeID string) (Account, error)
	// Busy tells why the link's account must not be traded now ("" = free):
	// a running AI trader or another running link uses it.
	Busy func(link store.MirrorLink) string
}

// Timing. A tick reads both accounts; protective orders are read less often.
const (
	tickInterval    = 10 * time.Second
	settleDelay     = 3 * time.Second // after a wake: let the AI set its stops
	protectInterval = 30 * time.Second
	errorRepeat     = 10 * time.Minute
	eventsShown     = 100
)

// Engine runs every running link in its own goroutine.
type Engine struct {
	store *store.MirrorStore
	deps  Deps

	mu      sync.Mutex
	runners map[string]*runner
}

func New(st *store.MirrorStore, deps Deps) *Engine {
	return &Engine{store: st, deps: deps, runners: map[string]*runner{}}
}

// Status is a running link's latest tick.
type Status struct {
	Equity    float64           `json:"equity"`
	Available float64           `json:"available"`
	PnL       float64           `json:"pnl"`
	Error     string            `json:"error,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
	Positions []FollowerHolding `json:"positions"`
}

// FollowerHolding is one copied position on the follower.
type FollowerHolding struct {
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"`
	Qty      float64 `json:"qty"`
	Entry    float64 `json:"entry"`
	Mark     float64 `json:"mark"`
	Leverage int     `json:"leverage"`
	PnL      float64 `json:"pnl"`
}

type runner struct {
	link    string
	wake    chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
	account Account

	mu     sync.Mutex
	status Status
	// errors keeps the last logged error per position, to not repeat it.
	errors map[string]errorSeen
	// protectAt is when the stops were last compared with the AI's.
	protectAt time.Time
	first     bool
}

type errorSeen struct {
	text string
	at   time.Time
}

// Start resumes the links saved as running.
func (e *Engine) Start(ctx context.Context) {
	links, err := e.store.Links("")
	if err != nil {
		logger.Warnf("mirror: load links: %v", err)
		return
	}
	for _, l := range links {
		if l.Running {
			e.run(ctx, l.ID)
		}
	}
	go func() {
		<-ctx.Done()
		e.mu.Lock()
		ids := make([]string, 0, len(e.runners))
		for id := range e.runners {
			ids = append(ids, id)
		}
		e.mu.Unlock()
		for _, id := range ids {
			e.halt(id)
		}
	}()
}

func (e *Engine) run(parent context.Context, linkID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.runners[linkID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r := &runner{link: linkID, wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{}),
		errors: map[string]errorSeen{}, first: true}
	e.runners[linkID] = r
	go e.loop(ctx, r)
}

// halt stops a link's goroutine and waits for it.
func (e *Engine) halt(linkID string) {
	e.mu.Lock()
	r := e.runners[linkID]
	delete(e.runners, linkID)
	e.mu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
	}
}

// Wake runs the links copying traderID soon, after it placed an order.
func (e *Engine) Wake(traderID string) {
	links, err := e.store.Links("")
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range links {
		if r := e.runners[l.ID]; r != nil && l.LeaderID == traderID {
			select {
			case r.wake <- struct{}{}:
			default:
			}
		}
	}
}

// Status returns a running link's latest tick.
func (e *Engine) Status(linkID string) (Status, bool) {
	e.mu.Lock()
	r := e.runners[linkID]
	e.mu.Unlock()
	if r == nil {
		return Status{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.UpdatedAt.IsZero() {
		return Status{}, false // no tick yet
	}
	if r.status.Positions == nil {
		r.status.Positions = []FollowerHolding{}
	}
	return r.status, true
}

func (e *Engine) loop(ctx context.Context, r *runner) {
	defer close(r.done)
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		if e.tick(ctx, r) {
			e.mu.Lock()
			if e.runners[r.link] == r {
				delete(e.runners, r.link)
			}
			e.mu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
			select {
			case <-ctx.Done():
				return
			case <-time.After(settleDelay):
			}
		}
	}
}

func (e *Engine) event(linkID, action, symbol, side string, qty, price float64, detail string) {
	ev := store.MirrorEvent{LinkID: linkID, Time: time.Now().UnixMilli(), Action: action, Symbol: symbol,
		Side: side, Qty: qty, Price: price, Detail: detail}
	if err := e.store.AddEvent(ev); err != nil {
		logger.Warnf("mirror: save event: %v", err)
	}
	if action == "error" {
		logger.Warnf("mirror %s: %s %s %s", linkID, symbol, side, detail)
	} else {
		logger.Infof("mirror %s: %s %s %s qty=%.6f %s", linkID, action, symbol, side, qty, detail)
	}
}

// failOnce logs an error for a position unless it was logged lately.
func (e *Engine) failOnce(r *runner, linkID, symbol, side, text string) {
	k := key(symbol, side)
	r.mu.Lock()
	seen := r.errors[k]
	repeat := seen.text == text && time.Since(seen.at) < errorRepeat
	if !repeat {
		r.errors[k] = errorSeen{text: text, at: time.Now()}
	}
	r.mu.Unlock()
	if !repeat {
		e.event(linkID, "error", symbol, side, 0, 0, text)
	}
}

func (r *runner) setError(err string) {
	r.mu.Lock()
	r.status.Error, r.status.UpdatedAt = err, time.Now()
	r.mu.Unlock()
}

// equityOf reads equity and free margin from Trader.GetBalance.
func equityOf(b map[string]interface{}) (equity, available float64) {
	if v, ok := b["totalEquity"].(float64); ok && v > 0 {
		equity = v
	} else {
		wallet, _ := b["totalWalletBalance"].(float64)
		upnl, _ := b["totalUnrealizedProfit"].(float64)
		equity = wallet + upnl
	}
	available, _ = b["availableBalance"].(float64)
	return equity, available
}

func invalidate(a Account) {
	if c, ok := a.(interface{ InvalidatePositionCache() }); ok {
		c.InvalidatePositionCache()
	}
}

func errText(err error) string {
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.TrimSpace(s)
}

func describe(link store.MirrorLink) string {
	if link.SizeMode == "ratio" {
		return fmt.Sprintf("ratio x%.2f", link.Ratio)
	}
	return fmt.Sprintf("fixed %.2f USDT", link.FixedMargin)
}
