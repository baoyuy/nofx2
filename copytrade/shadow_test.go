package copytrade

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"nofx/store"
)

// caller is a trader with one priced position, opened ago before now.
func caller(id, side string, ago time.Duration, now time.Time) Snapshot {
	at := now.Add(-ago).UnixMilli()
	return Snapshot{
		Follow: store.CopyTradeFollow{PortfolioID: id, Nickname: id, MarginBalance: 10000},
		Score:  TraderScore{Score: 60}, HistoryComplete: true,
		Positions: []Position{{Symbol: "SOLUSDT", Side: side, Qty: 10, EntryPrice: 100, EntryValue: 1000, MarkPrice: 100,
			OpenedAt: at, LastAddAt: at, Health: Health{Priced: true, Fresh: ago < freshWindow}}},
		Events: []Event{{Time: at, Symbol: "SOLUSDT", Side: side, Action: "open"}},
	}
}

func shadowFor(t *testing.T, snaps []Snapshot, now time.Time) Shadow {
	t.Helper()
	for _, c := range SymbolsAt(snaps, now) {
		if c.Symbol == "SOLUSDT" {
			return c.Shadow
		}
	}
	return Shadow{Direction: "neutral"}
}

func TestShadowCountsFreshCallsNotHoldings(t *testing.T) {
	now := time.Now()
	// Five traders holding longs since yesterday, two who just opened shorts.
	var snaps []Snapshot
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		snaps = append(snaps, caller(id, "long", time.Duration(30+i)*time.Hour, now))
	}
	snaps = append(snaps, caller("f", "short", 2*time.Hour, now), caller("g", "short", 5*time.Hour, now))
	coins := SymbolsAt(snaps, now)
	if coins[0].Confidence.Direction != "long" {
		t.Fatalf("holdings lean long: %+v", coins[0].Confidence)
	}
	sh := coins[0].Shadow
	if sh.Direction != "short" || sh.ShortCalls != 2 || sh.LongCalls != 0 || sh.Burst != 2 || sh.Score <= 0 {
		t.Fatalf("fresh calls are short: %+v", sh)
	}
}

func TestShadowLeavesOutNoiseAndMergesOneSource(t *testing.T) {
	now := time.Now()
	bot := caller("bot", "long", time.Hour, now)
	bot.HighFreq = true
	scalper := caller("scalp", "long", time.Hour, now)
	scalper.Style.Kind = StyleScalp
	averager := caller("avg", "long", time.Hour, now)
	averager.Style.Kind = StyleMartingale
	trapped := caller("trap", "long", time.Hour, now)
	trapped.Positions[0].Trapped = true
	if sh := shadowFor(t, []Snapshot{bot, scalper, averager, trapped}, now); sh.Direction != "neutral" || sh.LongCalls != 0 {
		t.Fatalf("noise voted: %+v", sh)
	}
	// Three accounts opening within the same minute are one source.
	same := []Snapshot{caller("x", "long", time.Hour, now), caller("y", "long", time.Hour-20*time.Second, now),
		caller("z", "long", time.Hour-40*time.Second, now)}
	if sh := shadowFor(t, same, now); sh.LongCalls != 1 {
		t.Fatalf("one source counted %d times", sh.LongCalls)
	}
	apart := []Snapshot{caller("x", "long", time.Hour, now), caller("y", "long", 2*time.Hour, now), caller("z", "long", 3*time.Hour, now)}
	if sh := shadowFor(t, apart, now); sh.LongCalls != 3 || sh.Burst != 3 {
		t.Fatalf("independent calls: %+v", sh)
	}
}

