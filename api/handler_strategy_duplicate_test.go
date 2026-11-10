package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"nofx/store"
)

// The strategy page posts without a body; the copy must still be created and
// named after its source. A given name is used as is, and nobody can copy
// another user's strategy.
func TestDuplicateStrategyWithAndWithoutName(t *testing.T) {
	s, router := coinPoolServer(t)
	router.POST("/strategies/:id/duplicate", s.handleDuplicateStrategy)
	cfg := store.GetDefaultStrategyConfig("zh")
	raw, _ := json.Marshal(map[string]any{"name": "喇叭口", "config": cfg})
	w := call(router, "POST", "/strategies", "alice", string(raw))
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var created struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	names := map[string]bool{}
	for _, body := range []string{"", `{}`, `{"name":"喇叭口（副本）"}`} {
		w := call(router, "POST", "/strategies/"+created.ID+"/duplicate", "alice", body)
		if w.Code != http.StatusOK {
			t.Fatalf("duplicate with body %q: %d %s", body, w.Code, w.Body.String())
		}
	}
	list, _ := s.store.Strategy().List("alice")
	for _, item := range list {
		names[item.Name] = true
	}
	if !names["喇叭口 (copy)"] || !names["喇叭口（副本）"] || len(list) != 4 {
		t.Fatalf("copies = %v (%d strategies)", names, len(list))
	}
	if w := call(router, "POST", "/strategies/"+created.ID+"/duplicate", "bob", ""); w.Code == http.StatusOK {
		t.Fatal("another user must not copy the strategy")
	}
}

func TestMarketBoardSymbolAcceptsNonLatinListings(t *testing.T) {
	for _, ok := range []string{"BTC", "1000PEPEUSDT", "哈基米", "哈基米USDT"} {
		if !marketBoardSymbol.MatchString(ok) {
			t.Errorf("%q must be accepted", ok)
		}
	}
	for _, bad := range []string{"", "../etc", "BTC USDT", "a/b", "xyz:SP500"} {
		if marketBoardSymbol.MatchString(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
