package store

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEventLabelSettingsPersistenceAndIsolation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:event-label-settings?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EventLabelSettings().initTables(); err != nil {
		t.Fatal(err)
	}
	if err := s.EventLabelSettings().Set("alice", "model-a", true); err != nil {
		t.Fatal(err)
	}
	got, err := s.EventLabelSettings().Get("alice")
	if err != nil || got.ModelID != "model-a" || !got.AutoLabel {
		t.Fatalf("alice settings = %#v, err=%v", got, err)
	}
	other, err := s.EventLabelSettings().Get("bob")
	if err != nil || other.ModelID != "" || other.AutoLabel {
		t.Fatalf("bob settings = %#v, err=%v", other, err)
	}
	if err := s.EventLabelSettings().Set("alice", "", false); err != nil {
		t.Fatal(err)
	}
	got, err = s.EventLabelSettings().Get("alice")
	if err != nil || got.ModelID != "" || got.AutoLabel {
		t.Fatalf("updated alice settings = %#v, err=%v", got, err)
	}
}
