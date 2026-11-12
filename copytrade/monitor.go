package copytrade

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
)

const (
	// MaxFollows bounds the followed traders so one refresh round stays a few
	// minutes at the request spacing.
	MaxFollows = 100

	// History is fetched one page at a time back to the retained range;
	// per-page yielding replaces the old hard page cap that skipped fills.
	backfillWindow   = retention
	retention        = 30 * 24 * time.Hour
	hotInterval      = 30 * time.Second
	positionInterval = 20 * time.Second
	coldInterval     = 15 * time.Minute
	// A public trader holding nothing is asked for positions less often, and
	// one who has not traded for dormantAfter less often still: with one
	// request every few seconds for everyone, the budget belongs to the
	// traders who act.
	idlePositionInterval    = 2 * time.Minute
	dormantAfter            = 3 * 24 * time.Hour
	dormantInterval         = time.Hour
	dormantPositionInterval = 10 * time.Minute
	// performanceInterval: 30-day results move slowly, one request a day.
	performanceInterval = 24 * time.Hour
	// highFreqMinDays of stored fills are needed to call a trader high
	// frequency for good; unsyncedAfter of failed backfill to give up on one.
	highFreqMinDays = 2.0
	unsyncedAfter   = 48 * time.Hour
	errorInterval   = 5 * time.Minute
	profileInterval = 6 * time.Hour
	hotWindow       = 2 * time.Hour
	maxEventsKept   = 300
	// profileVersion bumps when the stored profile gains fields, so older
	// profiles are fetched again once (2 = start time and balance, 3 =
	// portfolio type).
	profileVersion = 3
	// highFreqOrders a day (2 an hour, around the clock) marks a high
	// frequency trader; their votes count highFreqWeight.
	highFreqOrders = 48
	highFreqWeight = 0.4
)

// Snapshot is the rebuilt view of one followed trader.
type Snapshot struct {
	Follow    store.CopyTradeFollow `json:"trader"`
	Positions []Position            `json:"positions"`
	// Events are newest first.
	Events []Event `json:"events"`
	// PositionsSource: "public" when Binance shows the positions, else
	// "inferred" from fills since monitoring started.
	PositionsSource    string      `json:"positions_source"`
	UpdatedAt          time.Time   `json:"updated_at"`
	PositionsUpdatedAt time.Time   `json:"positions_updated_at"`
	FillsUpdatedAt     time.Time   `json:"fills_updated_at"`
	ProfileUpdatedAt   time.Time   `json:"profile_updated_at"`
	HistoryComplete    bool        `json:"history_complete"`
	PositionsError     string      `json:"positions_error,omitempty"`
	FillsError         string      `json:"fills_error,omitempty"`
	ProfileError       string      `json:"profile_error,omitempty"`
	Error              string      `json:"error,omitempty"`
	Hot                bool        `json:"hot"`
	Score              TraderScore `json:"score"`
	// Style is how they trade: holding time and whether they average down.
	Style Style `json:"style"`
	// OrdersPerDay counts actions (merged orders) in the last 24 hours;
	// HighFreq marks bots and grid traders whose positions churn too fast to
	// say much about direction.
	OrdersPerDay int  `json:"orders_per_day"`
	HighFreq     bool `json:"high_freq"`
	// OrdersPerDayAvg is the same count averaged over the stored fills of up
	// to 7 days (FillDays of them): one busy day is not a habit.
	OrdersPerDayAvg float64 `json:"orders_per_day_avg"`
	FillDays        float64 `json:"fill_days"`
	// Review lists the auto-follow drop rules the trader breaks (see
	// DropReasons). Manual follows are only flagged, never dropped.
	Review []string `json:"review,omitempty"`
	// Probation: picked by auto-follow within the last 7 days; votes count half.
	Probation bool `json:"probation,omitempty"`
	// Accuracy: how often the trader's own opens and adds moved their way
	// within 24h, over the stored 30 days. Scored every few hours.
	Accuracy Accuracy `json:"accuracy"`
	// AccuracyBy is the same record per market class (see ClassOf).
	AccuracyBy map[string]Accuracy `json:"accuracy_by,omitempty"`

	// Independent deadlines keep history backfills from blocking live work.
	nextDue     time.Time // pause / initial stagger
	positionDue time.Time
	fillsDue    time.Time
	profileDue  time.Time
	live        []LivePosition
	filling     *fillSync
	// pendingSince is when the running catch-up of the fills began (zero when
	// the history is complete).
	pendingSince time.Time
	// calls are the opens and adds of the stored fills, oldest first.
	calls []call
}

