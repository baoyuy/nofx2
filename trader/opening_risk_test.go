package trader

import (
	"math"
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"path/filepath"
	"testing"
	"time"
)

func TestSizeOpeningUsesActualQuoteAndCostsSymmetrically(t *testing.T) {
	r := store.RiskControlConfig{MaxMarginUsage: 0.5, MinRiskRewardRatio: 2, MaxTradeRiskPct: 1, MaxPortfolioRiskPct: 4}
	for _, side := range []string{"open_long", "open_short"} {
		stop, take := 98.0, 108.0
		if side == "open_short" {
			stop, take = 102, 92
		}
		d := kernel.Decision{Symbol: "SOLUSDT", Action: side, Leverage: 10, PositionSizeUSD: 3000, StopLoss: stop, TakeProfit: take}
		if err := sizeOpening(&d, r, 1000, 1000, 0, 0, 100, false); err != nil {
			t.Fatal(err)
		}
		if math.Abs(d.RiskUSD-10) > 1e-8 {
			t.Fatalf("%s risk=%f; want $10", side, d.RiskUSD)
		}
		if d.PositionSizeUSD >= 500 {
			t.Fatalf("costs not included: %f", d.PositionSizeUSD)
		}
		bad := d
		bad.TakeProfit = 103
		if side == "open_short" {
			bad.TakeProfit = 97
		}
		if err := sizeOpening(&bad, r, 1000, 1000, 0, 0, 100, false); err == nil {
			t.Fatal("accepted poor real reward/risk")
		}
	}
}

func TestSizeOpeningBookBudgetsAndInvalidValues(t *testing.T) {
	r := store.RiskControlConfig{MaxMarginUsage: 0.5, MinRiskRewardRatio: 2}
	base := kernel.Decision{Symbol: "SOLUSDT", Action: "open_long", Leverage: 10, PositionSizeUSD: 3000, StopLoss: 98, TakeProfit: 108}
	for _, tc := range []struct {
		name         string
		margin, risk float64
	}{{"margin exhausted", 500, 0}, {"stop risk exhausted", 0, 40}} {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			if sizeOpening(&d, r, 1000, 1000, tc.margin, tc.risk, 100, false) == nil {
				t.Fatal("budget bypass")
			}
		})
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), -1} {
		d := base
		d.StopLoss = v
		if sizeOpening(&d, r, 1000, 1000, 0, 0, 100, false) == nil {
			t.Fatal("invalid stop accepted")
		}
	}
	d := base
	if err := sizeOpening(&d, r, 1000, 1000, 0, 35, 100, false); err != nil {
		t.Fatal(err)
	}
	if math.Abs(d.RiskUSD-5) > 1e-8 {
		t.Fatalf("remaining portfolio risk not enforced: %f", d.RiskUSD)
	}
}

func TestAccountRiskPauseSurvivesDailyRecovery(t *testing.T) {
	state := &store.AccountRiskState{DayStartEquity: 1000, PeakEquity: 1200}
	r := store.RiskControlConfig{MaxDailyLossPct: 3, MaxAccountDrawdownPct: 10}
	if accountRiskReason(state, 960, r) == "" {
		t.Fatal("daily loss admitted")
	}
	state.DayStartEquity = 1050
	if accountRiskReason(state, 1050, r) == "" {
		t.Fatal("new day reset peak drawdown pause")
	}
	if reason := accountRiskReason(state, 1150, r); reason != "" {
		t.Fatal(reason)
	}
}

func TestVerifyFillRejectsSlippageAndOversizedFill(t *testing.T) {
	for _, action := range []string{"open_long", "open_short"} {
		side, stop, take := "long", 98.0, 108.0
		if action == "open_short" {
			side, stop, take = "short", 102, 92
		}
		cfg := store.GetDefaultStrategyConfig("en")
		cfg.RiskControl.MinRiskRewardRatio = 2
		fake := &emergencyCloseTestTrader{positions: []map[string]interface{}{{"symbol": "SOLUSDT", "side": side, "entryPrice": 100.0, "positionAmt": 1.0}}}
		at := &AutoTrader{trader: fake, config: AutoTraderConfig{StrategyConfig: &cfg}, strategyEngine: kernel.NewStrategyEngine(&cfg)}
		d := kernel.Decision{Symbol: "SOLUSDT", Action: action, StopLoss: stop, TakeProfit: take, RiskUSD: 2.15}
		if err := at.verifyOpeningFill(&d); err != nil {
			t.Fatal(err)
		}
		fake.positions[0]["positionAmt"] = 2.0
		if at.verifyOpeningFill(&d) == nil {
			t.Fatal("oversized fill accepted")
		}
		fake.positions[0]["positionAmt"] = 1.0
		if action == "open_long" {
			fake.positions[0]["entryPrice"] = 103.0
		} else {
			fake.positions[0]["entryPrice"] = 97.0
		}
		if at.verifyOpeningFill(&d) == nil {
			t.Fatal("adverse fill accepted")
		}
		if fake.invalidations != 3 {
			t.Fatal("fill check used cached position")
		}
	}
}

