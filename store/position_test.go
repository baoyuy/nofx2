package store

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetOpenPositionBySymbolMatchesSideCaseInsensitively(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	positions := NewPositionStore(db)
	if err := positions.InitTables(); err != nil {
		t.Fatalf("init position table: %v", err)
	}

	entryTime := time.Now().Add(-5 * time.Minute).UnixMilli()
	if err := positions.Create(&TraderPosition{
		TraderID:   "trader-1",
		Symbol:     "AAVEUSDT",
		Side:       "LONG",
		Quantity:   0.27,
		EntryPrice: 88.519,
		EntryTime:  entryTime,
	}); err != nil {
		t.Fatalf("create position: %v", err)
	}

	got, err := positions.GetOpenPositionBySymbol("trader-1", "AAVEUSDT", "long")
	if err != nil {
		t.Fatalf("get open position: %v", err)
	}
	if got == nil {
		t.Fatal("expected open position")
	}
	if got.EntryTime != entryTime {
		t.Fatalf("entry time mismatch: got %d want %d", got.EntryTime, entryTime)
	}
}

func TestGetClosedPositionsByTraderFiltersIncludesLegacyAutopilotIDs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	positions := NewPositionStore(db)
	if err := positions.InitTables(); err != nil {
		t.Fatalf("init position table: %v", err)
	}

	now := time.Now().UnixMilli()
	rows := []*TraderPosition{
		{
			TraderID:     "current-trader",
			Symbol:       "xyz:SP500",
			Side:         "LONG",
			Quantity:     1,
			EntryPrice:   100,
			EntryTime:    now - 3000,
			ExitPrice:    101,
			ExitTime:     now - 2000,
			RealizedPnL:  1,
			Status:       "CLOSED",
			CreatedAt:    now - 3000,
			UpdatedAt:    now - 2000,
			CloseReason:  "sync",
			ExchangeType: "hyperliquid",
		},
		{
			TraderID:     "exchange_user-123_claw402_111",
			Symbol:       "AAVEUSDT",
			Side:         "LONG",
			Quantity:     2,
			EntryPrice:   50,
			EntryTime:    now - 5000,
			ExitPrice:    49,
			ExitTime:     now - 4000,
			RealizedPnL:  -2,
			Status:       "CLOSED",
			CreatedAt:    now - 5000,
			UpdatedAt:    now - 4000,
			CloseReason:  "sync",
			ExchangeType: "hyperliquid",
		},
		{
			TraderID:     "exchange_other-user_claw402_222",
			Symbol:       "LITUSDT",
			Side:         "LONG",
			Quantity:     3,
			EntryPrice:   10,
			EntryTime:    now - 7000,
			ExitPrice:    12,
			ExitTime:     now - 6000,
			RealizedPnL:  6,
			Status:       "CLOSED",
			CreatedAt:    now - 7000,
			UpdatedAt:    now - 6000,
			CloseReason:  "sync",
			ExchangeType: "hyperliquid",
		},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("create position: %v", err)
		}
	}

	got, err := positions.GetClosedPositionsByTraderFilters(
		[]string{"current-trader"},
		[]string{"%_user-123_claw402_%"},
		100,
	)
	if err != nil {
		t.Fatalf("get closed positions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected current + same-user legacy positions, got %d", len(got))
	}

	stats, err := positions.GetFullStatsByTraderFilters(
		[]string{"current-trader"},
		[]string{"%_user-123_claw402_%"},
		0,
	)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.TotalTrades != 2 || stats.TotalPnL != -1 {
		t.Fatalf("unexpected stats: trades=%d pnl=%.2f", stats.TotalTrades, stats.TotalPnL)
	}
}

func TestFullStatsAreNetOfFees(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	positions := NewPositionStore(db)
	if err := positions.InitTables(); err != nil {
		t.Fatalf("init position table: %v", err)
	}

	now := time.Now().UnixMilli()
	// Gross +10 with 3 in fees is a +7 win; gross +1 with 2 in fees is a -1 loss.
	for i, row := range []struct{ pnl, fee float64 }{{10, 3}, {1, 2}} {
		if err := db.Create(&TraderPosition{
			TraderID: "t", Symbol: "BTCUSDT", Side: "LONG", Quantity: 1, EntryPrice: 100,
			EntryTime: now - int64(10-i), ExitTime: now - int64(5-i), RealizedPnL: row.pnl, Fee: row.fee,
			Status: "CLOSED",
		}).Error; err != nil {
			t.Fatalf("create position: %v", err)
		}
	}

	stats, err := positions.GetFullStats("t", 1000)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.TotalPnL != 6 || stats.TotalFee != 5 {
		t.Fatalf("net pnl/fee = %.2f/%.2f, want 6/5 (gross 11)", stats.TotalPnL, stats.TotalFee)
	}
	if stats.WinTrades != 1 || stats.LossTrades != 1 {
		t.Fatalf("wins/losses = %d/%d, want 1/1 after fees", stats.WinTrades, stats.LossTrades)
	}
}

func TestCalculateMaxDrawdownUsesRealBaseline(t *testing.T) {
	// +50 then -100: peak 550, trough 450 on a 500 account → 100/550 ≈ 18.18%.
	pnls := []float64{50, -100}

	got := calculateMaxDrawdownFromPnls(pnls, 500)
	if got < 18.1 || got > 18.3 {
		t.Fatalf("expected ~18.18%% drawdown on a 500 baseline, got %.2f", got)
	}

	// Unknown baseline falls back to the neutral 10k curve: 100/10050 ≈ 1%.
	fallback := calculateMaxDrawdownFromPnls(pnls, 0)
	if fallback < 0.9 || fallback > 1.1 {
		t.Fatalf("expected ~1%% drawdown on the 10k fallback, got %.2f", fallback)
	}

	if calculateMaxDrawdownFromPnls(nil, 500) != 0 {
		t.Fatalf("no trades must mean zero drawdown")
	}
}
