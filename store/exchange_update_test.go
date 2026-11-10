package store

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestExchangeUpdateOptionalAccountName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "exchange.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := db.AutoMigrate(&Exchange{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Exchange{ID: "ex", UserID: "alice", AccountName: "Original", Name: "Binance Paper", ExchangeType: "binance_paper", Type: "cex"}).Error; err != nil {
		t.Fatal(err)
	}
	s := NewExchangeStore(db)
	update := func(user string, name ...*string) error {
		return s.Update(user, "ex", true, "", "", "", false, "", false, false, "", "", "", "", "", "", 0, name...)
	}
	assertName := func(want string) {
		t.Helper()
		ex, err := s.GetByID("alice", "ex")
		if err != nil || ex.AccountName != want {
			t.Fatalf("account = %+v, err = %v; want name %q", ex, err, want)
		}
	}
	if err := update("alice"); err != nil {
		t.Fatal(err)
	} // Existing callers remain compatible.
	assertName("Original")
	if err := update("alice", nil); err != nil {
		t.Fatal(err)
	}
	assertName("Original")
	name := "  Renamed  "
	if err := update("alice", &name); err != nil {
		t.Fatal(err)
	}
	assertName("Renamed")
	name = "  "
	if err := update("alice", &name); err == nil {
		t.Fatal("empty name accepted")
	}
	assertName("Renamed")
	name = "stolen"
	if err := update("bob", &name); err == nil {
		t.Fatal("foreign account renamed")
	}
	assertName("Renamed")
	if err := s.UpdateAccountName("alice", "ex", ""); err == nil {
		t.Fatal("direct empty rename accepted")
	}
	if err := s.UpdateAccountName("bob", "ex", "stolen"); err == nil {
		t.Fatal("direct foreign rename accepted")
	}
	assertName("Renamed")
}