type openingTestTrader struct{ emergencyCloseTestTrader }

func (f *openingTestTrader) GetBalance() (map[string]interface{}, error) {
	return map[string]interface{}{"totalEquity": 1000.0, "availableBalance": 1000.0}, nil
}

func TestPrepareOpeningUsesPrimaryATRAndPortfolioStops(t *testing.T) {
	oldCandles, oldQuote := openingCandles, openingQuote
	defer func() { openingCandles = oldCandles; openingQuote = oldQuote }()
	now := time.Now()
	openingQuote = func(string, string) (*market.Quote, error) {
		return &market.Quote{Price: 100, Source: "test", AsOf: now, ObservedAt: now}, nil
	}
	openingCandles = func(symbol, exchange string, tfs []string, primary string, count int) (*market.Data, error) {
		if primary != "1h" || len(tfs) != 1 || tfs[0] != "1h" {
			t.Fatal("did not request primary timeframe")
		}
		return &market.Data{IndicatorAsOf: now, IndicatorTimeframe: "1h", TimeframeData: map[string]*market.TimeframeSeriesData{"1h": {ATR14: 2, AsOf: now}}}, nil
	}
	st, err := store.New(filepath.Join(t.TempDir(), "risk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "static"
	cfg.Indicators.Klines.PrimaryTimeframe = "1h"
	cfg.RiskControl.MinStopATR = 1.5
	cfg.RiskControl.MinRiskRewardRatio = 2
	cfg.RiskControl.MaxMarginUsage = .5
	fake := &openingTestTrader{}
	at := &AutoTrader{id: "test", store: st, trader: fake, exchange: "binance_paper", config: AutoTraderConfig{StrategyConfig: &cfg}, strategyEngine: kernel.NewStrategyEngine(&cfg)}
	for _, action := range []string{"open_long", "open_short"} {
		stop, take := 99.0, 112.0
		if action == "open_short" {
			stop, take = 101, 88
		}
		d := kernel.Decision{Symbol: "SOLUSDT", Action: action, Confidence: 90, Leverage: 3, PositionSizeUSD: 1000, StopLoss: stop, TakeProfit: take}
		if _, err := at.prepareOpening(&d, nil); err != nil {
			t.Fatal(err)
		}
		if math.Abs(100-d.StopLoss) != 3 || d.RiskUSD > 10.00001 {
			t.Fatalf("wrong ATR or risk: %+v", d)
		}
		held := []map[string]interface{}{{"symbol": "ETHUSDT", "side": "long", "positionAmt": 1.0, "markPrice": 100.0, "leverage": 3.0}}
		if _, err := at.prepareOpening(&d, held); err == nil {
			t.Fatal("unprotected existing exposure admitted")
		}
	}
	openingQuote = func(string, string) (*market.Quote, error) {
		return &market.Quote{Price: 100, AsOf: now.Add(-time.Minute), ObservedAt: now.Add(-time.Minute)}, nil
	}
	d := kernel.Decision{Symbol: "SOLUSDT", Action: "open_long", Confidence: 90, Leverage: 3, PositionSizeUSD: 1000, StopLoss: 97, TakeProfit: 112}
	if _, err := at.prepareOpening(&d, nil); err == nil {
		t.Fatal("stale execution quote admitted")
	}
}

// Decision 3078 (SUI short): sizeOpening approved $6.54 of stop risk at the
// quote; the paper venue filled 0.013% below it and the 1.01 margin on the
// budget rejected the fill as a 4.5% overrun, so the order was opened and
// emergency-closed in the same second. The fill is inside the entry slippage
// the plan is required to survive (entrySlipAllowance), so it must be kept.
func TestVerifyFillKeepsAnOrdinaryAdverseEntry(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.RiskControl.MinRiskRewardRatio = 2.8
	fill := 0.627374
	qty := 476.00471555543254
	fake := &emergencyCloseTestTrader{positions: []map[string]interface{}{{
		"symbol": "SUIUSDT", "side": "short", "entryPrice": fill, "positionAmt": qty,
	}}}
	at := &AutoTrader{trader: fake, config: AutoTraderConfig{StrategyConfig: &cfg}, strategyEngine: kernel.NewStrategyEngine(&cfg)}
	// Approved notional 298.931 at quote 0.628: 298.931 * (0.0128/0.628 + 0.0015).
	d := kernel.Decision{Symbol: "SUIUSDT", Action: "open_short", StopLoss: 0.6408, TakeProfit: 0.585, RiskUSD: 6.542}
	if err := at.verifyOpeningFill(&d); err != nil {
		t.Fatalf("ordinary fill rejected: %v", err)
	}
	// The allowance is a bound, not a removal of the check: a fill 2% worse
	// than the quote must still be refused on risk alone.
	cfg.RiskControl.MinRiskRewardRatio = 0.1
	fake.positions[0]["entryPrice"] = 0.615
	if at.verifyOpeningFill(&d) == nil {
		t.Fatal("fill outside the entry allowance accepted")
	}
}