// Monitor polls followed traders in the background.
type Monitor struct {
	Client *Client
	// Bars prices the hourly confidence signals; nil disables them.
	Bars BarsFunc
	// Marks prices held positions; nil leaves inferred positions unpriced.
	Marks MarksFunc
	store *store.CopyTradeStore

	markMu sync.RWMutex
	marks  map[string]float64

	mu    sync.RWMutex
	snaps map[string]*Snapshot
	wake  chan struct{}

	boardMu sync.Mutex
	boards  map[string]cachedBoard

	loadOnce sync.Once

	auto autoManager

	accMu      sync.RWMutex
	accuracy   map[string]Accuracy
	accuracyBy map[string]map[string]Accuracy

	hitMu    sync.Mutex
	hitCache []HitRate
	hitAt    time.Time
}

type cachedBoard struct {
	at   time.Time
	page LeaderPage
}

func NewMonitor(st *store.CopyTradeStore) *Monitor {
	return &Monitor{Client: NewClient(), store: st, snaps: map[string]*Snapshot{},
		wake: make(chan struct{}, 1), boards: map[string]cachedBoard{}}
}

// Polling performs one small task at a time, sharing the serial request gate.
// Load rebuilds every followed trader from stored fills, so readers have
// data before the first poll. Restore only the durable full-sync timestamp,
// never the follow row's update time (which also changes for partial writes).
func (m *Monitor) Load() {
	m.loadOnce.Do(func() {
		follows, err := m.store.ListFollows()
		if err != nil {
			logger.Warnf("copy-trade: load follows: %v", err)
		}
		follows = m.dropAliases(follows)
		now := time.Now()
		for i, f := range follows {
			snap := &Snapshot{Follow: f, nextDue: now.Add(time.Duration(i) * 2 * time.Second)}
			m.rebuild(snap, nil)
			if f.FillsSyncedAt > 0 {
				snap.FillsUpdatedAt = time.UnixMilli(f.FillsSyncedAt)
				snap.HistoryComplete = !f.FillsPending
				if !f.PositionShow && !f.FillsPending {
					snap.PositionsUpdatedAt = snap.FillsUpdatedAt
				}
				snap.UpdatedAt = snap.FillsUpdatedAt
			}
			if !snap.HistoryComplete {
				// The catch-up starts over with this process.
				snap.pendingSince = now
			}
			if f.ProfileAt > 0 {
				snap.ProfileUpdatedAt = time.UnixMilli(f.ProfileAt)
			}
			m.mu.Lock()
			m.snaps[f.PortfolioID] = snap
			m.mu.Unlock()
		}
	})
}

// dropAliases unfollows signal and private portfolios whose public portfolio
// is followed too. Both are one trader: the alias is hidden from every view
// (see distinctPortfolios), so it could not be unfollowed by hand while it
// kept using requests and a place among the follows.
func (m *Monitor) dropAliases(follows []store.CopyTradeFollow) []store.CopyTradeFollow {
	ids := make(map[string]bool, len(follows))
	for _, f := range follows {
		ids[f.PortfolioID] = true
	}
	// The user's own follow must not become one auto-follow may drop.
	manual := map[string]bool{}
	for _, f := range follows {
		if f.Source == "manual" && f.PublicID != "" && f.PublicID != f.PortfolioID && ids[f.PublicID] {
			manual[f.PublicID] = true
		}
	}
	for i := range follows {
		if f := &follows[i]; manual[f.PortfolioID] && f.Source != "manual" {
			f.Source, f.PickedAt = "manual", 0
			if err := m.store.SetFollowSource(f.PortfolioID, "manual"); err != nil {
				logger.Warnf("copy-trade: keep %s as a manual follow: %v", f.PortfolioID, err)
				delete(ids, f.PortfolioID) // then leave its alias alone
			}
		}
	}
	kept := follows[:0:0]
	for _, f := range follows {
		if f.PublicID != "" && f.PublicID != f.PortfolioID && ids[f.PublicID] {
			if err := m.store.DeleteFollow(f.PortfolioID); err != nil {
				logger.Warnf("copy-trade: unfollow alias %s: %v", f.PortfolioID, err)
				kept = append(kept, f)
				continue
			}
			logger.Infof("copy-trade: unfollowed %s (%s): its public portfolio %s is followed", f.Nickname, f.PortfolioID, f.PublicID)
			continue
		}
		kept = append(kept, f)
	}
	return kept
}

