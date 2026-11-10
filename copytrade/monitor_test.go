package copytrade

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

// fakeBinance serves one public-position trader's detail, performance,
// positions and a growing trade history (newest first).
type fakeBinance struct {
	mu      sync.Mutex
	trades  []map[string]any
	limited bool
	// renamed serves fills with Binance's fields under other names.
	renamed bool
	calls   map[string]int
}

func (f *fakeBinance) add(t time.Time, side, ps string, qty, price, pnl float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := map[string]any{"time": t.UnixMilli(), "symbol": "BTCUSDT", "side": side, "positionSide": ps,
		"qty": qty, "price": price, "fee": 0.1, "realizedProfit": pnl}
	f.trades = append([]map[string]any{row}, f.trades...)
}

func (f *fakeBinance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.URL.Path]++
	write := func(data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": data})
	}
	if f.limited {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "11012005", "data": nil})
		return
	}
	switch r.URL.Path {
	case "/v1/friendly/future/copy-trade/lead-portfolio/detail":
		switch r.URL.Query().Get("portfolioId") {
		case "42":
		case "43": // private, public twin 42
			write(map[string]any{"leadPortfolioId": "43", "nickname": "测试员", "status": "ACTIVE",
				"portfolioType": "PRIVATE", "publicLeadPortfolioId": "42", "privateLeadPortfolioId": "43"})
			return
		case "44": // private only
			write(map[string]any{"leadPortfolioId": "44", "nickname": "私域", "status": "ACTIVE", "portfolioType": "PRIVATE"})
			return
		default:
			write(nil)
			return
		}
		write(map[string]any{"leadPortfolioId": "42", "nickname": "测试员", "status": "ACTIVE", "positionShow": true,
			"aumAmount": "1000", "currentCopyCount": 3, "maxCopyCount": 100})
	case "/v1/public/future/copy-trade/lead-portfolio/performance":
		write(map[string]any{"roi": 12.5, "pnl": 300, "mdd": 4, "winRate": 60})
	case "/v1/friendly/future/copy-trade/lead-data/positions":
		write([]map[string]any{
			{"symbol": "BTCUSDT", "positionSide": "LONG", "positionAmount": "1.5", "entryPrice": "101", "markPrice": "105",
				"leverage": 10, "unrealizedProfit": "6", "notionalValue": "157.5"},
			{"symbol": "ETHUSDT", "positionSide": "LONG", "positionAmount": "0", "entryPrice": "0"},
		})
	case "/v1/friendly/future/copy-trade/lead-portfolio/trade-history":
		var body struct{ PageNumber, PageSize int }
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		from := (body.PageNumber - 1) * body.PageSize
		to := min(from+body.PageSize, len(f.trades))
		page := []map[string]any{}
		if from < len(f.trades) {
			page = f.trades[from:to]
		}
		if f.renamed {
			moved := make([]map[string]any, len(page))
			for i, r := range page {
				moved[i] = map[string]any{"tradeTime": r["time"], "pair": r["symbol"], "side": r["side"], "px": r["price"], "q": r["qty"]}
			}
			page = moved
		}
		write(map[string]any{"total": len(f.trades), "list": page})
	default:
		http.NotFound(w, r)
	}
}

func newTestMonitor(t *testing.T) (*Monitor, *fakeBinance) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewCopyTradeStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	fake := &fakeBinance{calls: map[string]int{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	m := NewMonitor(st)
	m.Client.BaseURL, m.Client.Spacing = srv.URL, 0
	return m, fake
}

func TestMonitorBackfillsThenAddsOnlyNewFills(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	now := time.Now()
	fake.add(now.Add(-40*24*time.Hour), "BUY", "LONG", 9, 90, 0) // older than the 30-day backfill
	fake.add(now.Add(-3*time.Hour), "BUY", "LONG", 1, 100, 0)
	fake.add(now.Add(-2*time.Hour), "BUY", "LONG", 0.5, 103, 0)

	if _, err := m.Follow(ctx, "nope"); err == nil {
		t.Fatal("following an unknown ID must fail")
	}
	f, err := m.Follow(ctx, "42")
	if err != nil || f.Nickname != "测试员" || !f.PositionShow {
		t.Fatalf("follow = %+v, %v", f, err)
	}
	if _, err := m.Follow(ctx, "42"); err == nil {
		t.Fatal("following twice must fail")
	}
	refreshRound(t, m, "42")
	s, _ := m.Snapshot("42")
	if s.Error != "" || s.Follow.ROI != 12.5 || s.PositionsSource != "public" || len(s.Positions) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	p := s.Positions[0]
	if p.Leverage != 10 || p.Margin != 15.15 || p.OpenedAt != now.Add(-3*time.Hour).UnixMilli() {
		t.Fatalf("public position must take the rebuilt open time: %+v", p)
	}
	if len(s.Events) != 2 || s.Events[0].Action != "add" || s.Events[1].Action != "open" {
		t.Fatalf("events newest first, without the 10-day-old fill: %+v", s.Events)
	}

	// A second refresh stores only the new fill and does not duplicate.
	fake.add(now.Add(-time.Minute), "SELL", "LONG", 1.5, 110, 13)
	refreshRound(t, m, "42")
	fills, _ := m.store.Fills("42", 0)
	if len(fills) != 3 {
		t.Fatalf("stored fills = %d, want 3", len(fills))
	}
	s, _ = m.Snapshot("42")
	if s.Events[0].Action != "close" || !s.Hot {
		t.Fatalf("latest event = %+v hot=%v", s.Events[0], s.Hot)
	}
	if fake.calls["/v1/public/future/copy-trade/lead-portfolio/performance"] != 1 {
		t.Fatal("profile must not be refetched every cycle")
	}

	if err := m.Unfollow("42"); err != nil {
		t.Fatal(err)
	}
	if fills, _ := m.store.Fills("42", 0); len(fills) != 0 {
		t.Fatal("unfollow must delete stored fills")
	}
}

func TestClientPausesWhenLimited(t *testing.T) {
	m, fake := newTestMonitor(t)
	fake.limited = true
	_, err := m.Client.Detail(context.Background(), "42")
	var limited *limitedError
	if !errors.As(err, &limited) {
		t.Fatalf("code 11012005 must count as rate limiting: %v", err)
	}
	st := m.Client.Status()
	if !st.Paused || time.Until(st.PausedUntil) < 50*time.Second {
		t.Fatalf("status = %+v", st)
	}
	before := fake.calls["/v1/friendly/future/copy-trade/lead-portfolio/detail"]
	if _, err := m.Client.Detail(context.Background(), "42"); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused client must not call Binance: %v", err)
	}
	if fake.calls["/v1/friendly/future/copy-trade/lead-portfolio/detail"] != before {
		t.Fatal("request sent while paused")
	}
}

