package api

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"nofx/copytrade"
	"nofx/store"
	"nofx/trader/binance"
)

func kl15(startSec int64, closes ...float64) []binance.MarketKline {
	out := make([]binance.MarketKline, len(closes))
	for i, c := range closes {
		out[i] = binance.MarketKline{Time: startSec + int64(i)*900, Close: c}
	}
	return out
}

func TestPriceAtKhReportsGapToDueTime(t *testing.T) {
	k := kl15(1000, 1, 2, 3) // opens at 1000s, 1900s, 2800s
	p, gap, ok := priceAtKh(k, 2000*1000)
	if !ok || p != 2 || gap != 100*1000 {
		t.Fatalf("want close 2 with a 100s gap, got %v %v %v", p, gap, ok)
	}
	if _, _, ok := priceAtKh(k, 500*1000); ok {
		t.Fatal("a time before the first candle has no price")
	}
	// Far past the last candle: the price exists but the gap exposes it as
	// stale, which is what lets the evaluator refuse it.
	_, gap, ok = priceAtKh(k, 10000*1000)
	if !ok || gap <= outcomeExitTolMs {
		t.Fatalf("a due time hours after the last candle must report a large gap, got %v", gap)
	}
}

func outcomeWith(sym, bias string, at int64, ret float64) store.CopyTradeScoreOutcome {
	return store.CopyTradeScoreOutcome{Symbol: sym, Bias: bias, AnalyzedAt: at, Ret4h: fp(ret), Done4h: true}
}

func TestStatForAppliesCostAndNeutralBand(t *testing.T) {
	rows := []store.CopyTradeScoreOutcome{
		outcomeWith("A", "short", 0, 0.010),  // hit
		outcomeWith("B", "short", 0, -0.010), // miss
		outcomeWith("C", "short", 0, 0.001),  // inside the 0.5% neutral band
		outcomeWith("D", "short", 0, 0.003),  // inside the band; gross win, net win (0.3% > 0.2% cost)
	}
	st := statFor("4h", "short", "all", rows, nil)
	if st.Samples != 4 || st.DirSamples != 2 {
		t.Fatalf("want 4 samples, 2 directional; got %d/%d", st.Samples, st.DirSamples)
	}
	if st.HitRate != 50 {
		t.Fatalf("hit rate is over directional samples only: got %v, want 50", st.HitRate)
	}
	// (1.0 - 1.0 + 0.1 + 0.3) / 4 = 0.1%
	if math.Abs(st.AvgRet-0.1) > 1e-9 {
		t.Fatalf("gross avg = %v, want 0.1%%", st.AvgRet)
	}
	if math.Abs(st.NetAvgRet-(0.1-0.2)) > 1e-9 {
		t.Fatalf("net avg = %v, want gross minus 0.2%% cost", st.NetAvgRet)
	}
	if st.NetWinRate != 50 { // A and D clear the cost; B and C do not
		t.Fatalf("net win rate = %v, want 50", st.NetWinRate)
	}
	if st.CostPct != 0.2 {
		t.Fatalf("cost pct = %v, want 0.2", st.CostPct)
	}
}

func TestBaselinesShowWhatNeedsNoModel(t *testing.T) {
	// A falling market: every coin's raw long return is negative.
	var rows []store.CopyTradeScoreOutcome
	for i, sym := range []string{"A", "B", "C", "D"} {
		rows = append(rows, outcomeWith(sym, "long", int64(i), -0.02)) // long that lost: raw -2%
	}
	bs := baselinesFor("4h", rows)
	by := map[string]baselineStat{}
	for _, b := range bs {
		by[b.Name] = b
	}
	if by["short_all"].HitRate != 100 || math.Abs(by["short_all"].NetAvgRet-(2-0.2)) > 1e-9 {
		t.Fatalf("short-everything in a falling market must look good: %+v", by["short_all"])
	}
	if by["long_all"].HitRate != 0 {
		t.Fatalf("long-everything must lose: %+v", by["long_all"])
	}
	if by["momentum"].Samples != 0 {
		t.Fatalf("momentum needs a previous reading of the same coin: %+v", by["momentum"])
	}
}

func TestBaselineMomentumFollowsTheCoinsPreviousMove(t *testing.T) {
	// A full 4h apart each time, so each previous window has matured: using a
	// previous reading whose window is still open would read the future.
	h := int64(4 * 3600 * 1000)
	rows := []store.CopyTradeScoreOutcome{
		outcomeWith("A", "long", 0, 0.03),    // raw +3%
		outcomeWith("A", "long", h, 0.02),    // momentum goes long: +2%
		outcomeWith("A", "short", 2*h, 0.01), // raw -1%; previous raw +2% so momentum long: -1%
	}
	var m baselineStat
	for _, b := range baselinesFor("4h", rows) {
		if b.Name == "momentum" {
			m = b
		}
	}
	if m.Samples != 2 {
		t.Fatalf("momentum samples = %d, want 2", m.Samples)
	}
	if math.Abs(m.NetAvgRet-(0.5-0.2)) > 1e-9 {
		t.Fatalf("momentum net = %v, want (2%% - 1%%)/2 - 0.2%%", m.NetAvgRet)
	}
}