// mergeAliases is dropAliases for the running monitor: a profile refresh can
// reveal that a follow is the alias of another one.
func (m *Monitor) mergeAliases() {
	m.mu.RLock()
	var aliases []string
	for id, s := range m.snaps {
		// Manual ones wait for the next start, where the user's choice is
		// carried over to the public portfolio first.
		if p := s.Follow.PublicID; p != "" && p != id && s.Follow.Source != "manual" {
			if _, ok := m.snaps[p]; ok {
				aliases = append(aliases, id)
			}
		}
	}
	m.mu.RUnlock()
	for _, id := range aliases {
		if err := m.Unfollow(id); err != nil {
			logger.Warnf("copy-trade: unfollow alias %s: %v", id, err)
			continue
		}
		logger.Infof("copy-trade: unfollowed alias portfolio %s: its public portfolio is followed", id)
	}
}

func (m *Monitor) Run(ctx context.Context) {
	m.Load()
	go m.signalLoop(ctx)
	go m.markLoop(ctx)
	go m.autoLoop(ctx)
	go m.accuracyLoop(ctx)
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		id, due := m.nextDue()
		wait := time.Until(due)
		if id == "" {
			wait = time.Hour
		}
		timer := time.NewTimer(max(wait, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.wake:
			timer.Stop()
			continue
		case <-prune.C:
			timer.Stop()
			if err := m.store.PruneFills(time.Now().Add(-retention).UnixMilli()); err != nil {
				logger.Warnf("copy-trade: prune fills: %v", err)
			}
			m.mergeAliases()
			continue
		case <-timer.C:
		}
		if id != "" {
			m.refresh(ctx, id)
		}
	}
}

func (m *Monitor) nextDue() (string, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var id string
	var due time.Time
	for k, s := range m.snaps {
		_, at := s.task()
		if id == "" || at.Before(due) || (at.Equal(due) && k < id) {
			id, due = k, at
		}
	}
	return id, due
}

func (m *Monitor) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Monitor) syncProfile(ctx context.Context, f *store.CopyTradeFollow) error {
	if time.Since(time.UnixMilli(f.ProfileAt)) < profileInterval && f.ProfileVersion >= profileVersion {
		return nil
	}
	d, err := m.Client.Detail(ctx, f.PortfolioID)
	if errors.Is(err, ErrClosed) {
		// Keep what is known of the trader; only the status changes. The
		// auto-follow manager drops it on its next run.
		f.Status = StatusClosed
		f.ProfileAt, f.ProfileVersion = time.Now().UnixMilli(), profileVersion
		return m.store.UpdateFollowProfile(f)
	}
	if err != nil {
		return err
	}
	if d.PortfolioID == "" && d.Nickname == "" {
		// Binance answers an unknown or removed ID with no data: keep the
		// stored profile instead of blanking it.
		return ErrNoProfile
	}
	applyDetail(f, d)
	if time.Since(time.UnixMilli(f.PerfAt)) >= performanceInterval || f.ProfileVersion < profileVersion {
		perf, err := m.Client.Performance(ctx, f.PortfolioID, "30D")
		if err != nil {
			return err
		}
		f.ROI, f.PnL, f.MDD, f.WinRate = float64(perf.ROI), float64(perf.PnL), float64(perf.MDD), float64(perf.WinRate)
		f.PerfAt = time.Now().UnixMilli()
	}
	f.ProfileAt, f.ProfileVersion = time.Now().UnixMilli(), profileVersion
	return m.store.UpdateFollowProfile(f)
}

func applyDetail(f *store.CopyTradeFollow, d Detail) {
	f.Nickname, f.AvatarURL, f.Status, f.PositionShow = d.Nickname, d.AvatarURL, d.Status, d.PositionShow
	f.AUM, f.CopyCount, f.MaxCopyCount = float64(d.AUM), d.CopyCount, d.MaxCopyCount
	f.MarginBalance, f.StartTime = float64(d.MarginBalance), d.StartTime
	f.Private, f.PublicID = d.Private(), d.PublicID
	if f.PublicID == f.PortfolioID {
		f.PublicID = ""
	}
	if d.LastTradeTime > 0 {
		f.LastTradeTime = d.LastTradeTime
	}
}

