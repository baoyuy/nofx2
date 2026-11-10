package copytrade

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
)

// AutoConfig is the auto-follow manager's setting: keep Target lead traders
// followed, picking good ones from Binance's leaderboard and dropping weak
// ones. Manual follows count towards Target but are never dropped.
type AutoConfig struct {
	Enabled bool `json:"enabled"`
	Target  int  `json:"target"`
}

const (
	DefaultAutoTarget = 50
	MinAutoTarget     = 5

	autoInterval = time.Hour
	// A new pick votes probationWeight and keeps all but the hard rules off
	// until it has been watched for probation.
	probation       = 7 * 24 * time.Hour
	probationWeight = 0.5
	// maxAddsPerRun bounds the backfills started at once: each takes up to a
	// few dozen requests at one per 4 s, shared with live polling.
	maxAddsPerRun = 10
	// maxChecksPerRun bounds the profile requests spent on candidates.
	maxChecksPerRun = 20
	// exclusionDays keeps a dropped trader from being picked again soon.
	exclusionDays = 30

	// Entry bar, from the leaderboard and the profile.
	pickMinDays    = 60
	pickMaxMDD     = 30.0
	pickMinBalance = 5000.0
	// Drop bar. Looser than the entry bar so a trader near a limit is not
	// followed and dropped over and over.
	dropMaxMDD       = 40.0
	dropMinBalance   = 1000.0
	dropInactiveDays = 14
	// staleAge: a trapped position held this long is a loss being sat out.
	staleAge        = 7 * 24 * time.Hour
	staleMinTrapped = 2
)

// Drop rule codes, shown to the user and kept in the log.
const (
	RuleClosed       = "closed"        // stopped leading
	RulePrivate      = "private"       // trades hidden
	RuleMartingale   = "martingale"    // averages into losses
	RuleROINegative  = "roi_negative"  // 30-day ROI below 0
	RuleMDDHigh      = "mdd_high"      // 30-day drawdown above dropMaxMDD
	RuleBalanceLow   = "balance_low"   // balance below dropMinBalance
	RuleStaleTrapped = "stale_trapped" // over half the book is old trapped positions
	RuleInactive     = "inactive"      // no trade and nothing held for 2 weeks
)

// Entry-bar rejections of a candidate, logged as "skip" with the value.
const (
	RejectBalance = "pick_balance" // balance below pickMinBalance
	Reject30D     = "pick_roi30"   // last 30 days lost or drew down too much
)

// onProbation tells a pick the manager made less than probation ago.
func onProbation(f store.CopyTradeFollow, now time.Time) bool {
	return f.Source != "manual" && f.PickedAt > 0 && now.Sub(time.UnixMilli(f.PickedAt)) < probation
}

// DropReasons lists the rules a followed trader breaks, from a view (scored,
// positions priced). Only closed and private apply on probation, and the rest
// wait for a complete picture of the trader.
func DropReasons(s Snapshot, now time.Time) []string {
	f := s.Follow
	var out []string
	if f.Status != "" && !strings.EqualFold(f.Status, "ACTIVE") {
		out = append(out, RuleClosed)
	}
	if f.Private {
		out = append(out, RulePrivate)
	}
	if onProbation(f, now) || f.ProfileAt == 0 || !s.HistoryComplete {
		return out
	}
	if s.Style.Martingale() {
		out = append(out, RuleMartingale)
	}
	if s.Accuracy.inaccurate() {
		out = append(out, RuleInaccurate)
	}
	if f.ROI < 0 {
		out = append(out, RuleROINegative)
	}
	if f.MDD > dropMaxMDD {
		out = append(out, RuleMDDHigh)
	}
	if f.MarginBalance > 0 && f.MarginBalance < dropMinBalance {
		out = append(out, RuleBalanceLow)
	}
	priced, stale := 0, 0
	for _, p := range s.Positions {
		if !p.Priced || p.Liquidated {
			continue
		}
		priced++
		old := p.PreExisting || p.OpenedAt <= 0 || now.Sub(time.UnixMilli(p.OpenedAt)) >= staleAge
		if p.Trapped && old {
			stale++
		}
	}
	if stale >= staleMinTrapped && stale*2 > priced {
		out = append(out, RuleStaleTrapped)
	}
	if len(s.Positions) == 0 && f.LastTradeTime > 0 &&
		now.Sub(time.UnixMilli(f.LastTradeTime)) > dropInactiveDays*24*time.Hour {
		out = append(out, RuleInactive)
	}
	return out
}

