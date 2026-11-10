package copytrade

import (
	"context"
	"encoding/json"
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

// reviewed is a fully synced auto follow, judged on its merits.
func reviewed(id string, score float64) Snapshot {
	return Snapshot{Follow: store.CopyTradeFollow{PortfolioID: id, Nickname: id, Status: "ACTIVE", Source: "auto",
		ROI: 20, MDD: 10, MarginBalance: 20000, ProfileAt: 1, LastTradeTime: time.Now().UnixMilli()},
		HistoryComplete: true, Score: TraderScore{Score: score}}
}

func TestDropReasons(t *testing.T) {
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour).UnixMilli()
	trapped := Position{Symbol: "X", Health: Health{Priced: true, Trapped: true}, OpenedAt: old}
	cases := []struct {
		name string
		edit func(s *Snapshot)
		want string
	}{
		{"clean", func(s *Snapshot) {}, ""},
		{"closed", func(s *Snapshot) { s.Follow.Status = "CLOSED" }, RuleClosed},
		{"martingale", func(s *Snapshot) { s.Style.Kind = StyleMartingale }, RuleMartingale},
		{"losing month", func(s *Snapshot) { s.Follow.ROI = -3 }, RuleROINegative},
		{"deep drawdown", func(s *Snapshot) { s.Follow.MDD = 45 }, RuleMDDHigh},
		{"small balance", func(s *Snapshot) { s.Follow.MarginBalance = 500 }, RuleBalanceLow},
		{"sits on old losses", func(s *Snapshot) {
			s.Positions = []Position{trapped, trapped, {Symbol: "Y", Health: Health{Priced: true}, OpenedAt: old}}
		}, RuleStaleTrapped},
		{"one old loss of three is fine", func(s *Snapshot) {
			s.Positions = []Position{trapped, {Health: Health{Priced: true}}, {Health: Health{Priced: true}}}
		}, ""},
		{"gone quiet", func(s *Snapshot) { s.Follow.LastTradeTime = now.Add(-20 * 24 * time.Hour).UnixMilli() }, RuleInactive},
		// A new pick is only held to the hard rules.
		{"probation", func(s *Snapshot) {
			s.Follow.PickedAt, s.Follow.ROI = now.Add(-24*time.Hour).UnixMilli(), -10
		}, ""},
		// Half-synced data must not get anyone dropped.
		{"history pending", func(s *Snapshot) { s.HistoryComplete, s.Follow.ROI = false, -10 }, ""},
	}
	for _, c := range cases {
		s := reviewed("a", 50)
		c.edit(&s)
		if got := strings.Join(DropReasons(s, now), ","); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPlanRemovals(t *testing.T) {
	now := time.Now()
	bad := func(id string, score float64) Snapshot {
		s := reviewed(id, score)
		s.Follow.ROI = -5
		return s
	}
	manual := bad("m", 1)
	manual.Follow.Source = "manual"
	views := []Snapshot{bad("b1", 30), bad("b2", 20), bad("b3", 40), reviewed("ok1", 70), reviewed("ok2", 60), manual}

	// At target with a budget of 1: only the worst breaker goes; manual stays.
	plan := planRemovals(views, 6, 1, now)
	if len(plan.drops) != 1 || plan.drops[0].id != "b2" || len(plan.trims) != 0 {
		t.Fatalf("budget 1: %+v", plan)
	}
	// Target cut to 2: every breaker goes whatever the budget, then the
	// weakest clean pick. The manual follow counts towards the 2 but is never
	// touched: 6 − 3 drops − 1 trim = ok1 + the manual one.
	plan = planRemovals(views, 2, 0, now)
	if len(plan.drops) != 3 || len(plan.trims) != 1 || plan.trims[0].id != "ok2" {
		t.Fatalf("over target: %+v", plan)
	}
	for _, p := range append(plan.drops, plan.trims...) {
		if p.id == "m" {
			t.Fatal("a manual follow was removed")
		}
	}
}

func TestPickable(t *testing.T) {
	now := time.Now()
	start := now.Add(-200 * 24 * time.Hour).UnixMilli()
	rows := []Leader{
		{PortfolioID: "good", ROI: 80, MDD: 10, CopierPnL: 1000, StartTime: start, WinRate: 70},
		{PortfolioID: "better", ROI: 300, MDD: 8, CopierPnL: 5000, StartTime: start, WinRate: 75, AUM: 1e6, CopyCount: 200},
		{PortfolioID: "new", ROI: 500, MDD: 5, CopierPnL: 100, StartTime: now.Add(-10 * 24 * time.Hour).UnixMilli()},
		{PortfolioID: "risky", ROI: 900, MDD: 60, CopierPnL: 100, StartTime: start},
		{PortfolioID: "copiers lose", ROI: 50, MDD: 10, CopierPnL: -10, StartTime: start},
		{PortfolioID: "followed", ROI: 80, MDD: 10, CopierPnL: 1000, StartTime: start},
		{PortfolioID: "good", ROI: 1, MDD: 1, CopierPnL: 1, StartTime: start}, // repeat from another board
	}
	got := pickable(rows, map[string]bool{"followed": true}, now)
	if len(got) != 2 || got[0].PortfolioID != "better" || got[1].PortfolioID != "good" || got[1].ROI != 80 {
		t.Fatalf("picks = %+v", got)
	}
}

// fakeBoard serves a leaderboard and the profiles behind it.
type fakeBoard struct {
	mu      sync.Mutex
	leaders []map[string]any
	details map[string]map[string]any
	calls   map[string]int
}

func (f *fakeBoard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.URL.Path]++
	write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": data}) }
	switch r.URL.Path {
	case "/v1/friendly/future/copy-trade/home-page/query-list":
		write(map[string]any{"total": len(f.leaders), "list": f.leaders})
	case "/v1/friendly/future/copy-trade/lead-portfolio/detail":
		write(f.details[r.URL.Query().Get("portfolioId")])
	case "/v1/public/future/copy-trade/lead-portfolio/performance":
		write(map[string]any{"roi": 15, "pnl": 500, "mdd": 6, "winRate": 65})
	default:
		write(map[string]any{"total": 0, "list": []any{}})
	}
}

func TestRunAutoPicksAndKeepsManualFollows(t *testing.T) {
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
	for _, id := range []string{"101", "102", "103", "104", "105", "106"} {
		fake.leaders = append(fake.leaders, map[string]any{"leadPortfolioId": id, "nickname": "L" + id,
			"roi": 100, "mdd": 10, "copierPnl": 1000, "startTime": start})
		fake.details[id] = map[string]any{"leadPortfolioId": id, "nickname": "L" + id, "status": "ACTIVE",
			"positionShow": true, "marginBalance": "20000", "portfolioType": "PUBLIC"}
	}
	fake.details["102"]["positionShow"] = false      // hidden positions: inferred from fills, picked
	fake.details["103"]["marginBalance"] = "300"     // too small: skipped
	fake.details["106"]["portfolioType"] = "PRIVATE" // trades hidden: skipped
	srv := httptest.NewServer(fake)
	defer srv.Close()
	m := NewMonitor(st)
	m.Client.BaseURL, m.Client.Spacing = srv.URL, 0

	// The user excluded 104 by unfollowing it earlier, and follows 900 by hand.
	if err := st.Exclude(store.CopyTradeExclusion{PortfolioID: "104", Reason: "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := m.addFollow(store.CopyTradeFollow{PortfolioID: "900", Source: "manual", Status: "CLOSED",
		ProfileAt: 1}); err != nil {
		t.Fatal(err)
	}
	m.SetAuto(AutoConfig{Enabled: true, Target: 5})
	m.RunAuto(context.Background(), now)
	st1 := m.AutoState()
	if st1.Error != "" || st1.Added != 3 {
		t.Fatalf("run: %+v", st1)
	}
	ids := map[string]string{}
	for _, s := range m.Snapshots() {
		ids[s.Follow.PortfolioID] = s.Follow.Source
	}
	// 101, 102, 105 picked; 103, 106 fail the profile check; 104 is excluded;
	// the closed manual follow is flagged but kept.
	for _, id := range []string{"101", "102", "105"} {
		if ids[id] != "auto" {
			t.Fatalf("%s not picked: %v", id, ids)
		}
	}
	if len(ids) != 4 || ids["900"] != "manual" {
		t.Fatalf("follows = %v", ids)
	}
	if snap, _ := m.Snapshot("900"); strings.Join(snap.Review, ",") != RuleClosed {
		t.Fatalf("manual follow review = %v", snap.Review)
	}
	if snap, _ := m.Snapshot("101"); !snap.Probation || snap.Follow.ROI != 15 {
		t.Fatalf("new pick = %+v", snap.Follow)
	}
	// A second run does not re-check the rejected ones.
	before := fake.calls["/v1/friendly/future/copy-trade/lead-portfolio/detail"]
	m.RunAuto(context.Background(), now)
	if after := fake.calls["/v1/friendly/future/copy-trade/lead-portfolio/detail"]; after != before {
		t.Fatalf("rejected picks were checked again: %d → %d", before, after)
	}
	logs, _ := st.AutoLogs(0, 10)
	actions := map[string]string{}
	for _, l := range logs {
		actions[l.PortfolioID] = l.Action + ":" + l.Reason
	}
	if len(logs) != 5 || actions["103"] != "skip:"+RejectBalance+"=300" || actions["106"] != "skip:"+RulePrivate ||
		!strings.HasPrefix(actions["102"], "follow:") {
		t.Fatalf("logs = %v", actions)
	}
	// Lowering the target trims the weakest picks, never the manual follow.
	m.SetAuto(AutoConfig{Enabled: true, Target: MinAutoTarget})
	m.RunAuto(context.Background(), now)
	if n := len(m.Snapshots()); n != 4 {
		t.Fatalf("target 5 with 4 follows must not trim: %d", n)
	}
}

// Off means off: nothing is followed or dropped.
func TestRunAutoOff(t *testing.T) {
	m, fake := newTestMonitor(t)
	m.RunAuto(context.Background(), time.Now())
	if len(fake.calls) != 0 || len(m.Snapshots()) != 0 {
		t.Fatalf("auto-follow off made requests: %v", fake.calls)
	}
}

func TestPickRejectAcceptsHiddenPositions(t *testing.T) {
	ok := Detail{Status: "ACTIVE", PortfolioType: "PUBLIC", MarginBalance: 8000}
	if r := pickReject(ok); r != "" {
		t.Fatalf("hidden positions rejected: %q", r)
	}
	for want, d := range map[string]Detail{
		RulePrivate:             {Status: "ACTIVE", PortfolioType: "PRIVATE", MarginBalance: 8000},
		RuleClosed:              {Status: "CLOSED", MarginBalance: 8000},
		RejectBalance + "=3000": {Status: "ACTIVE", MarginBalance: 3000},
	} {
		if r := pickReject(d); r != want {
			t.Errorf("pickReject = %q, want %q", r, want)
		}
	}
}
