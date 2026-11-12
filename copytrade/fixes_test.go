package copytrade

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"nofx/store"
)

func TestEmptyProfileDoesNotBlankFollow(t *testing.T) {
	m, _ := newTestMonitor(t)
	// "77" is unknown to the fake: Binance answers with no data.
	f := store.CopyTradeFollow{PortfolioID: "77", Nickname: "kept", Status: "ACTIVE", PositionShow: true,
		MarginBalance: 9000, ROI: 12}
	if err := m.store.SaveFollow(&f); err != nil {
		t.Fatal(err)
	}
	if err := m.syncProfile(context.Background(), &f); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("empty profile accepted: %v", err)
	}
	if f.Nickname != "kept" || f.MarginBalance != 9000 || f.ROI != 12 || f.ProfileAt != 0 {
		t.Fatalf("follow blanked in memory: %+v", f)
	}
	got, err := m.store.GetFollow("77")
	if err != nil || got.Nickname != "kept" || got.Status != "ACTIVE" || !got.PositionShow || got.ROI != 12 {
		t.Fatalf("stored follow blanked: %+v %v", got, err)
	}
}

func TestAutoPickResolvesAliasesOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewCopyTradeStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	start := now.Add(-200 * 24 * time.Hour).UnixMilli()
	fake := &fakeBoard{calls: map[string]int{}, details: map[string]map[string]any{}}
	for _, id := range []string{"201", "202"} { // signal and private aliases of 300
		fake.leaders = append(fake.leaders, map[string]any{"leadPortfolioId": id, "nickname": "L" + id,
			"roi": 100, "mdd": 10, "copierPnl": 1000, "startTime": start})
	}
	fake.details["201"] = map[string]any{"leadPortfolioId": "201", "portfolioType": "SIGNAL", "publicLeadPortfolioId": "300"}
	fake.details["202"] = map[string]any{"leadPortfolioId": "202", "portfolioType": "PRIVATE", "publicLeadPortfolioId": "300"}
	fake.details["300"] = map[string]any{"leadPortfolioId": "300", "nickname": "public", "status": "ACTIVE",
		"marginBalance": "20000", "portfolioType": "PUBLIC"}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	m := NewMonitor(st)
	m.Client.BaseURL, m.Client.Spacing = srv.URL, 0
	m.SetAuto(AutoConfig{Enabled: true, Target: 5})
	m.RunAuto(context.Background(), now)
	if s := m.AutoState(); s.Error != "" || s.Added != 1 {
		t.Fatalf("run: %+v", s)
	}
	if _, ok := m.Snapshot("300"); !ok {
		t.Fatal("private alias was not resolved to the public portfolio")
	}
	if n, _ := st.CountFollows(); n != 1 {
		t.Fatalf("follows = %d", n)
	}
	const detail = "/v1/friendly/future/copy-trade/lead-portfolio/detail"
	before := fake.calls[detail]
	m.RunAuto(context.Background(), now.Add(time.Hour))
	if s := m.AutoState(); s.Error != "" || s.Checked != 0 || fake.calls[detail] != before {
		t.Fatalf("aliases of a followed portfolio were checked again: %+v, %d extra requests", s, fake.calls[detail]-before)
	}
}

// A busy trader adds fills between scheduler turns: the backfill must still
// finish, and store every fill exactly once in Binance's order.
func TestBackfillConvergesWhileNewFillsKeepArriving(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m, fake := newTestMonitor(t)
		ctx := context.Background()
		at := time.Now().Add(-2 * time.Hour)
		n := 0
		add := func(k int) {
			for i := 0; i < k; i++ {
				if rng.Intn(3) > 0 { // otherwise the same millisecond again
					at = at.Add(time.Millisecond)
				}
				n++
				fake.add(at, "BUY", "LONG", float64(n), 100, 0)
			}
		}
		add(TradePageSize*(1+rng.Intn(4)) + rng.Intn(TradePageSize))
		f, err := m.Follow(ctx, "42")
		if err != nil {
			t.Fatal(err)
		}
		var pending *fillSync
		turns := 0
		for ; turns < 60; turns++ {
			if pending, err = m.syncFillsPage(ctx, f, pending); err != nil {
				t.Fatal(err)
			}
			if pending == nil {
				break
			}
			add(rng.Intn(6)) // sometimes none, usually a few
		}
		if pending != nil {
			t.Fatalf("seed %d: backfill never finished while fills kept arriving", seed)
		}
		// Fills added after the last validated head arrive with the next sync.
		if pending, err = m.syncFillsPage(ctx, f, nil); err != nil || pending != nil {
			t.Fatalf("seed %d: catch-up: %+v %v", seed, pending, err)
		}
		rows, err := m.store.Fills("42", 0)
		if err != nil || len(rows) != n {
			t.Fatalf("seed %d: fills lost or duplicated: stored %d of %d after %d turns (%v)", seed, len(rows), n, turns, err)
		}
		// Replay order (time, then newest index last) is the order they happened.
		for i, r := range rows {
			if r.Qty != float64(i+1) {
				t.Fatalf("seed %d: fill %d out of order: qty %v", seed, i, r.Qty)
			}
		}
	}
}