func TestOutcomesEndpointReportsWindowBaselinesAndVersions(t *testing.T) {
	s, router := coinPoolServer(t)
	router.GET("/copytrade/coin-score/outcomes", s.handleCoinScoreOutcomes)
	now := time.Now().UnixMilli()
	add := func(sym, bias, ver string, at int64, ret float64) {
		o := store.CopyTradeScoreOutcome{UserID: "alice", Symbol: sym, Bias: bias, AnalyzedAt: at,
			Bull: 20, Bear: 70, Ret4h: fp(ret), Done4h: true, ConfigVersion: ver}
		if err := s.store.CopyTrade().SaveScoreOutcome(&o); err != nil {
			t.Fatal(err)
		}
	}
	add("AUSDT", "short", "", now-10*24*3600*1000, 0.02)                 // legacy, in window
	add("BUSDT", "short", "copytrade-ai-v13", now-2*24*3600*1000, -0.01) // v13, in window
	add("CUSDT", "short", "copytrade-ai-v13", now-40*24*3600*1000, 0.05) // outside the 30d window

	w := call(router, http.MethodGet, "/copytrade/coin-score/outcomes", "alice", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var resp struct {
		Rows      []outcomeStat             `json:"rows"`
		Baselines map[string][]baselineStat `json:"baselines"`
		Window    struct {
			Days          int64   `json:"days"`
			RetentionDays int64   `json:"retention_days"`
			CostPct       float64 `json:"cost_pct"`
			Truncated     bool    `json:"truncated"`
		} `json:"window"`
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Window.Days != 30 || resp.Window.RetentionDays != 90 || resp.Window.CostPct != 0.2 || resp.Window.Truncated {
		t.Fatalf("unexpected window: %+v", resp.Window)
	}
	if resp.Rows[0].Horizon != "4h" || resp.Rows[0].Samples != 2 {
		t.Fatalf("the 40-day-old row must be outside the window; overall 4h = %+v", resp.Rows[0])
	}
	if len(resp.Baselines["4h"]) != 3 {
		t.Fatalf("want three 4h baselines, got %+v", resp.Baselines["4h"])
	}
	if len(resp.Versions) != 2 || resp.Versions[0] != "copytrade-ai-v13" || resp.Versions[1] != "legacy" {
		t.Fatalf("versions newest first: %v", resp.Versions)
	}

	w = call(router, http.MethodGet, "/copytrade/coin-score/outcomes?version=legacy", "alice", "")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Rows[0].Samples != 1 || resp.Rows[0].HitRate != 100 {
		t.Fatalf("?version=legacy must keep only the legacy row: %+v", resp.Rows[0])
	}
}

func TestListScoreOutcomesSinceIsOldestFirstAndWindowed(t *testing.T) {
	s, _ := coinPoolServer(t)
	for _, at := range []int64{300, 100, 200, 50} {
		o := store.CopyTradeScoreOutcome{UserID: "alice", Symbol: "AUSDT", Bias: "short", AnalyzedAt: at}
		if err := s.store.CopyTrade().SaveScoreOutcome(&o); err != nil {
			t.Fatal(err)
		}
	}
	got, truncated, err := s.store.CopyTrade().ListScoreOutcomesSince("alice", 100)
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if len(got) != 3 || got[0].AnalyzedAt != 100 || got[2].AnalyzedAt != 300 {
		t.Fatalf("want 100,200,300 oldest first, got %+v", got)
	}
}

func TestScoreFeaturesOmitWhatWasNotMeasured(t *testing.T) {
	raw := scoreFeaturesJSON(nil, binance.DerivativeInfo{}, nil, relStrength{Coin24h: 3, BTC24h: 1}, nil)
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"rel_1h", "rel_4h", "funding", "oi_chg_1h", "ls_ratio"} {
		if _, ok := m[absent]; ok {
			t.Fatalf("%s was not measured and must be omitted, not zero: %s", absent, raw)
		}
	}
	if m["rel_24h"] != 2.0 {
		t.Fatalf("rel_24h = %v, want 2", m["rel_24h"])
	}

	sum := &copytrade.SymbolSummary{}
	sum.Long.Openers, sum.Short.Traders = 2, 5
	raw = scoreFeaturesJSON(&binance.MarketTicker{FundingRate: 0.0001, QuoteVolume24h: 5e8},
		binance.DerivativeInfo{OpenInterest: 1, OIChangePct1h: 1.5, LongShortRatio: 1.2},
		sum, relStrength{Coin4h: -1, BTC4h: -3, Has4h: true}, nil)
	m = nil
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m["rel_4h"] != 2.0 || m["coin_4h"] != -1.0 || m["oi_chg_1h"] != 1.5 || m["openers_long"] != 2.0 || m["traders_short"] != 5.0 {
		t.Fatalf("unexpected features: %s", raw)
	}
	if f, _ := m["funding"].(float64); math.Abs(f-0.01) > 1e-12 {
		t.Fatalf("funding is stored in %%: got %v", m["funding"])
	}
}

// A neutral reading is not a call: its stored return is the raw long return, so
// counting it scored every neutral as a long. In a falling market that dragged
// the AI's measured net down (+0.50% -> +0.07% per 4h on the owner's data).
func TestAIStatsCountOnlyDirectionalCallsWhileBaselinesKeepEveryCoin(t *testing.T) {
	s, router := coinPoolServer(t)
	router.GET("/copytrade/coin-score/outcomes", s.handleCoinScoreOutcomes)
	now := time.Now().UnixMilli()
	add := func(sym, bias string, at int64, ret float64) {
		o := store.CopyTradeScoreOutcome{UserID: "alice", Symbol: sym, Bias: bias, AnalyzedAt: at,
			Bull: 60, Bear: 20, Ret4h: fp(ret), Done4h: true}
		if err := s.store.CopyTrade().SaveScoreOutcome(&o); err != nil {
			t.Fatal(err)
		}
	}
	// Two real short calls that worked, plus three neutral readings on coins
	// that fell (stored as negative long returns).
	add("AUSDT", "short", now-10*3600e3, 0.02)
	add("BUSDT", "short", now-10*3600e3, 0.02)
	for i, sym := range []string{"NUSDT", "OUSDT", "PUSDT"} {
		add(sym, "neutral", now-10*3600e3+int64(i), -0.03)
	}

	w := call(router, http.MethodGet, "/copytrade/coin-score/outcomes", "alice", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var resp struct {
		Rows      []outcomeStat             `json:"rows"`
		Baselines map[string][]baselineStat `json:"baselines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	all := resp.Rows[0]
	if all.Horizon != "4h" || all.Side != "all" || all.Band != "all" {
		t.Fatalf("unexpected first row: %+v", all)
	}
	if all.Samples != 2 {
		t.Fatalf("AI row must count the 2 directional calls only, got %d", all.Samples)
	}
	if all.HitRate != 100 {
		t.Fatalf("both calls were right: hit rate = %v, want 100", all.HitRate)
	}
	// The baselines still describe the whole market, neutral coins included:
	// 5 coins, of which 3 fell 3% and 2 "fell" (winning shorts of 2%).
	var shortAll baselineStat
	for _, b := range resp.Baselines["4h"] {
		if b.Name == "short_all" {
			shortAll = b
		}
	}
	if shortAll.Samples != 5 {
		t.Fatalf("baselines must keep every scored coin, got %d", shortAll.Samples)
	}
}

// The momentum baseline may only use a previous reading whose own window has
// finished; its forward return is not knowable before that.
func TestMomentumBaselineIgnoresAnUnfinishedPreviousWindow(t *testing.T) {
	base := int64(0)
	h := int64(4 * 3600 * 1000)
	mk := func(at int64, ret float64) store.CopyTradeScoreOutcome {
		return store.CopyTradeScoreOutcome{Symbol: "AUSDT", Bias: "long", AnalyzedAt: at, Ret4h: fp(ret), Done4h: true}
	}
	// Second reading only 1h after the first: the first window is still open.
	overlapping := []store.CopyTradeScoreOutcome{mk(base, 0.05), mk(base+3600e3, 0.04)}
	var m baselineStat
	for _, b := range baselinesFor("4h", overlapping) {
		if b.Name == "momentum" {
			m = b
		}
	}
	if m.Samples != 0 {
		t.Fatalf("an unfinished previous window must not be used, got %d samples", m.Samples)
	}
	// A full horizon apart: usable.
	spaced := []store.CopyTradeScoreOutcome{mk(base, 0.05), mk(base+h, 0.04)}
	for _, b := range baselinesFor("4h", spaced) {
		if b.Name == "momentum" {
			m = b
		}
	}
	if m.Samples != 1 {
		t.Fatalf("a matured previous window must be used, got %d samples", m.Samples)
	}
	if m.HitRate != 100 {
		t.Fatalf("up then up again is a hit: %v", m.HitRate)
	}
}

func TestHorizonMillisMatchesTheLabels(t *testing.T) {
	for label, want := range map[string]int64{"4h": 4 * 3600 * 1000, "24h": 24 * 3600 * 1000, "72h": 72 * 3600 * 1000} {
		if got := horizonMillis(label); got != want {
			t.Fatalf("horizonMillis(%q) = %d, want %d", label, got, want)
		}
	}
}