func TestShadowWeighsExitsAndChasing(t *testing.T) {
	now := time.Now()
	calls := []Snapshot{caller("x", "long", 2*time.Hour, now), caller("y", "long", 4*time.Hour, now)}
	base := shadowFor(t, calls, now)
	// Two others closed their longs in the last day: the flow is less one-sided.
	left := func(id string) Snapshot {
		return Snapshot{Follow: store.CopyTradeFollow{PortfolioID: id}, Score: TraderScore{Score: 60},
			Events: []Event{{Time: now.Add(-time.Hour).UnixMilli(), Symbol: "SOLUSDT", Side: "long", Action: "close"}}}
	}
	withExits := shadowFor(t, append(append([]Snapshot{}, calls...), left("p"), left("q")), now)
	if withExits.LongExits != 2 || withExits.Score >= base.Score || withExits.Direction != "long" {
		t.Fatalf("exits did not lower the score: %+v vs %+v", withExits, base)
	}
	// A reduce after the open is the latest word: no call from that trader.
	trimmed := caller("x", "long", 2*time.Hour, now)
	trimmed.Events = append([]Event{{Time: now.Add(-time.Hour).UnixMilli(), Symbol: "SOLUSDT", Side: "long", Action: "reduce"}}, trimmed.Events...)
	if sh := shadowFor(t, []Snapshot{trimmed}, now); sh.LongCalls != 0 || sh.LongExits != 1 {
		t.Fatalf("a trader reducing still counts as calling: %+v", sh)
	}
	// Price 5% past their entry: the move is mostly made.
	far := []Snapshot{caller("x", "long", 2*time.Hour, now), caller("y", "long", 4*time.Hour, now)}
	for i := range far {
		far[i].Positions[0].MarkPrice = 105
	}
	if sh := shadowFor(t, far, now); sh.EntryGapPct < 4.9 || sh.Score >= base.Score {
		t.Fatalf("chasing not discounted: %+v vs %+v", sh, base)
	}
}

func TestShadowUsesAccuracyOfTheMarketClass(t *testing.T) {
	now := time.Now()
	good, bad := Accuracy{Calls: 40, Hits: 30}, Accuracy{Calls: 40, Hits: 10}
	s := caller("x", "long", time.Hour, now)
	s.Accuracy = good
	s.AccuracyBy = map[string]Accuracy{ClassAlt: bad, ClassMajor: good}
	onAlt, _ := shadowVoter(s, "SOLUSDT", now)
	onMajor, _ := shadowVoter(s, "BTCUSDT", now)
	if onAlt >= onMajor {
		t.Fatalf("a trader wrong on small coins weighs %v there, %v on BTC", onAlt, onMajor)
	}
	// Too few calls in the class: the overall record stands in.
	s.AccuracyBy[ClassAlt] = Accuracy{Calls: 3}
	if w, _ := shadowVoter(s, "SOLUSDT", now); w != onMajor {
		t.Fatalf("thin class record used: %v, want %v", w, onMajor)
	}
}

