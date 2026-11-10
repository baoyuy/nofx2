package store

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestProfitStateLifecycleReopenAndPrune(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&PositionProfitState{}); err != nil {
		t.Fatal(err)
	}
	ps := &ProfitStateStore{db: db}
	seed := PositionProfitState{TraderID: "t", Symbol: "BTCUSDT", Side: "long", Lifecycle: "entry-1", EntryPrice: 100, PeakPricePct: 5, Eligible: true, BreakevenArmed: true, StopPrice: 101}
	state, fresh, err := ps.Observe(seed, true)
	if err != nil || !fresh {
		t.Fatalf("first open: fresh=%v err=%v", fresh, err)
	}
	state.PeakPricePct = 10
	if err = ps.Save(state); err != nil {
		t.Fatal(err)
	}
	same, fresh, err := ps.Observe(PositionProfitState{TraderID: "t", Symbol: "BTCUSDT", Side: "long", Lifecycle: "entry-1", EntryPrice: 100}, false)
	if err != nil || fresh || same.PeakPricePct != 10 || !same.BreakevenArmed {
		t.Fatalf("restart persistence lost: %+v fresh=%v err=%v", same, fresh, err)
	}
	_, fresh, err = ps.Observe(PositionProfitState{TraderID: "t", Symbol: "BTCUSDT", Side: "long", Lifecycle: "entry-2", EntryPrice: 101}, true)
	if err != nil || !fresh {
		t.Fatalf("new lifecycle not isolated: fresh=%v err=%v", fresh, err)
	}
	if err = ps.Prune("t", map[string]bool{}, same.UpdatedAt+1); err != nil {
		t.Fatal(err)
	}
	gone, err := ps.Get("t", "BTCUSDT", "long")
	if err != nil || gone != nil {
		t.Fatalf("closed state not pruned: %+v err=%v", gone, err)
	}
}

func TestNormalizeProfitProtectionDefaultsAndPaperIsolation(t *testing.T) {
	c := NormalizeProfitProtection(ProfitProtectionConfig{Enabled: false, BreakevenActivationPct: 0, TrailingGivebackPct: 1000})
	if c.Enabled || c.BreakevenActivationPct != 2 || c.TrailingGivebackPct != 99 {
		t.Fatalf("bad normalized defaults: %+v", c)
	}
	if ValidateProfitProtectionExchange("binance", ProfitProtectionConfig{Enabled: true}) == nil {
		t.Fatal("live exchange must reject optional protection")
	}
	if ValidateProfitProtectionExchange("binance_paper", ProfitProtectionConfig{Enabled: true}) != nil {
		t.Fatal("paper should accept protection")
	}
}
