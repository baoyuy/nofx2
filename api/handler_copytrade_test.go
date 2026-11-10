package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/crypto"
	"nofx/store"
)

func copyTradeServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	priv, _, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	dataKey, err := crypto.GenerateDataKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(crypto.EnvRSAPrivateKey, priv)
	t.Setenv(crypto.EnvDataEncryptionKey, dataKey)
	cs, err := crypto.NewCryptoService()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &Server{store: st, cryptoHandler: NewCryptoHandler(cs), copyTrade: copytrade.NewMonitor(st.CopyTrade())}
	router := gin.New()
	router.GET("/copytrade/proxy", s.handleCopyTradeProxyGet)
	router.PUT("/copytrade/proxy", s.handleCopyTradeProxyPut)
	router.POST("/copytrade/follows", s.handleCopyTradeFollow)
	router.GET("/copytrade/status", s.handleCopyTradeStatus)
	return s, router
}

func TestCopyTradeProxyIsStoredEncryptedAndMasked(t *testing.T) {
	s, router := copyTradeServer(t)
	if w := call(router, "PUT", "/copytrade/proxy", "", `{"proxy":"ftp://1.2.3.4:21"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad scheme must be rejected: %d %s", w.Code, w.Body.String())
	}
	w := call(router, "PUT", "/copytrade/proxy", "", `{"proxy":"http://bob:hunter2@10.0.0.5:3128"}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "hunter2") {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	raw, _ := s.store.GetSystemConfig(copyTradeProxyKey)
	if raw == "" || strings.Contains(raw, "hunter2") || strings.Contains(raw, "10.0.0.5") {
		t.Fatalf("proxy must be stored encrypted, got %q", raw)
	}
	w = call(router, "GET", "/copytrade/proxy", "", "")
	if !strings.Contains(w.Body.String(), `http://bob:***@10.0.0.5:3128`) || strings.Contains(w.Body.String(), "hunter2") {
		t.Fatalf("read must be masked: %s", w.Body.String())
	}
	if got := s.copyTrade.Client.Status().Proxy; got != "http://bob:***@10.0.0.5:3128" {
		t.Fatalf("monitor must use the saved proxy, status shows %q", got)
	}
	if loaded, err := s.loadCopyTradeProxy(); err != nil || loaded != "http://bob:hunter2@10.0.0.5:3128" {
		t.Fatalf("reload after restart = %q, %v", loaded, err)
	}

	w = call(router, "PUT", "/copytrade/proxy", "", `{"proxy":""}`)
	if w.Code != http.StatusOK || s.copyTrade.Client.Status().Proxy != "" {
		t.Fatalf("clearing must fall back to the server IP: %s", w.Body.String())
	}
}

func TestCopyTradeFollowNeedsAnID(t *testing.T) {
	_, router := copyTradeServer(t)
	if w := call(router, "POST", "/copytrade/follows", "", `{"id":"hello"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("text without an ID must be rejected: %d", w.Code)
	}
	if w := call(router, "GET", "/copytrade/status", "", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"max_follows":100`) {
		t.Fatalf("status: %s", w.Body.String())
	}
}

func TestLeadIDFromPageURL(t *testing.T) {
	got := leadIDPattern.FindString("https://www.binance.com/zh-CN/copy-trading/lead-details/5283137603594326273?timeRange=30D")
	if got != "5283137603594326273" {
		t.Fatalf("id = %q", got)
	}
}