// rebuild recomputes positions and events from stored fills. Caller holds
// m.mu for snapshots already in m.snaps.
func (m *Monitor) rebuild(s *Snapshot, live []LivePosition) {
	stored, err := m.store.Fills(s.Follow.PortfolioID, time.Now().Add(-retention).UnixMilli())
	if err != nil {
		s.Error = err.Error()
		return
	}
	fills := make([]Fill, len(stored))
	for i, f := range stored {
		fills[i] = Fill{Time: f.Time, Seq: f.Seq, Symbol: f.Symbol, Side: f.Side, PositionSide: f.PositionSide,
			Qty: f.Qty, Price: f.Price, Fee: f.Fee, RealizedPnL: f.RealizedPnL}
	}
	public := s.Follow.PositionShow && live != nil
	positions, events := Replay(fills)
	if public {
		// Binance shows a bigger position than the fills explain: it was
		// opened before the stored fills, so replay again knowing that.
		if pre := preOpenedBooks(live, positions); len(pre) > 0 {
			positions, events = ReplayKnown(fills, pre)
		}
	}
	s.PositionsSource = "inferred"
	if public {
		positions, s.PositionsSource = LivePositions(live, positions), "public"
	}
	// All 30 days of calls, before the event list is cut for display.
	s.calls = callsOf(events)
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	now := time.Now()
	s.OrdersPerDay = ordersSince(events, now.Add(-24*time.Hour).UnixMilli())
	s.HighFreq = s.OrdersPerDay >= highFreqOrders
	s.FillDays, s.OrdersPerDayAvg = 0, 0
	if len(fills) > 0 {
		// Stored fills are oldest first.
		s.FillDays = now.Sub(time.UnixMilli(fills[0].Time)).Hours() / 24
		days := math.Min(7, math.Max(1, s.FillDays))
		s.OrdersPerDayAvg = float64(ordersSince(events, now.Add(-time.Duration(days*24*float64(time.Hour))).UnixMilli())) / days
	}
	if len(events) > maxEventsKept {
		events = events[:maxEventsKept]
	}
	s.Positions, s.Events = positions, events
	s.Hot = len(positions) > 0 || time.Since(time.UnixMilli(s.Follow.LastTradeTime)) < hotWindow
	s.Style = StyleOf(fills)
	if s.Follow.Private {
		// Nothing to poll, and no fills to classify.
		s.Style, s.Hot = Style{Kind: StyleUnknown}, false
	}
	if Closed(s.Follow) {
		// What it held is gone with the portfolio.
		s.Positions, s.Hot = nil, false
	}
}

// ordersSince counts newest-first events at or after ms.
func ordersSince(events []Event, ms int64) int {
	n := 0
	for _, e := range events {
		if e.Time < ms {
			break
		}
		n++
	}
	return n
}