// planned is one follow the manager will remove.
type planned struct {
	id, nickname, reason string
}

// autoPlan is what a run removes before picking: drops break a rule and are
// kept out for exclusionDays; trims only bring the count down to the target.
type autoPlan struct {
	drops, trims []planned
}

// planRemovals decides the removals of one run. Manual follows are never
// removed. Rule breakers go first, worst score first, at most dropBudget of
// them unless the count is over target; then the weakest picks go until the
// count is down to target, picks still on probation last.
func planRemovals(views []Snapshot, target, dropBudget int, now time.Time) autoPlan {
	var plan autoPlan
	type ranked struct {
		s       Snapshot
		reasons []string
	}
	var breakers, rest []ranked
	for _, s := range views {
		if s.Follow.Source == "manual" {
			continue
		}
		r := ranked{s: s, reasons: DropReasons(s, now)}
		if len(r.reasons) > 0 {
			breakers = append(breakers, r)
		} else {
			rest = append(rest, r)
		}
	}
	byScore := func(list []ranked) {
		sort.SliceStable(list, func(a, b int) bool {
			pa, pb := onProbation(list[a].s.Follow, now), onProbation(list[b].s.Follow, now)
			if pa != pb {
				return !pa
			}
			return list[a].s.Score.Score < list[b].s.Score.Score
		})
	}
	byScore(breakers)
	byScore(rest)
	over := len(views) - target
	n := min(len(breakers), max(dropBudget, over))
	for _, r := range breakers[:n] {
		plan.drops = append(plan.drops, planned{r.s.Follow.PortfolioID, r.s.Follow.Nickname, strings.Join(r.reasons, ",")})
	}
	// Breakers past the budget stay for now; trims take the clean ones.
	left := append(append([]ranked(nil), breakers[n:]...), rest...)
	for _, r := range left {
		if len(views)-len(plan.drops)-len(plan.trims) <= target {
			break
		}
		plan.trims = append(plan.trims, planned{r.s.Follow.PortfolioID, r.s.Follow.Nickname, "over_target"})
	}
	return plan
}

// pickable keeps the leaderboard rows that pass the entry bar and are not in
// skip, best score first. Rows repeat across boards; the first one counts.
func pickable(rows []Leader, skip map[string]bool, now time.Time) []Leader {
	seen := map[string]bool{}
	var out []Leader
	scores := map[string]float64{}
	for _, r := range rows {
		id := r.PortfolioID
		if id == "" || seen[id] || skip[id] {
			continue
		}
		seen[id] = true
		if r.StartTime <= 0 || now.Sub(time.UnixMilli(r.StartTime)) < pickMinDays*24*time.Hour {
			continue
		}
		if r.ROI <= 0 || float64(r.MDD) > pickMaxMDD || r.CopierPnL <= 0 {
			continue
		}
		scores[id] = ScoreTrader(store.CopyTradeFollow{ROI: float64(r.ROI), MDD: float64(r.MDD),
			WinRate: float64(r.WinRate), AUM: float64(r.AUM), CopyCount: r.CopyCount,
			StartTime: r.StartTime, PositionShow: true}, now).Score
		out = append(out, r)
	}
	sort.SliceStable(out, func(a, b int) bool { return scores[out[a].PortfolioID] > scores[out[b].PortfolioID] })
	return out
}

// pickReject tells why a profile fails the entry bar, or "".
func pickReject(d Detail) string {
	// Hidden positions are fine: they are inferred from the public fills.
	switch {
	case d.Private():
		return RulePrivate
	case d.Status != "" && !strings.EqualFold(d.Status, "ACTIVE"):
		return RuleClosed
	case float64(d.MarginBalance) < pickMinBalance:
		return fmt.Sprintf("%s=%.0f", RejectBalance, float64(d.MarginBalance))
	}
	return ""
}

// AutoState is the manager's latest run, for the page.
type AutoState struct {
	Config  AutoConfig `json:"config"`
	LastRun time.Time  `json:"last_run"`
	Error   string     `json:"error,omitempty"`
	// Counts of the latest run.
	Added   int `json:"added"`
	Dropped int `json:"dropped"`
	Trimmed int `json:"trimmed"`
	Checked int `json:"checked"`
	// Candidates passed the leaderboard bar in the latest run.
	Candidates int  `json:"candidates"`
	Running    bool `json:"running"`
}

