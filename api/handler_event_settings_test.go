package api

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"nofx/store"
)

func TestDecodeEventLabelSettingsRequestStrict(t *testing.T) {
	if _, err := decodeEventLabelSettingsRequest(strings.NewReader(`{"model_id":"m","auto_label":true}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"model_id":"m","auto_label":true,"extra":1}`, `{"model_id":"m","auto_label":true} {}`, `{"model_id":"m"}`, `{"model_id":"m","auto_label":null}`} {
		if _, err := decodeEventLabelSettingsRequest(strings.NewReader(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestEventLabelSettingsModelOwnership(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&store.AIModel{}, &store.EventLabelSettings{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}
	if err := db.Create(&store.AIModel{ID: "m", UserID: "bob", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&store.AIModel{ID: "disabled", UserID: "alice", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m", "disabled"} {
		if model, err := s.store.AIModel().Get("alice", id); err == nil && model != nil && model.UserID == "alice" && model.Enabled {
			t.Fatal("invalid model accepted")
		}
	}
	if err := s.store.EventLabelSettings().Set("alice", "", false); err != nil {
		t.Fatal(err)
	}
}
