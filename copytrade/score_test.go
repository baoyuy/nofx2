package copytrade

import (
	"context"
	"errors"
	"testing"
	"time"

	"nofx/store"
)

func TestScoreTraderPrefersSteadyOverGamblers(t *testing.T) {
	now := time.Now()
	start := now.Add(-120 * 24 * time.Hour).UnixMilli()
	steady := store.CopyTradeFollow{ROI: 40, MDD: 8, WinRate: 70, AUM: 200000, CopyCount: 300, StartTime: start, PositionShow: true}
	gambler := store.CopyTradeFollow{ROI: 5000, MDD: 66, WinRate: 43, AUM: 80000, CopyCount: 400, StartTime: start}
	newbie := steady
	newbie.StartTime = now.Add(-5 * 24 * time.Hour).UnixMilli()
	s, g, n := ScoreTrader(steady, now), ScoreTrader(gambler, now), ScoreTrader(newbie, now)
	if s.Score <= g.Score {
		t.Fatalf("steady %.1f must beat a 66%% drawdown gambler %.1f", s.Score, g.Score)
	}
	if n.Score >= s.Score {
		t.Fatalf("a 5-day-old record %.1f must score below the same record at 120 days %.1f", n.Score, s.Score)
	}
	if loser := ScoreTrader(store.CopyTradeFollow{ROI: -20, MDD: 30}, now); loser.Quality != 0 {
		t.Fatalf("negative ROI must give no quality: %+v", loser)
	}
	if s.Score < 0 || s.Score > 100 {
		t.Fatalf("score out of range: %v", s.Score)
	}
}

func TestConfidenceNeedsAgreementAndTraders(t *testing.T) {
	one := confidence([]vote{{trader: "a", side: "long", weight: 50, score: 80}})
	many := confidence([]vote{
		{trader: "a", side: "long", weight: 50, score: 80}, {trader: "b", side: "long", weight: 50, score: 80},
		{trader: "c", side: "long", weight: 50, score: 80}, {trader: "d", side: "long", weight: 50, score: 80},
		{trader: "e", side: "long", weight: 50, score: 80}, {trader: "f", side: "long", weight: 50, score: 80},
	})
	split := confidence([]vote{
		{trader: "a", side: "long", weight: 50, score: 80}, {trader: "b", side: "short", weight: 50, score: 80},
		{trader: "c", side: "long", weight: 50, score: 80}, {trader: "d", side: "short", weight: 50, score: 80},
	})
	if one.Direction != "long" || one.Score > 30 {
		t.Fatalf("one trader must not be confident: %+v", one)
	}
	if many.Score < 70 || many.Agreement != 1 {
		t.Fatalf("six agreeing good traders must be high confidence: %+v", many)
	}
	if split.Direction != "neutral" || split.Score != 0 {
		t.Fatalf("an even split has no direction: %+v", split)
	}
	weak := confidence([]vote{
		{trader: "a", side: "long", weight: 50, score: 10}, {trader: "b", side: "long", weight: 50, score: 10},
		{trader: "c", side: "long", weight: 50, score: 10}, {trader: "d", side: "long", weight: 50, score: 10},
		{trader: "e", side: "long", weight: 50, score: 10}, {trader: "f", side: "long", weight: 50, score: 10},
	})
	if weak.Score >= many.Score {
		t.Fatalf("weak traders must lower confidence: weak %.1f vs good %.1f", weak.Score, many.Score)
	}
	hedged := confidence([]vote{{trader: "a", side: "long", weight: 50, score: 80}, {trader: "a", side: "short", weight: 10, score: 80}})
	if hedged.Traders != 1 {
		t.Fatalf("a hedged trader counts once: %+v", hedged)
	}
}