func TestClosedPortfolioIsMarkedNotRetried(t *testing.T) {
	m, _ := newTestMonitor(t)
	f := store.CopyTradeFollow{PortfolioID: "88", Nickname: "gone", Status: "ACTIVE", Source: "auto", ROI: 30, ProfileAt: 1}
	if err := m.addFollow(f); err != nil {
		t.Fatal(err)
	}
	m.Client.http.Transport = profileTestTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
			`{"code":"11012028","message":"The current portfolio is closed.","data":null}`))}, nil
	})
	m.mu.Lock()
	s := m.snaps["88"]
	s.Positions = []Position{{Symbol: "BTCUSDT", Side: "long", Source: "public", Qty: 1, EntryPrice: 1, EntryValue: 1}}
	s.Follow.ProfileAt = 0
	s.fillsDue, s.positionDue = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	m.mu.Unlock()
	m.refresh(context.Background(), "88")
	snap, _ := m.Snapshot("88")
	if snap.Follow.Status != StatusClosed || snap.Follow.Nickname != "gone" || snap.Follow.ROI != 30 {
		t.Fatalf("closed portfolio not marked, or its profile lost: %+v", snap.Follow)
	}
	if snap.Error != "" || len(snap.Positions) != 0 || snap.Hot {
		t.Fatalf("closed portfolio still shows as failing or holding: %q %+v", snap.Error, snap.Positions)
	}
	if got := strings.Join(snap.Review, ","); got != RuleClosed {
		t.Fatalf("review = %q", got)
	}
	if st := m.Client.Status(); st.Paused || st.LastError != "" {
		t.Fatalf("a closed portfolio must not read as a data outage: %+v", st)
	}
	// Nothing left to poll but the profile, and that not before its interval.
	m.mu.RLock()
	kind, due := m.snaps["88"].task()
	m.mu.RUnlock()
	if kind != refreshProfile || time.Until(due) < profileInterval-time.Minute {
		t.Fatalf("closed portfolio is still polled: task %d in %v", kind, time.Until(due))
	}
	if q := snap.Quality(time.Hour, time.Now()); q.Complete() {
		t.Fatal("a closed portfolio has no data to be complete")
	}
	if got, err := m.store.GetFollow("88"); err != nil || got.Status != StatusClosed {
		t.Fatalf("stored status: %+v %v", got, err)
	}
}