// Follow adds a trader after checking it exists. Its history is fetched in
// the background.
func (m *Monitor) Follow(ctx context.Context, id string) (*store.CopyTradeFollow, error) {
	if err := m.canFollow(id); err != nil {
		return nil, err
	}
	d, err := m.Client.Detail(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.PortfolioID == "" && d.Nickname == "" {
		return nil, errors.New("no lead trader with this ID")
	}
	if d.Private() || d.Signal() {
		// Its trades are hidden; the same trader's public portfolio shows them.
		if d.PublicID == "" || d.PublicID == id {
			return nil, ErrPrivate
		}
		id = d.PublicID
		if err := m.canFollow(id); err != nil {
			return nil, err
		}
		if d, err = m.Client.Detail(ctx, id); err != nil {
			return nil, err
		}
		if d.Private() || d.Signal() {
			return nil, ErrPrivate
		}
	}
	f := store.CopyTradeFollow{PortfolioID: id, Source: "manual"}
	applyDetail(&f, d)
	if err := m.addFollow(f); err != nil {
		return nil, err
	}
	// Following by hand lifts an earlier drop or unfollow.
	if err := m.store.Unexclude(id); err != nil {
		logger.Warnf("copy-trade: clear exclusion of %s: %v", id, err)
	}
	return &f, nil
}

// addFollow saves a follow and schedules its first poll.
func (m *Monitor) addFollow(f store.CopyTradeFollow) error {
	if err := m.canFollow(f.PortfolioID); err != nil {
		return err
	}
	if err := m.store.SaveFollow(&f); err != nil {
		return err
	}
	m.mu.Lock()
	m.snaps[f.PortfolioID] = &Snapshot{Follow: f, PositionsSource: "inferred", nextDue: time.Now(), pendingSince: time.Now()}
	m.mu.Unlock()
	m.poke()
	return nil
}

// StatusClosed is the stored status of a portfolio its trader closed.
const StatusClosed = "CLOSED"

// Closed tells a follow whose portfolio no longer leads.
func Closed(f store.CopyTradeFollow) bool {
	return f.Status != "" && !strings.EqualFold(f.Status, "ACTIVE")
}

// NoData tells a follow that can never show trades or positions: a private
// portfolio, or one that was closed. It cannot be stale either.
func NoData(f store.CopyTradeFollow) bool { return f.Private || Closed(f) }

// ErrNoProfile: Binance returned no profile for a followed portfolio.
var ErrNoProfile = errors.New("Binance returned no profile for this lead trader")

// ErrPrivate refuses a private portfolio with no public one to follow.
var ErrPrivate = errors.New("this is a private copy-trading portfolio: Binance hides its trades and positions, and the trader has no public portfolio to follow instead")

func (m *Monitor) canFollow(id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.snaps[id]; exists {
		return errors.New("already following this trader")
	}
	if len(m.snaps) >= MaxFollows {
		return fmt.Errorf("you can follow at most %d traders", MaxFollows)
	}
	return nil
}

func (m *Monitor) Unfollow(id string) error { return m.unfollow(id, false) }

// unfollow removes a follow. keepFills leaves its fills until they age out of
// the retained range: what a dropped trader did stays measurable, so the
// record is not only of those who survived.
func (m *Monitor) unfollow(id string, keepFills bool) error {
	del := m.store.DeleteFollow
	if keepFills {
		del = m.store.DeleteFollowKeepFills
	}
	if err := del(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.snaps, id)
	m.mu.Unlock()
	m.poke()
	return nil
}

// Snapshots returns copies of every followed trader's view, newest follow last.
func (m *Monitor) Snapshots() []Snapshot {
	m.mu.RLock()
	out := make([]Snapshot, 0, len(m.snaps))
	now := time.Now()
	marks := m.currentMarks()
	peers := m.peersLocked()
	for _, s := range m.snaps {
		out = append(out, m.view(s, marks, now, peers))
	}
	m.mu.RUnlock()
	sort.Slice(out, func(a, b int) bool { return out[a].Follow.CreatedAt.Before(out[b].Follow.CreatedAt) })
	return distinctPortfolios(out)
}

func (m *Monitor) Snapshot(id string) (Snapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.snaps[id]
	if !ok {
		return Snapshot{}, false
	}
	return m.view(s, m.currentMarks(), time.Now(), m.peersLocked()), true
}

// peersLocked is the drop context of everyone followed. Caller holds m.mu.
func (m *Monitor) peersLocked() DropContext {
	follows := make([]store.CopyTradeFollow, 0, len(m.snaps))
	for _, s := range m.snaps {
		follows = append(follows, s.Follow)
	}
	return dropContextOf(follows)
}

// view copies a snapshot with its score, position health and review.
func (m *Monitor) view(s *Snapshot, marks map[string]float64, now time.Time, peers DropContext) Snapshot {
	snap := *s
	snap.Score = ScoreTrader(snap.Follow, now)
	positions := s.Positions
	if !s.HistoryComplete {
		// A partial first backfill can miss closes and invent held positions.
		// Public positions are independently observed and remain usable.
		positions = make([]Position, 0, len(s.Positions))
		for _, p := range s.Positions {
			if p.Source == "public" {
				positions = append(positions, p)
			}
		}
	}
	snap.Positions = assessHealth(positions, marks, snap.Follow.MarginBalance, now)
	snap.Accuracy, snap.AccuracyBy = m.accuracyOf(s.Follow.PortfolioID)
	snap.Review = DropReasonsIn(snap, now, peers)
	snap.Probation = onProbation(snap.Follow, now)
	return snap
}

// Leaderboard serves Binance's leaderboard, cached for 10 minutes per query.
func (m *Monitor) Leaderboard(ctx context.Context, q LeaderQuery) (LeaderPage, error) {
	key := fmt.Sprintf("%s|%s|%d|%s", q.TimeRange, q.SortBy, q.Page, q.Nickname)
	m.boardMu.Lock()
	hit, ok := m.boards[key]
	m.boardMu.Unlock()
	if ok && time.Since(hit.at) < 10*time.Minute {
		return hit.page, nil
	}
	page, err := m.Client.Leaderboard(ctx, q)
	if err != nil {
		return LeaderPage{}, err
	}
	m.boardMu.Lock()
	if len(m.boards) > 200 {
		m.boards = map[string]cachedBoard{}
	}
	m.boards[key] = cachedBoard{at: time.Now(), page: page}
	m.boardMu.Unlock()
	return page, nil
}
