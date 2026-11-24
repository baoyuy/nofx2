package store

import (
	"math"
	"testing"
)

func TestRiskBudgetNormalization(t *testing.T) {
	c := StrategyConfig{RiskControl: RiskControlConfig{MaxTradeRiskPct: math.NaN(), MaxPortfolioRiskPct: 100, MaxDailyLossPct: -1, TradingCostPct: math.Inf(1)}}
	c.ClampLimits()
	r := c.RiskControl
	if r.MaxTradeRiskPct != 1 || r.MaxPortfolioRiskPct != 30 || r.MaxDailyLossPct != 0.01 || r.TradingCostPct != 0.15 {
		t.Fatalf("invalid budgets: %+v", r)
	}
	if c.CoinSource.AssetScope != "crypto" || c.CoinSource.PoolDirectionMode != "follow" {
		t.Fatal("unsafe default pool boundary")
	}
}