type autoManager struct {
	mu    sync.Mutex
	state AutoState
	wake  chan struct{}
	// rejected keeps candidates that failed the profile check, until the time.
	rejected map[string]time.Time
	run      sync.Mutex // one run at a time
}

func (a *autoManager) init() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wake == nil {
		a.wake = make(chan struct{}, 1)
		a.rejected = map[string]time.Time{}
	}
}

// SetAuto applies a setting and runs the manager soon when it is on.
func (m *Monitor) SetAuto(c AutoConfig) {
	m.auto.init()
	c.Target = min(max(c.Target, MinAutoTarget), MaxFollows)
	m.auto.mu.Lock()
	m.auto.state.Config = c
	m.auto.mu.Unlock()
	if c.Enabled {
		select {
		case m.auto.wake <- struct{}{}:
		default:
		}
	}
}

func (m *Monitor) AutoState() AutoState {
	m.auto.mu.Lock()
	defer m.auto.mu.Unlock()
	return m.auto.state
}

func (m *Monitor) autoLoop(ctx context.Context) {
	m.auto.init()
	// A setting loaded at startup must not run before the first polls.
	select {
	case <-m.auto.wake:
	default:
	}
	// Let the first polls fill the snapshots before judging anyone.
	first := time.NewTimer(5 * time.Minute)
	defer first.Stop()
	t := time.NewTicker(autoInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		case <-m.auto.wake:
		}
		m.RunAuto(ctx, time.Now())
	}
}

// RunAuto runs the manager once: drop and trim, then pick. It does nothing
// while auto-follow is off.
func (m *Monitor) RunAuto(ctx context.Context, now time.Time) {
	m.auto.init()
	m.auto.run.Lock()
	defer m.auto.run.Unlock()
	cfg := m.AutoState().Config
	if !cfg.Enabled {
		return
	}
	st := AutoState{Config: cfg, LastRun: now, Running: true}
	m.setAutoState(st)
	err := m.runAuto(ctx, cfg, now, &st)
	st.Running = false
	if err != nil {
		st.Error = err.Error()
		logger.Warnf("copy-trade auto-follow: %v", err)
	}
	m.setAutoState(st)
}

func (m *Monitor) setAutoState(st AutoState) {
	m.auto.mu.Lock()
	defer m.auto.mu.Unlock()
	st.Config = m.auto.state.Config // a save during the run wins
	m.auto.state = st
}
func (m *Monitor) runAuto(ctx context.Context, cfg AutoConfig, now time.Time, st *AutoState) error {
	nowMs := now.UnixMilli()
	logs, err := m.store.AutoLogs(now.Add(-24*time.Hour).UnixMilli(), 500)
	if err != nil {
		return err
	}
	budget := max(1, cfg.Target/10)
	for _, l := range logs {
		if l.Action == "drop" {
			budget--
		}
	}
	plan := planRemovals(m.Snapshots(), cfg.Target, max(budget, 0), now)
	remove := func(p planned, action string) error {
		if err := m.Unfollow(p.id); err != nil {
			return err
		}
		if action == "drop" {
			if err := m.store.Exclude(store.CopyTradeExclusion{PortfolioID: p.id, Nickname: p.nickname, Reason: p.reason,
				Until: now.Add(exclusionDays * 24 * time.Hour).UnixMilli(), CreatedAt: nowMs}); err != nil {
				return err
			}
		}
		logger.Infof("copy-trade auto-follow: %s %s (%s)", action, p.nickname, p.reason)
		return m.store.AddAutoLog(store.CopyTradeAutoLog{Time: nowMs, Action: action, PortfolioID: p.id,
			Nickname: p.nickname, Reason: p.reason})
	}
	for _, p := range plan.drops {
		if err := remove(p, "drop"); err != nil {
			return err
		}
		st.Dropped++
	}
	for _, p := range plan.trims {
		if err := remove(p, "trim"); err != nil {
			return err
		}
		st.Trimmed++
	}
	return m.fillAuto(ctx, cfg, now, st)
}