func TestSignalsKeepFeaturesExcessAnd72h(t *testing.T) {
	m, _ := newTestMonitor(t)
	ctx := context.Background()
	hour := time.Now().Truncate(time.Hour).Add(-80 * time.Hour)
	clock := hour.Add(10 * time.Minute)
	// SOL rises 1% an hour, BTC 0.5%.
	m.Bars = func(_ context.Context, symbol string, limit int) ([]Bar, error) {
		step := 0.01
		if symbol == benchSymbol {
			step = 0.005
		}
		var bars []Bar
		for i := 0; i < 120; i++ {
			at := hour.Add(time.Duration(i-5) * time.Hour)
			if at.Add(time.Hour).After(clock) {
				break
			}
			bars = append(bars, Bar{Time: at.Unix(), Close: 100 * (1 + step*float64(i-5))})
		}
		return bars[max(0, len(bars)-limit):], nil
	}
	at := hour.Add(10 * time.Minute)
	for i, id := range []string{"a", "b", "c"} {
		s := caller(id, "long", time.Duration(i+1)*time.Hour, at)
		s.Follow.PositionShow, s.Follow.ROI, s.Follow.MDD, s.Follow.WinRate = true, 50, 10, 70
		s.PositionsUpdatedAt, s.FillsUpdatedAt, s.ProfileUpdatedAt, s.UpdatedAt = at, at, at, at
		for j := range s.Positions {
			s.Positions[j].Source = "public"
		}
		m.snaps[id] = &s
	}
	if err := m.RecordSignals(ctx, at); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.store.SignalsAt(hour.Unix())
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if r.Features != 1 || !r.Ranked || r.LongTraders != 3 || r.ShortTraders != 0 || r.LongShare != 1 || r.Done72h {
		t.Fatalf("features: %+v", r)
	}
	// Their positions were opened hours before `at`, so not fresh by the
	// wall clock the view uses; the shadow columns are still stored.
	if r.ShadowDirection == "" {
		t.Fatalf("shadow direction not stored: %+v", r)
	}
	clock = time.Now()
	if err := m.EvaluateSignals(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, _ = m.store.SignalsAt(hour.Unix())
	r = rows[0]
	if !r.Done72h || r.Ret72h == nil || r.Bench72h == nil || r.Bench24h == nil || r.Bench4h == nil {
		t.Fatalf("72h or benchmark missing: %+v", r)
	}
	if *r.Ret72h <= *r.Bench72h || *r.Bench72h <= 0 {
		t.Fatalf("returns: coin %v, BTC %v", *r.Ret72h, *r.Bench72h)
	}
	rates, err := m.HitRates(0)
	if err != nil {
		t.Fatal(err)
	}
	var conf72, openers HitRate
	for _, x := range rates {
		if x.Horizon == "72h" && x.Direction == "all" && x.Band == "all" && x.Group == GroupConfidence {
			conf72 = x
		}
		// Recorded hours ago: none of the holders is a fresh opener any more.
		if x.Group == GroupOpeners && x.Horizon == "24h" && x.Direction == "all" && x.Band == "0" {
			openers = x
		}
	}
	if conf72.Samples != 1 || conf72.ExcessSamples != 1 || conf72.ExcessRate != 1 || conf72.AvgExcess <= 0 || conf72.AvgExcess >= conf72.AvgReturn {
		t.Fatalf("72h excess: %+v", conf72)
	}
	if openers.Samples != 1 {
		t.Fatalf("openers group: %+v", openers)
	}
}

func TestExcessTellsAFallingMarketFromAGoodShort(t *testing.T) {
	ret := func(v float64) *float64 { return &v }
	// The coin fell 2% while BTC fell 5%: the short "hit" but lost to the market.
	sigs := []store.CopyTradeSignal{{Hour: 10 * 3600, Symbol: "X", Direction: "short", Confidence: 50, Ranked: true,
		Ret24h: ret(-0.02), Bench24h: ret(-0.05), Done24h: true}}
	for _, r := range hitRates(sigs, 0) {
		if r.Group == GroupConfidence && r.Band == "all" && r.Horizon == "24h" && r.Direction == "all" {
			if r.Hits != 1 || r.ExcessRate != 0 || r.AvgExcess > -0.029 {
				t.Fatalf("short in a falling market: %+v", r)
			}
			return
		}
	}
	t.Fatal("row missing")
}

func TestUnrankedRowsDoNotDiluteTheConfidenceRecord(t *testing.T) {
	ret := func(v float64) *float64 { return &v }
	sigs := []store.CopyTradeSignal{
		{Hour: 10 * 3600, Symbol: "A", Direction: "long", Confidence: 50, Ranked: true, Ret24h: ret(0.01)},
		{Hour: 10 * 3600, Symbol: "B", Direction: "long", Confidence: 5, Ret24h: ret(-0.01),
			ShadowDirection: "short", ShadowScore: 45},
	}
	var conf, shadow int
	for _, r := range hitRates(sigs, 0) {
		if r.Band == "all" && r.Horizon == "24h" && r.Direction == "all" {
			switch r.Group {
			case GroupConfidence:
				conf = r.Samples
			case GroupShadow:
				shadow = r.Samples
				if r.Hits != 1 {
					t.Fatalf("shadow scored on its own direction: %+v", r)
				}
			}
		}
	}
	if conf != 1 || shadow != 1 {
		t.Fatalf("confidence %d, shadow %d", conf, shadow)
	}
}

func TestOldSignalRowsAreNotAsked72hAgain(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// The table as it was before the new columns.
	if err := db.Exec(`CREATE TABLE copytrade_signals (id integer primary key autoincrement, hour integer not null,
		symbol text not null, direction text, confidence real, traders integer, price real, ret_4h real, ret_24h real,
		done_4h numeric, done_24h numeric)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO copytrade_signals (hour, symbol, direction, confidence, traders, price, done_4h, done_24h)
		VALUES (3600, 'BTCUSDT', 'long', 50, 3, 100, 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	st := store.NewCopyTradeStore(db)
	if err := st.InitTables(); err != nil {
		t.Fatal(err)
	}
	if err := st.InitTables(); err != nil { // a second start changes nothing
		t.Fatal(err)
	}
	if pending, _ := st.PendingSignals(time.Now().Unix()); len(pending) != 0 {
		t.Fatalf("old rows queued for 72h: %+v", pending)
	}
	rows, _ := st.SignalsAt(3600)
	if len(rows) != 1 || !rows[0].Ranked || !rows[0].Done72h || rows[0].Features != 0 {
		t.Fatalf("migrated row: %+v", rows)
	}
	// A new row is evaluated at 72h like any other horizon.
	if err := st.SaveSignals([]store.CopyTradeSignal{{Hour: 7200, Symbol: "ETHUSDT", Direction: "long", Done4h: true, Done24h: true}}); err != nil {
		t.Fatal(err)
	}
	if pending, _ := st.PendingSignals(time.Now().Unix()); len(pending) != 1 {
		t.Fatalf("new row not queued for 72h: %d", len(pending))
	}
}

func TestBacktestReplaysWithoutLookingAhead(t *testing.T) {
	start := time.Now().Truncate(time.Hour).Add(-10 * 24 * time.Hour)
	// SOL rises 1% an hour for the first five days, then falls; BTC is flat.
	var sol, btc []Bar
	for i := 0; i < 10*24; i++ {
		step := float64(i)
		if i > 120 {
			step = 240 - float64(i)
		}
		sol = append(sol, Bar{Time: start.Add(time.Duration(i) * time.Hour).Unix(), Close: 100 * (1 + 0.01*step)})
		btc = append(btc, Bar{Time: start.Add(time.Duration(i) * time.Hour).Unix(), Close: 50000})
	}
	fills := map[string][]Fill{}
	// Three traders open longs on day 2, hours apart, and close on day 4.
	for i, id := range []string{"a", "b", "c"} {
		open := start.Add(48*time.Hour + time.Duration(i)*2*time.Hour + 10*time.Minute).UnixMilli()
		shut := start.Add(96*time.Hour + time.Duration(i)*time.Hour + 10*time.Minute).UnixMilli()
		fills[id] = []Fill{
			{Time: open, Symbol: "SOLUSDT", Side: "BUY", PositionSide: "LONG", Qty: 10, Price: 148},
			{Time: shut, Symbol: "SOLUSDT", Side: "SELL", PositionSide: "LONG", Qty: 10, Price: 196, RealizedPnL: 480},
		}
	}
	res := Backtest(BacktestInput{Fills: fills, Bars: map[string][]Bar{"SOLUSDT": sol, benchSymbol: btc},
		From: start.Add(24 * time.Hour), To: start.Add(9 * 24 * time.Hour)})
	if res.Traders != 3 || res.Hours != 8*24+1 || res.Signals == 0 {
		t.Fatalf("result: %+v", res)
	}
	get := func(group, band, horizon string) HitRate {
		for _, r := range res.Rates {
			if r.Group == group && r.Band == band && r.Horizon == horizon && r.Direction == "all" {
				return r
			}
		}
		return HitRate{}
	}
	// One run of long calls, right at 4h and 24h, beating a flat BTC.
	conf := get(GroupConfidence, "all", "24h")
	if conf.Samples != 1 || conf.Hits != 1 || conf.ExcessRate != 1 || conf.AvgExcess <= 0 {
		t.Fatalf("confidence 24h: %+v", conf)
	}
	// 72h after the first call the price is still higher than at the call.
	if r := get(GroupConfidence, "all", "72h"); r.Samples != 1 || r.Hits != 1 {
		t.Fatalf("confidence 72h: %+v", r)
	}
	// The fresh-call reading starts with the opens, like the holdings one.
	if r := get(GroupShadow, "all", "24h"); r.Samples == 0 || r.Hits == 0 {
		t.Fatalf("shadow 24h: %+v", r)
	}
	// Nothing is called before the first open: the replay knows only the past.
	early := Backtest(BacktestInput{Fills: fills, Bars: map[string][]Bar{"SOLUSDT": sol, benchSymbol: btc},
		From: start.Add(24 * time.Hour), To: start.Add(47 * time.Hour)})
	if early.Signals != 0 {
		t.Fatalf("signals before any fill: %d", early.Signals)
	}
}
