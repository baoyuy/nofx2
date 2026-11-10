package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nofx/mirror"
	"nofx/store"
)

func mirrorServer(t *testing.T) (*Server, *gin.Engine, map[string]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ids := map[string]string{}
	mk := func(name, typ, key string) {
		id, err := st.Exchange().Create("alice", typ, name, true, key, key, "", false, "", false, false, "", "", "", "", "", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	mk("ai", "binance_paper", "")
	mk("paper2", "binance_paper", "")
	mk("real", "binance", "k1")
	mk("real-again", "binance", "k1") // the same API key: the same account
	mk("other", "binance", "k2")
	for _, tr := range []store.Trader{
		{ID: "ai-trader", UserID: "alice", Name: "AI", ExchangeID: ids["ai"], AIModelID: "m"},
		{ID: "busy-trader", UserID: "alice", Name: "Busy", ExchangeID: ids["real-again"], AIModelID: "m", IsRunning: true},
	} {
		tr := tr
		if err := st.Trader().Create(&tr); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{store: st, mirrorCtx: context.Background()}
	s.mirror = mirror.New(st.Mirror(), mirror.Deps{
		Leader:   func(string) (mirror.Account, error) { return nil, errors.New("not loaded") },
		Follower: s.mirrorFollower,
		Busy:     s.mirrorBusy,
	})
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", c.GetHeader("Test-User")) })
	router.GET("/mirror/accounts", s.handleMirrorAccounts)
	router.GET("/mirror/links", s.handleMirrorLinks)
	router.POST("/mirror/links", s.handleMirrorCreate)
	return s, router, ids
}

func TestMirrorCreateRefusesUnsafeAccounts(t *testing.T) {
	s, router, ids := mirrorServer(t)
	body := func(exchange, extra string) string {
		return `{"leader_id":"ai-trader","exchange_id":"` + exchange + `","size_mode":"fixed","fixed_margin":20,
			"leverage_mode":"follow","max_leverage":10` + extra + `}`
	}
	cases := []struct {
		name, user, body, want string
	}{
		{"another user's trader", "bob", body(ids["paper2"], ""), "AI trader not found"},
		{"bad settings", "alice", strings.Replace(body(ids["paper2"], ""), `"fixed_margin":20`, `"fixed_margin":0`, 1), "fixed margin"},
		{"the AI's own account", "alice", body(ids["ai"], ""), "own account"},
		{"real without confirming", "alice", body(ids["other"], ""), "confirm_real"},
		{"real used by a running trader", "alice", body(ids["real"], `,"confirm_real":true`), "Busy is running"},
		{"leader not loaded", "alice", body(ids["paper2"], ""), "Could not start"},
	}
	for _, c := range cases {
		w := call(router, "POST", "/mirror/links", c.user, c.body)
		if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	// A link that could not start is not kept.
	if links, _ := s.store.Mirror().Links("alice"); len(links) != 0 {
		t.Fatalf("links kept: %+v", links)
	}
	w := call(router, "GET", "/mirror/accounts", "alice", "")
	if !strings.Contains(w.Body.String(), `"used_by":["Busy"]`) || !strings.Contains(w.Body.String(), `"paper":true`) {
		t.Fatalf("accounts = %s", w.Body.String())
	}
}