func TestSignalsRecordEvaluateAndHitRate(t *testing.T) {
	m, _ := newTestMonitor(t)
	ctx := context.Background()
	hour := time.Now().Truncate(time.Hour).Add(-30 * time.Hour)
	// Price rises 1% per hour from 100; like Binance, only candles closed
	// by the clock are returned.
	clock := hour.Add(10 * time.Minute)
	m.Bars = func(_ context.Context, _ string, limit int) ([]Bar, error) {
		var bars []Bar
		for i := 0; i < 60; i++ {
			at := hour.Add(time.Duration(i-5) * time.Hour)
			if at.Add(time.Hour).After(clock) {
				break
			}
			bars = append(bars, Bar{Time: at.Unix(), Close: 100 * (1 + 0.01*float64(i-5))})
		}
		return bars[max(0, len(bars)-limit):], nil
	}
	start := time.Now().Add(-200 * 24 * time.Hour).UnixMilli()
	for i, side := range []string{"long", "long", "long", "long", "long", "short"} {
		id := string(rune('a' + i))
		m.snaps[id] = &Snapshot{
			Follow:             store.CopyTradeFollow{PortfolioID: id, ROI: 50, MDD: 10, WinRate: 70, StartTime: start, PositionShow: true, MarginBalance: 1000},
			Positions:          []Position{{Symbol: "BTCUSDT", Side: side, Qty: 1, EntryPrice: 100, EntryValue: 500}},
			UpdatedAt:          hour.Add(10 * time.Minute),
			PositionsUpdatedAt: hour.Add(10 * time.Minute), FillsUpdatedAt: hour.Add(10 * time.Minute),
			ProfileUpdatedAt: hour.Add(10 * time.Minute), HistoryComplete: true,
		}
	}
	if err := m.RecordSignals(ctx, hour.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordSignals(ctx, hour.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	sigs, _ := m.store.PendingSignals(time.Now().Unix())
	if len(sigs) != 1 || sigs[0].Direction != "long" || sigs[0].Price != 99 {
		t.Fatalf("one signal per hour priced at the previous close: %+v", sigs)
	}
	clock = time.Now()
	if err := m.EvaluateSignals(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	rates, err := m.HitRates(0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rates {
		if r.Direction != "all" || r.Group != GroupConfidence {
			continue
		}
		if r.Band == "all" && r.Horizon == "4h" {
			found = true
			// 99 → close of hour+3 (=103): long called right.
			if r.Samples != 1 || r.Hits != 1 || r.AvgReturn < 0.03 {
				t.Fatalf("4h hit rate: %+v", r)
			}
		}
		if r.Band == "all" && r.Horizon == "24h" && r.Samples != 1 {
			t.Fatalf("24h result missing: %+v", r)
		}
	}
	if !found {
		t.Fatal("no 4h row")
	}
}

func TestHitRatesCountARunOfSignalsOnce(t *testing.T) {
	up, down := 0.02, -0.02
	sig := func(h int64, sym, dir string, conf float64, ret float64) store.CopyTradeSignal {
		r := ret
		return store.CopyTradeSignal{Hour: h * 3600, Symbol: sym, Direction: dir, Confidence: conf, Ret4h: &r, Done4h: true, Ranked: true}
	}
	sigs := []store.CopyTradeSignal{
		sig(9, "BTC", "long", 80, down), // before the window: makes hour 10 a continuation
		sig(10, "BTC", "long", 80, down),
		sig(10, "ETH", "long", 80, up), // run 1, scored from here (high band)
		sig(11, "ETH", "long", 20, down),
		sig(12, "ETH", "long", 20, down),
		sig(13, "ETH", "short", 50, down), // flipped: run 2, a short that fell = hit
		sig(15, "ETH", "short", 50, up),   // gap at 14: run 3
	}
	rates := hitRates(sigs, 10*3600)
	get := func(band string) HitRate {
		for _, r := range rates {
			if r.Group == GroupConfidence && r.Band == band && r.Horizon == "4h" && r.Direction == "all" {
				return r
			}
		}
		return HitRate{}
	}
	if all := get("all"); all.Samples != 3 || all.Hits != 2 {
		t.Fatalf("three runs, two right: %+v", all)
	}
	if high, low := get("high"), get("low"); high.Samples != 1 || low.Samples != 0 {
		t.Fatalf("a run takes the band of its first hour: high %+v low %+v", high, low)
	}
	// Split by the called side: one long run (right), two short runs (one right).
	for _, r := range rates {
		if r.Group != GroupConfidence {
			continue
		}
		if r.Band == "all" && r.Horizon == "4h" && r.Direction == "long" && (r.Samples != 1 || r.Hits != 1) {
			t.Fatalf("long runs: %+v", r)
		}
		if r.Band == "all" && r.Horizon == "4h" && r.Direction == "short" && (r.Samples != 2 || r.Hits != 1) {
			t.Fatalf("short runs: %+v", r)
		}
	}
}

// Confidence must count INDEPENDENT voices, not accounts. One dominant holder
// plus many tiny ones is close to a single opinion, so it must not score like
// a wide consensus.
func TestConfidenceCountsIndependentVoicesNotAccounts(t *testing.T) {
	even := make([]vote, 0, 8)
	for i := 0; i < 8; i++ {
		even = append(even, vote{trader: string(rune('a' + i)), side: "long", weight: 1, base: 1, score: 60})
	}
	// Same eight accounts, but one carries almost all the size.
	lopsided := []vote{{trader: "whale", side: "long", weight: 100, base: 100, score: 60}}
	for i := 1; i < 8; i++ {
		lopsided = append(lopsided, vote{trader: string(rune('a' + i)), side: "long", weight: 0.1, base: 0.1, score: 60})
	}
	a, b := confidence(even), confidence(lopsided)
	if a.Effective <= 7 || b.Effective >= 2 {
		t.Fatalf("effective voices: even=%.2f lopsided=%.2f", a.Effective, b.Effective)
	}
	if b.CountFactor >= a.CountFactor {
		t.Fatalf("one dominant holder must not score like eight voices: %.3f vs %.3f", b.CountFactor, a.CountFactor)
	}
	if b.Score >= a.Score {
		t.Fatalf("score must follow the independent count: lopsided %.1f vs even %.1f", b.Score, a.Score)
	}
}

// A crowded coin is discounted: the measured record has the 8+ holder band
// doing worst, so more holders on one side is a cost beyond crowdFree.
func TestConfidenceDiscountsACrowdedCoin(t *testing.T) {
	mk := func(n int) Confidence {
		votes := make([]vote, 0, n)
		for i := 0; i < n; i++ {
			votes = append(votes, vote{trader: string(rune('a' + i)), side: "long", weight: 1, base: 1, score: 60})
		}
		return confidence(votes)
	}
	roomy, crowded := mk(crowdFree), mk(crowdFree+10)
	if roomy.Crowding != 1 {
		t.Fatalf("at or below crowdFree there is no discount: %v", roomy.Crowding)
	}
	if crowded.Crowding >= 1 || crowded.Crowding < crowdFloor-1e-9 {
		t.Fatalf("crowded discount out of range: %v", crowded.Crowding)
	}
	// Agreement and quality are identical, so the crowded coin must score
	// lower per voice despite having more accounts behind it.
	if crowded.Score/crowded.CountFactor >= roomy.Score/roomy.CountFactor {
		t.Fatalf("crowding must reduce the score per voice: %.2f vs %.2f",
			crowded.Score/crowded.CountFactor, roomy.Score/roomy.CountFactor)
	}
}

// Stake is the MARGIN committed, not the notional. With notional, a 10x
// position committing 30% of the balance read as 300% and saturated at 1, so
// almost every leveraged position looked maximally convinced.
func TestStakeFactorUsesMarginNotNotional(t *testing.T) {
	balance := 1000.0
	var p Position
	p.EntryValue, p.Leverage = 3000, 10 // 30% of balance as margin, 300% as notional
	margin := positionMargin(p, p.EntryValue)
	if margin != 300 {
		t.Fatalf("margin = %v, want 300 (3000 notional / 10x)", margin)
	}
	if got := stakeFactor(margin, balance); got >= 1 {
		t.Fatalf("30%% of balance must not saturate: %v", got)
	}
	if stakeFactor(p.EntryValue, balance) != 1 {
		t.Fatal("fixture check: the old notional reading saturated at 1")
	}
	// Binance's own margin wins when the portfolio is public.
	p.Margin = 150
	if got := positionMargin(p, p.EntryValue); got != 150 {
		t.Fatalf("reported margin must be used: %v", got)
	}
	// Unknown leverage falls back to the assumed one rather than to notional.
	var q Position
	q.EntryValue = 1000
	if got := positionMargin(q, q.EntryValue); got != 1000/assumedLeverage {
		t.Fatalf("unknown leverage must use assumedLeverage: %v", got)
	}
}

// One permanently unpriceable signal (e.g. a delisted coin) must not wipe the
// BTC benchmark for every other signal. It used to set how far back BTC was
// read; once it was older than maxBarsFetch the BTC request returned nothing
// and every other row settled with no benchmark, permanently.
func TestEvaluateSignalsKeepsBenchmarkDespiteAStaleRow(t *testing.T) {
	m, _ := newTestMonitor(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Hour)
	fresh := now.Add(-10 * time.Hour).Unix()
	ancient := now.Add(-time.Duration(maxBarsFetch+50) * time.Hour).Unix()
	if err := m.store.SaveSignals([]store.CopyTradeSignal{
		{Hour: fresh, Symbol: "SOLUSDT", Direction: "long", Price: 100},
		{Hour: ancient, Symbol: "DEADUSDT", Direction: "long", Price: 1},
	}); err != nil {
		t.Fatal(err)
	}
	m.Bars = func(_ context.Context, symbol string, limit int) ([]Bar, error) {
		if symbol == "DEADUSDT" {
			return nil, errors.New("delisted")
		}
		if limit > maxBarsFetch {
			return nil, errors.New("limit too large")
		}
		var bars []Bar
		for i := limit; i > 0; i-- {
			bars = append(bars, Bar{Time: now.Add(-time.Duration(i) * time.Hour).Unix(), Close: 100})
		}
		return bars, nil
	}
	if err := m.EvaluateSignals(ctx, now); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.store.SignalsAt(fresh)
	if len(rows) != 1 || !rows[0].Done4h {
		t.Fatalf("the fresh signal must settle: %+v", rows)
	}
	if rows[0].Bench4h == nil {
		t.Fatal("the fresh signal lost its BTC benchmark because of an unrelated stale row")
	}
}

// A transient BTC outage must not settle rows without a benchmark: they would
// be marked done and never retried.
func TestEvaluateSignalsWaitsWhenTheBenchmarkIsUnavailable(t *testing.T) {
	m, _ := newTestMonitor(t)
	now := time.Now().Truncate(time.Hour)
	at := now.Add(-10 * time.Hour).Unix()
	if err := m.store.SaveSignals([]store.CopyTradeSignal{{Hour: at, Symbol: "SOLUSDT", Direction: "long", Price: 100}}); err != nil {
		t.Fatal(err)
	}
	m.Bars = func(_ context.Context, symbol string, limit int) ([]Bar, error) {
		if symbol == benchSymbol {
			return nil, errors.New("timeout")
		}
		var bars []Bar
		for i := limit; i > 0; i-- {
			bars = append(bars, Bar{Time: now.Add(-time.Duration(i) * time.Hour).Unix(), Close: 100})
		}
		return bars, nil
	}
	if err := m.EvaluateSignals(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.store.SignalsAt(at)
	if len(rows) != 1 || rows[0].Done4h {
		t.Fatalf("with no benchmark the row must stay pending for a retry: %+v", rows)
	}
}
