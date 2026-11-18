package copytrade

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
)

// Board sync replaces the rule-based auto-follow: once a day it reads Binance's
// 90-day copier-PnL leaderboard and follows the top public accounts. Private
// accounts in the top of the board cannot be read but stay visible as
// observe-only. It does not score traders or drop them for breaking rules.

const (
	// BoardSize is how many public lead traders the daily sync follows.
	BoardSize = 100
	// A followed board trader is dropped only when it falls past boardDropRank
	// or is absent from the board for boardMissedDays consecutive daily scans.
	// The gap between BoardSize and boardDropRank is a buffer so a trader on the
	// boundary does not enter and leave every day.
	boardDropRank   = 150
	boardMissedDays = 3
	// boardScanLimit bounds the leaderboard rows read: enough to find BoardSize
	// public accounts plus the drop band (Binance returns 30 rows a page).
	boardScanLimit = 300
	// boardInterval is how often the board is synced.
	boardInterval = 24 * time.Hour
)

// BoardSyncConfig is the daily board sync setting.
type BoardSyncConfig struct {
	Enabled bool `json:"enabled"`
}

// BoardSyncState is the latest sync, for the page.
type BoardSyncState struct {
	Config    BoardSyncConfig `json:"config"`
	LastRun   time.Time       `json:"last_run"`
	Error     string          `json:"error,omitempty"`
	Scanned   int             `json:"scanned"`
	Public    int             `json:"public"`
	Private   int             `json:"private"`
	Following int             `json:"following"`
	Added     int             `json:"added"`
	Dropped   int             `json:"dropped"`
	Kept      int             `json:"kept"`
	Running   bool            `json:"running"`
}

type boardManager struct {
	mu    sync.Mutex
	state BoardSyncState
	wake  chan struct{}
	run   sync.Mutex // one sync at a time
}

func (b *boardManager) init() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.wake == nil {
		b.wake = make(chan struct{}, 1)
	}
}

type boardCandidate struct {
	id       string
	rank     int
	nickname string
}

// SetBoardSync applies a setting and runs the sync soon when it is on.
func (m *Monitor) SetBoardSync(c BoardSyncConfig) {
	m.board.init()
	m.board.mu.Lock()
	m.board.state.Config = c
	m.board.mu.Unlock()
	if c.Enabled {
		select {
		case m.board.wake <- struct{}{}:
		default:
		}
	}
}

func (m *Monitor) BoardState() BoardSyncState {
	m.board.mu.Lock()
	defer m.board.mu.Unlock()
	return m.board.state
}

func (m *Monitor) setBoardState(st BoardSyncState) {
	m.board.mu.Lock()
	defer m.board.mu.Unlock()
	st.Config = m.board.state.Config // a save during the run wins
	m.board.state = st
}

func (m *Monitor) boardLoop(ctx context.Context) {
	m.board.init()
	// A setting loaded at startup must not run before the first polls.
	select {
	case <-m.board.wake:
	default:
	}
	// Give the first polls time to settle before rebuilding the follow list.
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	t := time.NewTicker(boardInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		case <-m.board.wake:
		}
		m.RunBoardSync(ctx, time.Now())
	}
}

// RunBoardSync runs the board sync once. It does nothing while disabled.
func (m *Monitor) RunBoardSync(ctx context.Context, now time.Time) {
	m.board.init()
	m.board.run.Lock()
	defer m.board.run.Unlock()
	cfg := m.BoardState().Config
	if !cfg.Enabled {
		return
	}
	st := BoardSyncState{Config: cfg, LastRun: now, Running: true}
	m.setBoardState(st)
	err := m.runBoardSync(ctx, now, &st)
	st.Running = false
	if err != nil {
		st.Error = err.Error()
		logger.Warnf("copy-trade board sync: %v", err)
	}
	m.setBoardState(st)
}