func TestAliasFollowsAreRemovedOnLoad(t *testing.T) {
	m, _ := newTestMonitor(t)
	for _, f := range []store.CopyTradeFollow{
		{PortfolioID: "signal", PublicID: "public", Nickname: "same"},
		{PortfolioID: "public", Nickname: "same"},
		{PortfolioID: "lonely", PublicID: "not-followed", Nickname: "other"},
	} {
		if err := m.store.SaveFollow(&f); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.store.ReplaceFills("signal", 0, []store.CopyTradeFill{{PortfolioID: "signal", Time: 5, Symbol: "X", Qty: 1, Price: 1}}, 5); err != nil {
		t.Fatal(err)
	}
	m.Load()
	if n, _ := m.store.CountFollows(); n != 2 {
		t.Fatalf("follows = %d", n)
	}
	if _, ok := m.Snapshot("signal"); ok {
		t.Fatal("alias still followed")
	}
	if _, ok := m.Snapshot("lonely"); !ok {
		t.Fatal("an alias whose public portfolio is not followed must stay")
	}
	if rows, _ := m.store.Fills("signal", 0); len(rows) != 0 {
		t.Fatal("alias fills kept")
	}
}

func TestLosingMonthIsJudgedAgainstPeers(t *testing.T) {
	now := time.Now()
	peer := func(roi float64) store.CopyTradeFollow {
		return store.CopyTradeFollow{ROI: roi, ProfileAt: 1, Status: "ACTIVE"}
	}
	falling := dropContextOf([]store.CopyTradeFollow{peer(-8), peer(-6), peer(-4), peer(-2), peer(3)})
	rising := dropContextOf([]store.CopyTradeFollow{peer(10), peer(20), peer(30), peer(40), peer(50)})
	cases := []struct {
		name  string
		roi   float64
		peers DropContext
		drop  bool
	}{
		{"with the pack in a falling market", -5, falling, false},
		{"well under the pack", -15, falling, true},
		{"below the floor whatever the pack", -25, falling, true},
		{"negative while the others earn", -1, rising, true},
		{"positive", 2, rising, false},
		{"too few peers: plain rule", -1, DropContext{}, true},
	}
	for _, c := range cases {
		s := reviewed("a", 50)
		s.Follow.ROI = c.roi
		got := strings.Contains(strings.Join(DropReasonsIn(s, now, c.peers), ","), RuleROINegative)
		if got != c.drop {
			t.Errorf("%s: dropped = %v", c.name, got)
		}
	}
}

func TestHighFrequencyAndUnsyncedAreDropped(t *testing.T) {
	now := time.Now()
	s := reviewed("a", 50)
	s.Follow.PickedAt = now.Add(-3 * 24 * time.Hour).UnixMilli() // on probation
	s.OrdersPerDayAvg, s.FillDays = 300, 3
	if got := strings.Join(DropReasons(s, now), ","); got != RuleHighFreq {
		t.Fatalf("a bot on probation: %q", got)
	}
	s.FillDays = 1 // one busy day is not a habit
	if got := DropReasons(s, now); len(got) != 0 {
		t.Fatalf("one day of fills: %v", got)
	}
	s = reviewed("b", 50)
	s.HistoryComplete, s.pendingSince = false, now.Add(-49*time.Hour)
	if got := strings.Join(DropReasons(s, now), ","); got != RuleUnsynced {
		t.Fatalf("never synced: %q", got)
	}
	s.pendingSince = now.Add(-2 * time.Hour)
	if got := DropReasons(s, now); len(got) != 0 {
		t.Fatalf("still syncing: %v", got)
	}
}

func TestExclusionLengthFollowsTheReason(t *testing.T) {
	day := 24 * time.Hour
	for reason, want := range map[string]time.Duration{
		RuleROINegative:                        softExclusionDays * day,
		RuleROINegative + "," + RuleMartingale: exclusionDays * day,
		RuleHighFreq:                           exclusionDays * day,
		RuleClosed:                             goneExclusionDays * day,
		RuleMDDHigh + "," + RulePrivate:        goneExclusionDays * day,
	} {
		if got := exclusionFor(reason); got != want {
			t.Errorf("%s: %v, want %v", reason, got, want)
		}
	}
}

func TestAutoDropKeepsFillsForTheRecord(t *testing.T) {
	m, _ := newTestMonitor(t)
	now := time.Now()
	f := store.CopyTradeFollow{PortfolioID: "9", Nickname: "dropped", Status: "CLOSED", Source: "auto", ProfileAt: 1}
	if err := m.addFollow(f); err != nil {
		t.Fatal(err)
	}
	if err := m.store.ReplaceFills("9", 0, []store.CopyTradeFill{{PortfolioID: "9", Time: now.UnixMilli(), Symbol: "X", Qty: 1, Price: 1}}, 0); err != nil {
		t.Fatal(err)
	}
	st := AutoState{}
	if err := m.runAuto(context.Background(), AutoConfig{Enabled: true, Target: 0}, now, &st); err != nil || st.Dropped != 1 {
		t.Fatalf("run: %+v %v", st, err)
	}
	if rows, _ := m.store.Fills("9", 0); len(rows) != 1 {
		t.Fatal("fills of an auto-dropped trader were deleted")
	}
	ex, _ := m.store.Exclusions(now.UnixMilli())
	if len(ex) != 1 || time.UnixMilli(ex[0].Until).Sub(now) < 89*24*time.Hour {
		t.Fatalf("exclusion: %+v", ex)
	}
	// Following again by hand starts from a clean history.
	if err := m.addFollow(f); err != nil {
		t.Fatal(err)
	}
	if err := m.Unfollow("9"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := m.store.Fills("9", 0); len(rows) != 0 {
		t.Fatal("a manual unfollow keeps fills")
	}
}

func TestProfileRefreshReadsResultsOnceADay(t *testing.T) {
	m, fake := newTestMonitor(t)
	if _, err := m.Follow(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	// The monitor's own copy carries the row's creation time.
	m.mu.RLock()
	follow := m.snaps["42"].Follow
	m.mu.RUnlock()
	f := &follow
	var err error
	const perf = "/v1/public/future/copy-trade/lead-portfolio/performance"
	if err = m.syncProfile(context.Background(), f); err != nil || fake.calls[perf] != 1 || f.ROI != 12.5 {
		t.Fatalf("first refresh: %d calls, roi %v, %v", fake.calls[perf], f.ROI, err)
	}
	f.ProfileAt = time.Now().Add(-7 * time.Hour).UnixMilli() // profile due, results not
	if err := m.syncProfile(context.Background(), f); err != nil || fake.calls[perf] != 1 {
		t.Fatalf("results read again within a day: %d calls %v", fake.calls[perf], err)
	}
	if time.Since(time.UnixMilli(f.ProfileAt)) > time.Minute || f.ROI != 12.5 {
		t.Fatalf("profile not refreshed or results lost: %+v", f)
	}
	f.ProfileAt, f.PerfAt = time.Now().Add(-7*time.Hour).UnixMilli(), time.Now().Add(-25*time.Hour).UnixMilli()
	if err := m.syncProfile(context.Background(), f); err != nil || fake.calls[perf] != 2 {
		t.Fatalf("results not read after a day: %d calls %v", fake.calls[perf], err)
	}
}

func TestIdleAndDormantTradersArePolledLess(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	if _, err := m.Follow(ctx, "42"); err != nil {
		t.Fatal(err)
	}
	// The fake holds a position: the fast interval stays.
	due := func(kind refreshTask) time.Duration {
		m.mu.Lock()
		s := m.snaps["42"]
		far := time.Now().Add(time.Hour)
		s.positionDue, s.fillsDue, s.profileDue = far, far, far
		switch kind {
		case refreshPositions:
			s.positionDue = time.Time{}
		case refreshFills:
			s.fillsDue = time.Time{}
		}
		m.mu.Unlock()
		m.refresh(ctx, "42")
		m.mu.RLock()
		defer m.mu.RUnlock()
		if kind == refreshPositions {
			return time.Until(s.positionDue)
		}
		return time.Until(s.fillsDue)
	}
	if d := due(refreshPositions); d > positionInterval+time.Second {
		t.Fatalf("holding trader polled every %v", d)
	}
	// Private positions and an old last trade: nothing held, dormant.
	m.mu.Lock()
	m.snaps["42"].Follow.PositionShow = false
	m.snaps["42"].live = nil
	m.mu.Unlock()
	fake.add(time.Now().Add(-5*24*time.Hour), "BUY", "LONG", 1, 100, 0)
	fake.add(time.Now().Add(-5*24*time.Hour+time.Minute), "SELL", "LONG", 1, 101, 1)
	if d := due(refreshFills); d < dormantInterval-time.Minute {
		t.Fatalf("dormant trader's fills polled every %v", d)
	}
}

func TestProxiesEachKeepTheirOwnPaceAndBackoff(t *testing.T) {
	c := NewClient()
	if err := c.SetProxies([]string{"http://a:pw@10.0.0.1:8080", "http://10.0.0.2:8080", "http://a:pw@10.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	st := c.Status()
	if st.Proxies != 2 || st.Proxy != "http://a:***@10.0.0.1:8080, http://10.0.0.2:8080" {
		t.Fatalf("proxies: %+v", st)
	}
	if err := c.SetProxies(make([]string, 0)); err != nil || c.Status().Proxies != 1 || c.Status().Proxy != "" {
		t.Fatalf("no proxy: %+v %v", c.Status(), err)
	}
	var many []string
	for i := 0; i <= MaxProxies; i++ {
		many = append(many, "http://10.0.1."+string(rune('0'+i))+":8080")
	}
	if err := c.SetProxies(many); err == nil {
		t.Fatal("too many proxies accepted")
	}
	if err := c.SetProxies([]string{"http://10.0.0.1:8080", "ftp://10.0.0.2:21"}); err == nil {
		t.Fatal("a bad proxy in the list accepted")
	}

	// Two ways out, told apart by the transport that serves them.
	_ = c.SetProxies([]string{"http://10.0.0.1:8080", "http://10.0.0.2:8080"})
	c.Spacing = 0
	var mu sync.Mutex
	hits := map[int]int{}
	limit := map[int]bool{}
	for i, e := range c.egresses {
		i := i
		e.http = &http.Client{Transport: profileTestTransport(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			hits[i]++
			body := `{"code":"000000","data":{"total":0,"list":[]}}`
			if limit[i] {
				body = `{"code":"11012005","data":null}`
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
	}
	c.http = c.egresses[0].http
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if _, err := c.Trades(ctx, "1", 1, 50); err != nil {
			t.Fatal(err)
		}
	}
	if hits[0] != 3 || hits[1] != 3 {
		t.Fatalf("requests not shared in turn: %v", hits)
	}
	// Binance pushes back on the first IP only.
	mu.Lock()
	limit[0] = true
	mu.Unlock()
	var limited *limitedError
	for i := 0; i < 2; i++ { // whichever turn it is, the first IP is hit once
		if _, err := c.Trades(ctx, "1", 1, 50); errors.As(err, &limited) {
			break
		}
	}
	if st := c.Status(); st.Paused || st.ProxiesLimited != 1 {
		t.Fatalf("one limited proxy must not pause the monitor: %+v", st)
	}
	before := hits[0]
	for i := 0; i < 4; i++ {
		if _, err := c.Trades(ctx, "1", 1, 50); err != nil {
			t.Fatalf("the other proxy should carry on: %v", err)
		}
	}
	if hits[0] != before {
		t.Fatal("a paused proxy was used")
	}
	mu.Lock()
	limit[1] = true
	mu.Unlock()
	_, _ = c.Trades(ctx, "1", 1, 50)
	if _, err := c.Trades(ctx, "1", 1, 50); !errors.Is(err, ErrPaused) {
		t.Fatalf("every proxy limited: %v", err)
	}
	if st := c.Status(); !st.Paused || st.PausedUntil.IsZero() || st.ProxiesLimited != 2 {
		t.Fatalf("status: %+v", st)
	}
}

func TestDormantTraderOnScheduleIsNotStale(t *testing.T) {
	now := time.Now()
	s := Snapshot{Follow: store.CopyTradeFollow{PortfolioID: "q", LastTradeTime: now.Add(-5 * 24 * time.Hour).UnixMilli()},
		HistoryComplete: true, ProfileUpdatedAt: now,
		// Last asked 50 minutes ago: within the hourly schedule of a dormant trader.
		FillsUpdatedAt: now.Add(-50 * time.Minute), PositionsUpdatedAt: now.Add(-50 * time.Minute)}
	if !s.Quality(30*time.Minute, now).Complete() {
		t.Fatal("a dormant trader polled on schedule reads as stale")
	}
	// An active trader that old is stale.
	s.Follow.LastTradeTime = now.Add(-time.Hour).UnixMilli()
	if s.Quality(30*time.Minute, now).Complete() {
		t.Fatal("an active trader 50 minutes old must be stale")
	}
	// Past the schedule a dormant one is stale too.
	s.Follow.LastTradeTime = now.Add(-5 * 24 * time.Hour).UnixMilli()
	s.FillsUpdatedAt = now.Add(-100 * time.Minute)
	if s.Quality(30*time.Minute, now).Complete() {
		t.Fatal("a dormant trader not asked for 100 minutes must be stale")
	}
}

func TestManualAliasKeepsThePublicFollowManual(t *testing.T) {
	m, _ := newTestMonitor(t)
	for _, f := range []store.CopyTradeFollow{
		{PortfolioID: "signal", PublicID: "public", Source: "manual"},
		{PortfolioID: "public", Source: "auto", PickedAt: 5},
	} {
		if err := m.store.SaveFollow(&f); err != nil {
			t.Fatal(err)
		}
	}
	m.Load()
	snap, ok := m.Snapshot("public")
	if !ok || snap.Follow.Source != "manual" || snap.Follow.PickedAt != 0 {
		t.Fatalf("the user's follow became droppable: %+v", snap.Follow)
	}
	if got, err := m.store.GetFollow("public"); err != nil || got.Source != "manual" {
		t.Fatalf("stored: %+v %v", got, err)
	}
	if n, _ := m.store.CountFollows(); n != 1 {
		t.Fatalf("follows = %d", n)
	}
}
