package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"nofx/store"
)

func TestTraderUpdateRejectsGridPromptBeforePersistence(t *testing.T) {
	for _, fields := range []string{`,"custom_prompt":"Only trade confirmed signals"`, `,"override_base_prompt":true`} {
		s, db := configUpdateServer(t)
		createUpdateSafetyStrategy(t, s, "grid_trading")
		old := store.Trader{ID: "bot", UserID: "alice", Name: "original", AIModelID: "missing", ExchangeID: "exchange", StrategyID: "strategy"}
		if err := db.Create(&old).Error; err != nil {
			t.Fatal(err)
		}
		w := putConfig(s, "/traders/:id", "/traders/bot", "alice", `{"name":"changed","ai_model_id":"missing","exchange_id":"exchange"`+fields+`}`, s.handleUpdateTrader)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not supported for grid") {
			t.Fatalf("expected pre-save grid rejection: %d %s", w.Code, w.Body.String())
		}
		var got store.Trader
		if err := db.First(&got, "id = ?", "bot").Error; err != nil {
			t.Fatal(err)
		}
		if got.Name != "original" || got.CustomPrompt != "" || got.OverrideBasePrompt {
			t.Fatalf("rejected request persisted: %+v", got)
		}
	}
}

func TestTraderCreateRejectsGridPromptBeforeExchangeProbeOrStore(t *testing.T) {
	s, db := configUpdateServer(t)
	createUpdateSafetyStrategy(t, s, "grid_trading")
	model := store.AIModel{ID: "fixture-model", UserID: "alice", Name: "fixture", Provider: "deepseek", Enabled: true, APIKey: "fixture-never-used"}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", "alice") })
	r.POST("/traders", s.handleCreateTrader)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/traders", strings.NewReader(`{"name":"new","ai_model_id":"fixture-model","exchange_id":"missing-no-probe","strategy_id":"strategy","custom_prompt":"must be rejected"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not supported for grid") {
		t.Fatalf("expected grid capability rejection before exchange use: %d %s", w.Code, w.Body.String())
	}
	var n int64
	if err := db.Model(&store.Trader{}).Count(&n).Error; err != nil || n != 0 {
		t.Fatalf("rejected create left a record: %d %v", n, err)
	}
}
