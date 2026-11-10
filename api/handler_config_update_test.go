package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nofx/config"
	"nofx/manager"
	"nofx/store"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Missing models deliberately prevent trader construction: these tests exercise
// persistence only, with no exchange client, trading loop or model request.
func configUpdateServer(t *testing.T) (*Server, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&store.Trader{}, &store.Exchange{}, &store.AIModel{}, &store.Strategy{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: st, traderManager: manager.NewTraderManager(), exchangeAccountStateCache: NewExchangeAccountStateCache()}, db
}

func putConfig(s *Server, route, url, user, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", user) })
	r.PUT(route, handler)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestUpdateTraderOptionalFields(t *testing.T) {
	for _, tc := range []struct {
		name, fields, strategy, prompt, symbols string
		override                                bool
	}{
		{"omitted", "", "saved", "keep prompt", "BTCUSDT", true},
		{"clear legacy", `,"custom_prompt":"","trading_symbols":"","override_base_prompt":false`, "saved", "", "", false},
		{"detach", `,"strategy_id":""`, "", "keep prompt", "BTCUSDT", true},
		{"replace", `,"strategy_id":"replacement","custom_prompt":"new","trading_symbols":"ETHUSDT","override_base_prompt":false`, "replacement", "new", "ETHUSDT", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := configUpdateServer(t)
			original := &store.Trader{ID: "bot", UserID: "alice", Name: "old", AIModelID: "missing", ExchangeID: "exchange", StrategyID: "saved", CustomPrompt: "keep prompt", TradingSymbols: "BTCUSDT", OverrideBasePrompt: true, UseAI500: true, UseOITop: true}
			if err := s.store.Trader().Create(original); err != nil {
				t.Fatal(err)
			}
			if err := db.Create([]store.Strategy{
				{ID: "saved", UserID: "alice", Name: "saved", Config: `{"strategy_type":"ai_trading","ai_config":{}}`},
				{ID: "replacement", UserID: "alice", Name: "replacement", Config: `{"strategy_type":"ai_trading","ai_config":{}}`},
			}).Error; err != nil {
				t.Fatal(err)
			}
			body := `{"name":"renamed","ai_model_id":"missing","exchange_id":"exchange"` + tc.fields + `}`
			w := putConfig(s, "/traders/:id", "/traders/bot", "alice", body, s.handleUpdateTrader)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var got store.Trader
			if err := db.First(&got, "id = ?", "bot").Error; err != nil {
				t.Fatal(err)
			}
			if got.Name != "renamed" || got.StrategyID != tc.strategy || got.CustomPrompt != tc.prompt || got.TradingSymbols != tc.symbols || got.OverrideBasePrompt != tc.override || !got.UseAI500 || !got.UseOITop {
				t.Fatalf("unexpected persisted config: %+v", got)
			}
		})
	}
}

func TestUpdateTraderRejectsForeignStrategy(t *testing.T) {
	s, db := configUpdateServer(t)
	if err := db.Create(&store.Trader{ID: "bot", UserID: "alice", Name: "old", StrategyID: "saved"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&store.Strategy{ID: "foreign", UserID: "bob", Name: "private"}).Error; err != nil {
		t.Fatal(err)
	}
	w := putConfig(s, "/traders/:id", "/traders/bot", "alice", `{"name":"new","ai_model_id":"missing","exchange_id":"exchange","strategy_id":"foreign"}`, s.handleUpdateTrader)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateExchangeAccountName(t *testing.T) {
	t.Setenv("JWT_SECRET", "config-update-test-secret-at-least-32-bytes")
	cfg := config.Get()
	oldEncryption := cfg.TransportEncryption
	cfg.TransportEncryption = false
	t.Cleanup(func() { cfg.TransportEncryption = oldEncryption })
	for _, tc := range []struct {
		name, user, fields, want string
		status                   int
	}{
		{"omitted", "alice", "", "Original", http.StatusOK},
		{"rename", "alice", `,"account_name":"  New account  "`, "New account", http.StatusOK},
		{"empty", "alice", `,"account_name":""`, "Original", http.StatusBadRequest},
		{"whitespace", "alice", `,"account_name":"   "`, "Original", http.StatusBadRequest},
		{"foreign", "bob", `,"account_name":"stolen"`, "Original", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := configUpdateServer(t)
			// Paper accounts need no credentials and never connect to a venue.
			if err := db.Create(&store.Exchange{ID: "exchange", UserID: "alice", ExchangeType: "binance_paper", AccountName: "Original", Name: "Binance Paper", Type: "cex", Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"exchanges":{"exchange":{"enabled":true%s}}}`, tc.fields)
			w := putConfig(s, "/exchanges", "/exchanges", tc.user, body, s.handleUpdateExchangeConfigs)
			if w.Code != tc.status {
				t.Fatalf("status %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			ex, err := s.store.Exchange().GetByID("alice", "exchange")
			if err != nil {
				t.Fatal(err)
			}
			if ex.AccountName != tc.want {
				t.Fatalf("name %q want %q", ex.AccountName, tc.want)
			}
		})
	}
}