func (m *Monitor) runBoardSync(ctx context.Context, now time.Time, st *BoardSyncState) error {
	rows, err := m.fetchBoardRows(ctx)
	if err != nil {
		return err
	}
	st.Scanned = len(rows)

	// Currently followed board traders, and the persisted members (for the
	// absence count and the observe-only display data).
	m.mu.RLock()
	current := map[string]store.CopyTradeFollow{}
	for id, s := range m.snaps {
		// "auto" follows are the legacy rule-based picks the board sync now
		// replaces: reconcile them too, or they would linger unmaintained.
		if s.Follow.Source == "board" || s.Follow.Source == "auto" {
			current[id] = s.Follow
		}
	}
	m.mu.RUnlock()
	persisted, err := m.store.ListBoardMembers()
	if err != nil {
		return err
	}
	prev := map[string]store.CopyTradeBoardMember{}
	observing := map[string]store.CopyTradeBoardMember{}
	for _, b := range persisted {
		if b.Observe {
			observing[b.PortfolioID] = b
		} else {
			prev[b.PortfolioID] = b
		}
	}

	rankOf := map[string]int{}
	nick := map[string]string{}
	var candidates []boardCandidate
	var privateTop []store.CopyTradeBoardMember
	seenPub := map[string]bool{}
	for i, r := range rows {
		rank := i + 1
		if rank > boardScanLimit {
			break
		}
		if r.PortfolioID == "" {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(r.PortfolioType)) {
		case "PRIVATE":
			// Trades are hidden; remember it for the observe-only list when it
			// sits in the top of the board.
			if rank <= BoardSize {
				privateTop = append(privateTop, store.CopyTradeBoardMember{
					PortfolioID: r.PortfolioID, Nickname: r.Nickname, AvatarURL: r.AvatarURL,
					PortfolioType: "PRIVATE", Rank: rank, Observe: true, LastSeenAt: now.UnixMilli()})
			}
		case "SIGNAL":
			// A signal portfolio mirrors a public one; follow that instead.
			pubID, d, rerr := m.resolvePublic(ctx, r.PortfolioID)
			if errors.Is(rerr, ErrPaused) {
				return rerr
			}
			if rerr != nil || pubID == "" {
				continue
			}
			if cur, ok := rankOf[pubID]; !ok || rank < cur {
				rankOf[pubID] = rank
			}
			if !seenPub[pubID] && len(candidates) < BoardSize {
				seenPub[pubID] = true
				candidates = append(candidates, boardCandidate{id: pubID, rank: rank, nickname: nicknameOf(d, r.Nickname)})
			}
		default: // PUBLIC (or an unlabelled row)
			if cur, ok := rankOf[r.PortfolioID]; !ok || rank < cur {
				rankOf[r.PortfolioID] = rank
			}
			if !seenPub[r.PortfolioID] && len(candidates) < BoardSize {
				seenPub[r.PortfolioID] = true
				candidates = append(candidates, boardCandidate{id: r.PortfolioID, rank: rank, nickname: r.Nickname})
			}
		}
	}
	for _, c := range candidates {
		nick[c.id] = c.nickname
	}
	st.Public = len(candidates)
	st.Private = len(privateTop)

	// Decide removals: past boardDropRank now, or absent boardMissedDays days.
	currentIDs := make([]string, 0, len(current))
	prevMissed := make(map[string]int, len(current))
	for id := range current {
		currentIDs = append(currentIDs, id)
		prevMissed[id] = prev[id].Missed
	}
	dropReasons, missedNow := planBoardDrops(currentIDs, rankOf, prevMissed)
	dropped := map[string]bool{}
	for id, why := range dropReasons {
		dropped[id] = true
		logger.Infof("copy-trade board sync: drop %s (%s)", id, why)
	}
	for id := range dropped {
		// Keep fills: what a dropped trader did stays measurable.
		if err := m.unfollow(id, true); err != nil {
			logger.Warnf("copy-trade board sync: unfollow %s: %v", id, err)
			continue
		}
		st.Dropped++
	}

	// Permanent exclusions the user made by hand are respected: a trader the
	// user unfollowed is not added back by the next sync.
	permanent := map[string]bool{}
	if ex, err := m.store.Exclusions(now.UnixMilli()); err == nil {
		for _, e := range ex {
			if e.Until == 0 {
				permanent[e.PortfolioID] = true
			}
		}
	} else {
		logger.Warnf("copy-trade board sync: read exclusions: %v", err)
	}

	// Add the top public candidates that are not followed yet.
	var addErr error
	addedBoard := map[string]bool{}
	for _, c := range candidates {
		if _, ok := current[c.id]; ok {
			continue
		}
		if dropped[c.id] || permanent[c.id] {
			continue
		}
		added, err := m.ensureBoardFollow(ctx, c.id, c.nickname, now)
		if err != nil {
			if isPausedErr(err) {
				addErr = err
				break
			}
			logger.Warnf("copy-trade board sync: follow %s: %v", c.id, err)
			continue
		}
		if added {
			addedBoard[c.id] = true
			st.Added++
		}
	}

	// Persist the members: every followed one (kept or newly added) with its
	// rank / absence, then the observe-only private ones.
	following := map[string]bool{}
	for id := range current {
		if !dropped[id] {
			following[id] = true
		}
	}
	for id := range addedBoard {
		following[id] = true
	}
	// A kept legacy auto-follow becomes a board follow: same trader, now
	// governed by the board, and no longer on probation.
	for id := range following {
		if f, ok := current[id]; ok && f.Source == "auto" {
			if err := m.store.SetFollowSource(id, "board"); err != nil {
				logger.Warnf("copy-trade board sync: relabel %s as board: %v", id, err)
			}
			m.mu.Lock()
			if sn, ok := m.snaps[id]; ok {
				sn.Follow.Source, sn.Follow.PickedAt = "board", 0
			}
			m.mu.Unlock()
		}
	}
	for id := range following {
		mem := prev[id]
		mem.PortfolioID = id
		mem.Followed = true
		mem.Observe = false
		mem.Rank = rankOf[id]
		if n := nick[id]; n != "" {
			mem.Nickname = n
		}
		if _, onBoard := rankOf[id]; onBoard {
			mem.Missed = 0
			mem.LastSeenAt = now.UnixMilli()
		} else if n, ok := missedNow[id]; ok {
			mem.Missed = n
		}
		if err := m.store.SaveBoardMember(&mem); err != nil {
			logger.Warnf("copy-trade board sync: save member %s: %v", id, err)
		}
	}
	for id := range dropped {
		if err := m.store.DeleteBoardMember(id); err != nil {
			logger.Warnf("copy-trade board sync: delete member %s: %v", id, err)
		}
	}
	st.Kept = len(following)
	st.Following = len(following)

	// Observe-only private accounts: refresh what Binance does show (last trade
	// time, 30-day results, coin mix), best effort.
	keepObserve := map[string]bool{}
	for _, ob := range privateTop {
		d, derr := m.Client.Detail(ctx, ob.PortfolioID)
		if derr != nil {
			if isPausedErr(derr) {
				addErr = derr
				break
			}
			// Keep the previous row so the account does not vanish on a blip.
			if old, ok := observing[ob.PortfolioID]; ok {
				ob = old
			}
		} else {
			ob.AvatarURL = d.AvatarURL
			ob.Nickname = nicknameOf(d, ob.Nickname)
			ob.LastTradeTime = d.LastTradeTime
			ob.PortfolioType = "PRIVATE"
			if perf, perr := m.Client.Performance(ctx, ob.PortfolioID, "30D"); perr == nil {
				ob.ROI30, ob.PnL30 = float64(perf.ROI), float64(perf.PnL)
				ob.MDD30, ob.WinRate30 = float64(perf.MDD), float64(perf.WinRate)
			} else if isPausedErr(perr) {
				addErr = perr
				m.store.SaveBoardMember(&ob)
				keepObserve[ob.PortfolioID] = true
				continue
			}
			if shares, serr := m.Client.CoinShares(ctx, ob.PortfolioID, "30D"); serr == nil {
				if b, merr := json.Marshal(coinShareOut(shares)); merr == nil {
					ob.CoinShare, ob.CoinShareAt = string(b), now.UnixMilli()
				}
			} else if isPausedErr(serr) {
				addErr = serr
				m.store.SaveBoardMember(&ob)
				keepObserve[ob.PortfolioID] = true
				continue
			} else if old, ok := observing[ob.PortfolioID]; ok {
				ob.CoinShare, ob.CoinShareAt = old.CoinShare, old.CoinShareAt
			}
		}
		ob.Observe = true
		ob.Followed = false
		if ob.LastSeenAt == 0 {
			ob.LastSeenAt = now.UnixMilli()
		}
		if err := m.store.SaveBoardMember(&ob); err != nil {
			logger.Warnf("copy-trade board sync: save observe %s: %v", ob.PortfolioID, err)
		}
		keepObserve[ob.PortfolioID] = true
	}
	for id := range observing {
		if !keepObserve[id] && !following[id] {
			if err := m.store.DeleteBoardMember(id); err != nil {
				logger.Warnf("copy-trade board sync: delete observe %s: %v", id, err)
			}
		}
	}
	return addErr
}