// fillAuto follows leaderboard picks until the target is reached, at most
// maxAddsPerRun of them, checking each profile first.
func (m *Monitor) fillAuto(ctx context.Context, cfg AutoConfig, now time.Time, st *AutoState) error {
	m.mu.RLock()
	need := cfg.Target - len(m.snaps)
	skip := make(map[string]bool, len(m.snaps))
	for id := range m.snaps {
		skip[id] = true
	}
	m.mu.RUnlock()
	need = min(need, maxAddsPerRun)
	if need <= 0 {
		return nil
	}
	ex, err := m.store.Exclusions(now.UnixMilli())
	if err != nil {
		return err
	}
	for _, e := range ex {
		skip[e.PortfolioID] = true
	}
	m.auto.mu.Lock()
	for id, until := range m.auto.rejected {
		if now.Before(until) {
			skip[id] = true
		} else {
			delete(m.auto.rejected, id)
		}
	}
	m.auto.mu.Unlock()
	// Copier PnL first: copiers made money following them. Then plain PnL.
	var rows []Leader
	for _, q := range []LeaderQuery{{TimeRange: "90D", SortBy: "COPIER_PNL", Page: 1}, {TimeRange: "90D", SortBy: "COPIER_PNL", Page: 2},
		{TimeRange: "90D", SortBy: "PNL", Page: 1}, {TimeRange: "30D", SortBy: "COPIER_PNL", Page: 1}} {
		page, err := m.Leaderboard(ctx, q)
		if err != nil {
			return fmt.Errorf("leaderboard: %w", err)
		}
		rows = append(rows, page.List...)
	}
	picks := pickable(rows, skip, now)
	st.Candidates = len(picks)
	for _, r := range picks {
		if need <= 0 || st.Checked >= maxChecksPerRun {
			break
		}
		st.Checked++
		reject, f, err := m.checkPick(ctx, r.PortfolioID)
		if err != nil {
			return err
		}
		if reject != "" {
			m.auto.mu.Lock()
			m.auto.rejected[r.PortfolioID] = now.Add(24 * time.Hour)
			m.auto.mu.Unlock()
			logger.Infof("copy-trade auto-follow: skip %s (%s)", r.Nickname, reject)
			if err := m.store.AddAutoLog(store.CopyTradeAutoLog{Time: now.UnixMilli(), Action: "skip", PortfolioID: r.PortfolioID,
				Nickname: r.Nickname, Reason: reject}); err != nil {
				return err
			}
			continue
		}
		f.Source, f.PickedAt = "auto", now.UnixMilli()
		if err := m.addFollow(f); err != nil {
			if strings.Contains(err.Error(), "at most") {
				return nil
			}
			return err
		}
		need--
		st.Added++
		logger.Infof("copy-trade auto-follow: follow %s", f.Nickname)
		if err := m.store.AddAutoLog(store.CopyTradeAutoLog{Time: now.UnixMilli(), Action: "follow", PortfolioID: f.PortfolioID,
			Nickname: f.Nickname, Reason: fmt.Sprintf("roi90=%.0f,mdd90=%.0f", float64(r.ROI), float64(r.MDD))}); err != nil {
			return err
		}
	}
	return nil
}

// checkPick reads a candidate's profile and 30-day results. It returns why
// the candidate fails the entry bar, or the follow to save.
func (m *Monitor) checkPick(ctx context.Context, id string) (string, store.CopyTradeFollow, error) {
	f := store.CopyTradeFollow{PortfolioID: id}
	d, err := m.Client.Detail(ctx, id)
	if err != nil {
		return "", f, err
	}
	if reason := pickReject(d); reason != "" {
		return reason, f, nil
	}
	perf, err := m.Client.Performance(ctx, id, "30D")
	if err != nil {
		return "", f, err
	}
	if perf.ROI <= 0 || float64(perf.MDD) > pickMaxMDD {
		return fmt.Sprintf("%s=%.0f,mdd30=%.0f", Reject30D, float64(perf.ROI), float64(perf.MDD)), f, nil
	}
	applyDetail(&f, d)
	f.ROI, f.PnL, f.MDD, f.WinRate = float64(perf.ROI), float64(perf.PnL), float64(perf.MDD), float64(perf.WinRate)
	f.ProfileAt, f.ProfileVersion = time.Now().UnixMilli(), profileVersion
	return "", f, nil
}
