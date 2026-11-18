package copytrade

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"nofx/store"
)

func TestRunBoardSyncFollowsPublicObservesPrivate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewCopyTradeStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	fake := &fakeBoard{calls: map[string]int{}, details: map[string]map[string]any{}}
	row := func(id, typ string) map[string]any {
		return map[string]any{"leadPortfolioId": id, "nickname": "N" + id, "portfolioType": typ,
			"roi": 10, "mdd": 5, "copierPnl": 100, "startTime": time.Now().Add(-200 * 24 * time.Hour).UnixMilli()}
	}
	fake.leaders = []map[string]any{row("A", "PUBLIC"), row("P1", "PRIVATE"), row("S1", "SIGNAL"), row("B", "PUBLIC"), row("M", "PUBLIC")}
	fake.details["A"] = map[string]any{"leadPortfolioId": "A", "nickname": "NA", "status": "ACTIVE", "positionShow": true, "portfolioType": "PUBLIC"}
	fake.details["B"] = map[string]any{"leadPortfolioId": "B", "nickname": "NB", "status": "ACTIVE", "positionShow": true, "portfolioType": "PUBLIC"}
	fake.details["P1"] = map[string]any{"leadPortfolioId": "P1", "nickname": "NP1", "status": "ACTIVE", "portfolioType": "PRIVATE", "lastTradeTime": 123}
	fake.details["S1"] = map[string]any{"leadPortfolioId": "S1", "nickname": "NS1", "status": "ACTIVE", "portfolioType": "SIGNAL", "publicLeadPortfolioId": "S1P"}
	fake.details["S1P"] = map[string]any{"leadPortfolioId": "S1P", "nickname": "NS1P", "status": "ACTIVE", "positionShow": true, "portfolioType": "PUBLIC"}
	fake.details["M"] = map[string]any{"leadPortfolioId": "M", "nickname": "NM", "status": "ACTIVE", "positionShow": true, "portfolioType": "PUBLIC"}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	m := NewMonitor(st)
	m.Client.BaseURL, m.Client.Spacing = srv.URL, 0
	// The user follows M by hand; it is also a public board row and must stay
	// manual, never be turned into a board follow nor dropped by the sync.
	if err := m.addFollow(store.CopyTradeFollow{PortfolioID: "M", Source: "manual", Status: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	m.SetBoardSync(BoardSyncConfig{Enabled: true})
	m.RunBoardSync(context.Background(), time.Now())

	if got := m.BoardState(); got.Error != "" {
		t.Fatalf("board sync error: %v", got.Error)
	}
	sources := map[string]string{}
	for _, s := range m.Snapshots() {
		sources[s.Follow.PortfolioID] = s.Follow.Source
	}
	for _, id := range []string{"A", "B", "S1P"} {
		if sources[id] != "board" {
			t.Fatalf("%s not followed from the board: %v", id, sources)
		}
	}
	if sources["M"] != "manual" {
		t.Fatalf("manual follow M must stay manual: %v", sources)
	}
	if _, ok := sources["P1"]; ok {
		t.Fatalf("private P1 must not be followed: %v", sources)
	}
	members, err := st.ListBoardMembers()
	if err != nil {
		t.Fatal(err)
	}
	var observe *store.CopyTradeBoardMember
	for i := range members {
		if members[i].PortfolioID == "P1" {
			observe = &members[i]
		}
		if members[i].PortfolioID == "M" && members[i].Followed {
			t.Fatalf("manual follow M must not be recorded as a board follow: %+v", members[i])
		}
	}
	if observe == nil || !observe.Observe || observe.Followed {
		t.Fatalf("P1 must be observe-only: %+v", observe)
	}
	if observe.LastTradeTime != 123 {
		t.Fatalf("observe last trade time = %d, want 123", observe.LastTradeTime)
	}
}