func TestProxyValidationAndMask(t *testing.T) {
	for _, bad := range []string{"ftp://1.2.3.4:21", "http://1.2.3.4", "http://1.2.3.4:8080/path", "nonsense"} {
		if _, err := ParseProxy(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	for _, ok := range []string{"http://1.2.3.4:8080", "socks5://u:p@proxy.example:1080", "socks5h://h:1"} {
		if _, err := ParseProxy(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if got := MaskProxy("http://user:secret@1.2.3.4:8080"); got != "http://user:***@1.2.3.4:8080" {
		t.Fatalf("mask = %s", got)
	}
}

func TestFollowPrivatePortfolioUsesPublicOne(t *testing.T) {
	m, _ := newTestMonitor(t)
	ctx := context.Background()
	if _, err := m.Follow(ctx, "44"); !errors.Is(err, ErrPrivate) {
		t.Fatalf("a private portfolio without a public one must be refused: %v", err)
	}
	f, err := m.Follow(ctx, "43")
	if err != nil || f.PortfolioID != "42" || f.Private {
		t.Fatalf("private 43 must follow its public 42: %+v %v", f, err)
	}
	if _, err := m.Follow(ctx, "43"); err == nil || !strings.Contains(err.Error(), "already following") {
		t.Fatalf("its public portfolio is already followed: %v", err)
	}
}

func TestRenamedFieldsShowAsFormatError(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	fake.add(time.Now().Add(-time.Hour), "BUY", "LONG", 1, 100, 0)
	fake.renamed = true
	if _, err := m.Client.Trades(ctx, "42", 1, 50); !errors.Is(err, ErrFormat) {
		t.Fatalf("fills without price or time must be a format error: %v", err)
	}
	if st := m.Client.Status(); len(st.FormatErrors) != 1 || !strings.HasPrefix(st.FormatErrors[0], "trade-history") {
		t.Fatalf("status lists the endpoint: %+v", st.FormatErrors)
	}
	fake.renamed = false
	if _, err := m.Client.Trades(ctx, "42", 1, 50); err != nil {
		t.Fatal(err)
	}
	if st := m.Client.Status(); len(st.FormatErrors) != 0 {
		t.Fatalf("cleared once the data looks right: %+v", st.FormatErrors)
	}
	if d, err := m.Client.Detail(ctx, "nope"); err != nil || d.PortfolioID != "" {
		t.Fatalf("an unknown ID is not a format error: %+v %v", d, err)
	}
}

func TestLoadServesStoredDataBeforeTheFirstPoll(t *testing.T) {
	m, fake := newTestMonitor(t)
	ctx := context.Background()
	fake.add(time.Now().Add(-time.Hour), "BUY", "LONG", 1, 100, 0)
	if _, err := m.Follow(ctx, "42"); err != nil {
		t.Fatal(err)
	}
	refreshRound(t, m, "42")
	// A restart: a new monitor over the same store, not polled yet.
	again := NewMonitor(m.store)
	again.Load()
	s, ok := again.Snapshot("42")
	if !ok || s.UpdatedAt.IsZero() || time.Since(s.UpdatedAt) > time.Minute || len(s.Events) != 1 {
		t.Fatalf("restored from stored fills, dated by the last sync: %+v", s)
	}
}

// Binance showing a bigger position than the stored fills explain: the trader
// held it before monitoring, so the first fill is an add, not an open, and the
// holding time stays unknown instead of looking one hour old.
func TestRebuildTreatsOlderPositionsAsAdds(t *testing.T) {
	m, _ := newTestMonitor(t)
	seen := time.Now().Add(-time.Hour).UnixMilli()
	if err := m.store.ReplaceFills("9", 0, []store.CopyTradeFill{
		{PortfolioID: "9", Time: seen, Symbol: "BTCUSDT", Side: "BUY", PositionSide: "BOTH", Qty: 1, Price: 100},
	}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{Follow: store.CopyTradeFollow{PortfolioID: "9", PositionShow: true}, nextDue: time.Now()}
	live := []LivePosition{{Symbol: "BTCUSDT", PositionSide: "BOTH", PositionAmount: 3, EntryPrice: 95, MarkPrice: 96, Leverage: 10}}
	m.rebuild(snap, live)
	if len(snap.Positions) != 1 {
		t.Fatalf("positions = %+v", snap.Positions)
	}
	p := snap.Positions[0]
	if !p.PreExisting || p.OpenedAt != 0 || !approx(p.FirstEntry, 100) {
		t.Fatalf("position held before the fills: %+v", p)
	}
	for _, e := range snap.Events {
		if e.Action == "open" {
			t.Fatalf("the fill adds to a position seen earlier: %+v", snap.Events)
		}
	}
}
