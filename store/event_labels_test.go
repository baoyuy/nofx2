package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEventLabelsBudgetPersistenceAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labels.db")
	_, db := openEventHistory(t, path)
	s := NewEventLabelStore(db)
	if err := s.InitTables(); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	key := EventLabel{UserID: "u", EventID: "e", ModelID: "m", Fingerprint: "f", InputHash: "h"}
	_, token, err := s.Reserve(key, at)
	if err != nil {
		t.Fatal(err)
	}
	var limit *EventLabelLimit
	if _, _, err = s.Reserve(key, at); !errors.As(err, &limit) || !limit.Busy {
		t.Fatal("missing active reservation", err)
	}
	if err = s.Finish(key, token, `{"summary":"已验证"}`, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	cached, _, err := s.Reserve(key, at.Add(2*time.Second))
	if err != nil || cached == nil {
		t.Fatal("cache must bypass cooldown", err)
	}
	_, db2 := openEventHistory(t, path)
	reopened := NewEventLabelStore(db2)
	row, err := reopened.Latest("u", "e", "m", "f")
	if err != nil || row == nil {
		t.Fatal("not persisted", err)
	}
	for _, k := range []EventLabel{{UserID: "v", EventID: "e", ModelID: "m", Fingerprint: "f"}, {UserID: "u", EventID: "x", ModelID: "m", Fingerprint: "f"}, {UserID: "u", EventID: "e", ModelID: "x", Fingerprint: "f"}, {UserID: "u", EventID: "e", ModelID: "m", Fingerprint: "x"}} {
		row, err := s.Latest(k.UserID, k.EventID, k.ModelID, k.Fingerprint)
		if err != nil || row != nil {
			t.Fatal("isolation", err)
		}
	}
	key.InputHash = "changed"
	if _, _, err = s.Reserve(key, at.Add(2*time.Second)); !errors.As(err, &limit) || limit.Busy {
		t.Fatal("missing cooldown", err)
	}
	for i := 1; i < eventLabelHourlyLimit; i++ {
		now := at.Add(time.Duration(i) * eventLabelCooldown)
		key.InputHash = fmt.Sprint(i)
		_, token, err = s.Reserve(key, now)
		if err != nil {
			t.Fatal(i, err)
		}
		// Failure release stores no success, but still consumes the attempt.
		if err = s.Finish(key, token, "", now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	key.InputHash = "over"
	if _, _, err = s.Reserve(key, at.Add(eventLabelHourlyLimit*eventLabelCooldown)); !errors.As(err, &limit) || limit.RetryAfter < 1100 {
		t.Fatal("missing hour cap", err)
	}
	if row, err := s.Latest("u", "e", "m", "f"); err != nil || row.InputHash != "h" {
		t.Fatal("failure stored as success", err)
	}
}
func TestEventLabelsConcurrentLeaseRecovery(t *testing.T) {
	_, db := openEventHistory(t, filepath.Join(t.TempDir(), "labels.db"))
	s := NewEventLabelStore(db)
	if err := s.InitTables(); err != nil {
		t.Fatal(err)
	}
	key := EventLabel{UserID: "u", EventID: "e", ModelID: "m", Fingerprint: "f", InputHash: "h"}
	now := time.Now().UTC()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := []string{}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, token, err := NewEventLabelStore(db).Reserve(key, now)
			if err == nil {
				mu.Lock()
				wins = append(wins, token)
				mu.Unlock()
			} else {
				var limit *EventLabelLimit
				if !errors.As(err, &limit) {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if len(wins) != 1 {
		t.Fatalf("winners %d", len(wins))
	}
	var count int64
	db.Model(&EventLabelAttempt{}).Count(&count)
	if count != 1 {
		t.Fatal("double spent", count)
	}
	_, token, err := s.Reserve(key, now.Add(6*time.Minute))
	if err != nil {
		t.Fatal("expired lease not recovered", err)
	}
	if err = s.Finish(key, wins[0], `{}`, now.Add(6*time.Minute)); err == nil {
		t.Fatal("late worker accepted")
	}
	if err = s.Finish(key, token, "", now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
}
