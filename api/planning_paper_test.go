package api

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"nofx/store"
	"nofx/trader/binance"
)

func TestPlanningAcceptsBinancePaperAccountsAsBinance(t *testing.T) {
	t.Setenv("DB_PATH", filepath.Join(t.TempDir(), "data.db"))
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "paper.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err = db.AutoMigrate(&store.Exchange{}, &store.Trader{}); err != nil {
		t.Fatal(err)
	}
	for _, ex := range []store.Exchange{
		{ID: "paper-a", UserID: "alice", ExchangeType: binance.PaperExchangeType, Enabled: true},
		{ID: "paper-b", UserID: "alice", ExchangeType: binance.PaperExchangeType, Enabled: true},
		{ID: "paper-off", UserID: "alice", ExchangeType: binance.PaperExchangeType, Enabled: false},
	} {
		if err = db.Create(&ex).Error; err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}

	ex, err := s.planningWorkspace("alice", "paper-a", "binance")
	if err != nil || planningVenue(ex) != "binance" {
		t.Fatalf("paper account must open Binance markets: %v", err)
	}
	if _, err = s.planningWorkspace("alice", "paper-a", "okx"); err == nil {
		t.Fatal("paper account must not pair with another venue")
	}
	if _, err = s.planningWorkspace("bob", "paper-a", "binance"); err == nil {
		t.Fatal("another user's paper account must be refused")
	}
	if _, err = s.planningAccount("alice", "paper-off"); err == nil {
		t.Fatal("a disabled paper account must be refused")
	}
	b, _ := s.planningAccount("alice", "paper-b")
	if accountKey(ex) == accountKey(b) {
		t.Fatal("two keyless paper accounts must not share one account lock")
	}
	broker, key, err := s.planningBroker(context.Background(), "alice", "paper-a")
	if err != nil || broker.Venue() != "binance" || key != accountKey(ex) {
		t.Fatalf("paper broker must trade Binance markets under its own key: %v", err)
	}
	account, err := broker.Account(context.Background(), "BTCUSDT")
	if err != nil || account.Position != "0.00000000" || account.OpenOrders != 0 {
		t.Fatalf("fresh paper account must be flat: %+v %v", account, err)
	}
}
