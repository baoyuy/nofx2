package copytrade

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"nofx/store"
)

type profileTestTransport func(*http.Request) (*http.Response, error)

func (f profileTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLateProfileCannotReviveAnUnfollow(t *testing.T) {
	m, _ := newTestMonitor(t)
	if _, err := m.Follow(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	s := m.snaps["42"]
	s.positionDue, s.fillsDue = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	m.mu.Unlock()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	base := m.Client.http.Transport
	m.Client.http.Transport = profileTestTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "performance") {
			close(started)
			<-release
		}
		return base.RoundTrip(r)
	})
	go func() { defer close(done); m.refresh(context.Background(), "42") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("profile did not start")
	}
	if err := m.Unfollow("42"); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("profile did not finish")
	}
	if n, err := m.store.CountFollows(); err != nil || n != 0 {
		t.Fatalf("late response revived follow: %d %v", n, err)
	}
}

func TestProfileUpdateCannotOverwriteRefollowedIncarnation(t *testing.T) {
	m, _ := newTestMonitor(t)
	old := store.CopyTradeFollow{PortfolioID: "42", Nickname: "old"}
	if err := m.store.SaveFollow(&old); err != nil {
		t.Fatal(err)
	}
	if err := m.store.DeleteFollow("42"); err != nil {
		t.Fatal(err)
	}
	current := store.CopyTradeFollow{PortfolioID: "42", Nickname: "new", CreatedAt: old.CreatedAt.Add(time.Second)}
	if err := m.store.SaveFollow(&current); err != nil {
		t.Fatal(err)
	}
	old.Nickname = "late response"
	if err := m.store.UpdateFollowProfile(&old); err == nil {
		t.Fatal("late profile accepted a different follow incarnation")
	}
	got, err := m.store.GetFollow("42")
	if err != nil || got.Nickname != "new" {
		t.Fatalf("new follow overwritten: %+v %v", got, err)
	}
}

func TestProfileCachePreservesOriginalExpiry(t *testing.T) {
	m, fake := newTestMonitor(t)
	if _, err := m.Follow(context.Background(), "42"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-5 * time.Hour).UnixMilli()
	m.mu.Lock()
	s := m.snaps["42"]
	s.Follow.ProfileAt, s.Follow.ProfileVersion = at, profileVersion
	s.positionDue, s.fillsDue = time.Now().Add(2*time.Hour), time.Now().Add(2*time.Hour)
	m.mu.Unlock()
	m.refresh(context.Background(), "42")
	m.mu.RLock()
	due := s.profileDue
	m.mu.RUnlock()
	if !due.Equal(time.UnixMilli(at).Add(profileInterval)) {
		t.Fatalf("cache check extended expiry: %v", due)
	}
	if fake.calls["/v1/public/future/copy-trade/lead-portfolio/performance"] != 0 {
		t.Fatal("valid profile cache should not fetch again")
	}
}
