package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nofx/kernel"
	"nofx/store"
)

func coinPoolServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &Server{store: st}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", c.GetHeader("Test-User")) })
	router.GET("/coin-pools", s.handleListCoinPools)
	router.POST("/coin-pools", s.handleCreateCoinPool)
	router.PUT("/coin-pools/:id", s.handleUpdateCoinPool)
	router.DELETE("/coin-pools/:id", s.handleDeleteCoinPool)
	router.POST("/strategies", s.handleCreateStrategy)
	return s, router
}

func call(router *gin.Engine, method, path, user, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Test-User", user)
	r.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, r)
	return w
}

func TestCoinPoolsAreOwnerScopedAndCleaned(t *testing.T) {
	_, router := coinPoolServer(t)
	w := call(router, "POST", "/coin-pools", "alice", `{"name":"喇叭口","screener":"bollinger_squeeze","interval":"1h","coins":[
		{"symbol":"btcusdt","direction":"long","score":90},{"symbol":"BTCUSDT"},{"symbol":"../etc"},{"symbol":"龙虾USDT","direction":"sideways"}]}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var pool store.CoinPool
	_ = json.Unmarshal(w.Body.Bytes(), &pool)
	if len(pool.Coins) != 2 || pool.Coins[0].Symbol != "BTCUSDT" || pool.Coins[1].Direction != "neutral" {
		t.Fatalf("pool must be normalized and deduplicated: %+v", pool.Coins)
	}
	if w := call(router, "GET", "/coin-pools", "bob", ""); strings.Contains(w.Body.String(), pool.ID) {
		t.Fatal("another user's pool was listed")
	}
	if w := call(router, "PUT", "/coin-pools/"+pool.ID, "bob", `{"name":"x","coins":[{"symbol":"ETHUSDT"}]}`); w.Code != http.StatusNotFound {
		t.Fatalf("another user must not edit the pool: %d", w.Code)
	}
	if w := call(router, "DELETE", "/coin-pools/"+pool.ID, "bob", ""); w.Code != http.StatusNotFound {
		t.Fatalf("another user must not delete the pool: %d", w.Code)
	}
	if w := call(router, "POST", "/coin-pools", "alice", `{"name":"empty","coins":[{"symbol":"???"}]}`); w.Code != http.StatusBadRequest {
		t.Fatal("a pool without valid coins must be refused")
	}
	if w := call(router, "PUT", "/coin-pools/"+pool.ID, "alice", `{"name":"改名","coins":[{"symbol":"ETHUSDT","direction":"short"}]}`); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
}

func TestStrategyTradesItsOwnCoinPool(t *testing.T) {
	s, router := coinPoolServer(t)
	alice := &store.CoinPool{Name: "pool", Coins: []store.CoinPoolCoin{{Symbol: "SOLUSDT"}, {Symbol: "DOGEUSDT"}}}
	if err := s.store.CoinPool().Create("alice", alice); err != nil {
		t.Fatal(err)
	}
	strategy := func(user string) *httptest.ResponseRecorder {
		cfg := store.GetDefaultStrategyConfig("zh")
		cfg.CoinSource.SourceType = "coin_pool"
		cfg.CoinSource.CoinPoolID = alice.ID
		cfg.CoinSource.StaticCoins = []string{"BTCUSDT"}
		raw, _ := json.Marshal(map[string]any{"name": "pool strategy", "config": cfg})
		return call(router, "POST", "/strategies", user, string(raw))
	}
	if w := strategy("bob"); w.Code != http.StatusBadRequest {
		t.Fatalf("a strategy must not reference another user's pool: %d %s", w.Code, w.Body.String())
	}
	if w := strategy("alice"); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	list, _ := s.store.Strategy().List("alice")
	var cfg *store.StrategyConfig
	for _, item := range list {
		if item.Name == "pool strategy" {
			cfg, _ = item.ParseConfig()
		}
	}
	if cfg == nil || cfg.CoinSource.SourceType != "coin_pool" || cfg.CoinSource.CoinPoolID != alice.ID ||
		strings.Join(cfg.CoinSource.StaticCoins, ",") != "SOLUSDT,DOGEUSDT" {
		t.Fatalf("strategy must keep the pool and refresh its snapshot: %+v", cfg)
	}

	previous := kernel.CoinPoolResolver
	t.Cleanup(func() { kernel.CoinPoolResolver = previous })
	kernel.CoinPoolResolver = s.store.CoinPool().Symbols
	alice.Coins = []store.CoinPoolCoin{{Symbol: "XRPUSDT"}}
	if err := s.store.CoinPool().Update("alice", alice); err != nil {
		t.Fatal(err)
	}
	coins, err := kernel.NewStrategyEngine(cfg).GetCandidateCoins()
	if err != nil || len(coins) != 1 || coins[0].Symbol != "XRPUSDT" {
		t.Fatalf("a running strategy must pick up pool edits: %+v %v", coins, err)
	}
	if err := s.store.CoinPool().Delete("alice", alice.ID); err != nil {
		t.Fatal(err)
	}
	coins, _ = kernel.NewStrategyEngine(cfg).GetCandidateCoins()
	if len(coins) != 2 || coins[0].Symbol != "SOLUSDT" {
		t.Fatalf("a deleted pool must fall back to the saved snapshot: %+v", coins)
	}
}
