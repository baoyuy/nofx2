package copytrade

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nofx/store"
)

func TestLinkedPortfoliosCountOnlyPublic(t *testing.T) {
	makeSnap := func(id, public string, value float64) Snapshot {
		return Snapshot{Follow: store.CopyTradeFollow{PortfolioID: id, PublicID: public, Nickname: "same name"},
			Positions: []Position{{Symbol: "INJUSDT", Side: "long", Qty: value / 8, EntryPrice: 8, EntryValue: value}},
			Events:    []Event{{Symbol: "INJUSDT", Side: "long", Action: "open", Time: 1000}}, HistoryComplete: true}
	}
	snaps := []Snapshot{makeSnap("signal", "public", 743), makeSnap("public", "", 104), makeSnap("unrelated", "", 50)}
	holders := Holders(snaps, "injusdt")
	if len(holders) != 2 || holders[0].PortfolioID != "public" || !holders[0].HistoryComplete {
		t.Fatalf("holders = %+v", holders)
	}
	summary := Symbols(snaps)[0]
	if summary.Long.Traders != 2 || summary.Long.Value != 154 {
		t.Fatalf("summary = %+v", summary)
	}
	if feed := Feed(snaps, FeedFilter{}); len(feed) != 2 {
		t.Fatalf("feed = %+v", feed)
	}
	if feed := Feed(snaps, FeedFilter{PortfolioID: "signal"}); len(feed) != 1 {
		t.Fatalf("explicit account feed = %+v", feed)
	}
	if holders := Holders(snaps[:1], "INJUSDT"); len(holders) != 1 {
		t.Fatal("unpaired account was discarded")
	}
	now := time.Now()
	for i := range snaps {
		snaps[i].PositionsUpdatedAt = now
		snaps[i].FillsUpdatedAt = now
		snaps[i].ProfileUpdatedAt = now
	}
	snaps[1].HistoryComplete = false
	if fresh := Fresh(snaps, time.Minute, now); len(fresh) != 1 || fresh[0].Follow.PortfolioID != "unrelated" {
		t.Fatalf("fresh must not fall back to linked signal: %+v", fresh)
	}
}

func TestSignalFollowResolvesPublicAndRejectsDuplicate(t *testing.T) {
	m, _ := newTestMonitor(t)
	base := m.Client.http.Transport
	m.Client.http.Transport = profileTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/detail") && r.URL.Query().Get("portfolioId") == "signal" {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":"000000","data":{"leadPortfolioId":"signal","nickname":"测试员","portfolioType":"SIGNAL","publicLeadPortfolioId":"42"}}`))}, nil
		}
		return base.RoundTrip(r)
	})
	f, err := m.Follow(context.Background(), "signal")
	if err != nil || f.PortfolioID != "42" {
		t.Fatalf("follow = %+v, %v", f, err)
	}
	if _, err := m.Follow(context.Background(), "signal"); err == nil {
		t.Fatal("duplicate public identity accepted")
	}
	if n, _ := m.store.CountFollows(); n != 1 {
		t.Fatalf("follows = %d", n)
	}
}

func TestDetailRejectsWrongAccountIdentity(t *testing.T) {
	m, _ := newTestMonitor(t)
	m.Client.http.Transport = profileTestTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":"000000","data":{"leadPortfolioId":"wrong","nickname":"wrong account"}}`))}, nil
	})
	d, err := m.Client.Detail(context.Background(), "42")
	if err == nil || d.Nickname != "" {
		t.Fatalf("mismatched identity accepted: %+v %v", d, err)
	}
	if _, err := m.Follow(context.Background(), "42"); err == nil {
		t.Fatal("wrong identity followed")
	}
	if n, _ := m.store.CountFollows(); n != 0 {
		t.Fatal("wrong identity persisted")
	}
}

func TestSignalAutoPickResolvesPublic(t *testing.T) {
	m, _ := newTestMonitor(t)
	var performanceID string
	m.Client.http.Transport = profileTestTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":"000000","data":{"leadPortfolioId":"signal","portfolioType":"SIGNAL","publicLeadPortfolioId":"public"}}`
		if r.URL.Query().Get("portfolioId") == "public" {
			body = `{"code":"000000","data":{"leadPortfolioId":"public","portfolioType":"PUBLIC","marginBalance":"6000","nickname":"public trader"}}`
		}
		if strings.Contains(r.URL.Path, "/performance") {
			performanceID = r.URL.Query().Get("portfolioId")
			body = `{"code":"000000","data":{"roi":10,"mdd":5}}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	reject, f, err := m.checkPick(context.Background(), "signal")
	if err != nil || reject != "" || f.PortfolioID != "public" || performanceID != "public" {
		t.Fatalf("pick = %s, %+v, %v; performance account %s", reject, f, err, performanceID)
	}
}

func TestMonitorSnapshotsUseOneLinkedIdentityAndHidePartialInferences(t *testing.T) {
	m, _ := newTestMonitor(t)
	signal := &Snapshot{Follow: store.CopyTradeFollow{PortfolioID: "signal", PublicID: "public", Nickname: "same trader"},
		HistoryComplete: true, Positions: []Position{{Symbol: "INJUSDT", Side: "long", Source: "inferred", Qty: 10, EntryPrice: 8, EntryValue: 80}}}
	public := &Snapshot{Follow: store.CopyTradeFollow{PortfolioID: "public", Nickname: "same trader"},
		Positions: []Position{{Symbol: "INJUSDT", Side: "long", Source: "inferred", Qty: 2, EntryPrice: 8, EntryValue: 16}}}
	m.snaps["signal"], m.snaps["public"] = signal, public
	snaps := m.Snapshots()
	if len(snaps) != 1 || snaps[0].Follow.PortfolioID != "public" {
		t.Fatalf("duplicate follow cards: %+v", snaps)
	}
	if len(snaps[0].Positions) != 0 || len(Symbols(snaps)) != 0 || len(Holders(snaps, "INJUSDT")) != 0 {
		t.Fatal("incomplete inferred positions leaked into holdings or statistics")
	}
	if s, ok := m.Snapshot("public"); !ok || len(s.Positions) != 0 {
		t.Fatal("detail endpoint exposed incomplete inferred holdings")
	}
	if len(public.Positions) != 1 {
		t.Fatal("readiness filtering mutated the stored replay")
	}
	public.HistoryComplete = true
	if snaps = m.Snapshots(); len(snaps[0].Positions) != 1 || Symbols(snaps)[0].Long.Value != 16 {
		t.Fatal("completed public portfolio did not become available")
	}
	public.HistoryComplete = false
	public.Positions[0].Source = "public"
	if snaps = m.Snapshots(); len(snaps[0].Positions) != 1 {
		t.Fatal("independently observed public positions must remain visible")
	}
}