// planBoardDrops decides which currently followed board traders to drop: a
// trader still on the board past boardDropRank goes now; one absent from the
// board is dropped on its boardMissedDays-th consecutive miss, and otherwise
// its miss count is carried forward.
func planBoardDrops(current []string, rankOf map[string]int, prevMissed map[string]int) (drops map[string]string, missed map[string]int) {
	drops = map[string]string{}
	missed = map[string]int{}
	for _, id := range current {
		if rank, ok := rankOf[id]; ok {
			if rank > boardDropRank {
				drops[id] = "rank past " + itoa(boardDropRank)
			}
			continue
		}
		m := prevMissed[id] + 1
		if m >= boardMissedDays {
			drops[id] = "absent " + itoa(m) + " days"
		} else {
			missed[id] = m
		}
	}
	return
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// fetchBoardRows pages through the 90-day copier-PnL leaderboard until
// boardScanLimit rows or the end.
func (m *Monitor) fetchBoardRows(ctx context.Context) ([]Leader, error) {
	var rows []Leader
	for page := 1; page <= boardScanLimit/30+2; page++ {
		p, err := m.Leaderboard(ctx, LeaderQuery{TimeRange: "90D", SortBy: "COPIER_PNL", Page: page})
		if err != nil {
			return rows, err
		}
		rows = append(rows, p.List...)
		if len(p.List) == 0 || len(rows) >= p.Total || len(rows) >= boardScanLimit {
			break
		}
	}
	return rows, nil
}

// resolvePublic follows a signal or private portfolio to the trader's public
// one. It returns "" (and the private detail) when there is no public portfolio.
func (m *Monitor) resolvePublic(ctx context.Context, id string) (string, Detail, error) {
	d, err := m.Client.Detail(ctx, id)
	if err != nil {
		return "", Detail{}, err
	}
	if (d.Signal() || d.Private()) && d.PublicID != "" && d.PublicID != id {
		pub, err := m.Client.Detail(ctx, d.PublicID)
		if err != nil {
			return "", Detail{}, err
		}
		return d.PublicID, pub, nil
	}
	if d.Private() {
		return "", d, nil
	}
	return id, d, nil
}

// ensureBoardFollow follows one board candidate after reading its profile.
func (m *Monitor) ensureBoardFollow(ctx context.Context, id, nickname string, now time.Time) (bool, error) {
	if m.isFollowing(id) {
		return false, nil
	}
	pubID, d, err := m.resolvePublic(ctx, id)
	if err != nil {
		return false, err
	}
	if pubID == "" || d.Private() || d.Signal() {
		return false, nil
	}
	if m.isFollowing(pubID) {
		return false, nil
	}
	f := store.CopyTradeFollow{PortfolioID: pubID, Source: "board"}
	applyDetail(&f, d)
	if nickname != "" && f.Nickname == "" {
		f.Nickname = nickname
	}
	if perf, perr := m.Client.Performance(ctx, pubID, "30D"); perr == nil {
		f.ROI, f.PnL, f.MDD, f.WinRate = float64(perf.ROI), float64(perf.PnL), float64(perf.MDD), float64(perf.WinRate)
	} else if isPausedErr(perr) {
		return false, perr
	}
	f.ProfileAt, f.ProfileVersion = now.UnixMilli(), profileVersion
	f.PerfAt = now.UnixMilli()
	if err := m.addFollow(f); err != nil {
		return false, err
	}
	logger.Infof("copy-trade board sync: follow %s (%s)", f.Nickname, f.PortfolioID)
	return true, nil
}

func (m *Monitor) isFollowing(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.snaps[id]
	return ok
}

func nicknameOf(d Detail, fallback string) string {
	if d.Nickname != "" {
		return d.Nickname
	}
	return fallback
}

func isPausedErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPaused) {
		return true
	}
	var limited *limitedError
	return errors.As(err, &limited)
}

type coinShareOutRow struct {
	Symbol string  `json:"symbol"`
	Ratio  float64 `json:"ratio"`
}

func coinShareOut(in []CoinShare) []coinShareOutRow {
	out := make([]coinShareOutRow, 0, len(in))
	for _, c := range in {
		out = append(out, coinShareOutRow{Symbol: strings.ToUpper(c.Symbol), Ratio: float64(c.Share)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ratio > out[j].Ratio })
	return out
}

