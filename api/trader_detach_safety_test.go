package api

import (
	"net/http"
	"testing"

	"nofx/store"
)

func TestRunningTraderCannotBeDetachedAsImplicitStop(t *testing.T) {
	s, db := configUpdateServer(t)
	original := store.Trader{ID: "bot", UserID: "alice", Name: "original", AIModelID: "missing", ExchangeID: "exchange", StrategyID: "saved", IsRunning: true}
	if err := db.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	w := putConfig(s, "/traders/:id", "/traders/bot", "alice", `{"name":"changed","ai_model_id":"missing","exchange_id":"exchange","strategy_id":""}`, s.handleUpdateTrader)
	if w.Code != http.StatusConflict {
		t.Fatalf("running detach should require explicit stop: %d %s", w.Code, w.Body.String())
	}
	var got store.Trader
	if err := db.First(&got, "id = ?", "bot").Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != original.Name || got.StrategyID != original.StrategyID || !got.IsRunning {
		t.Fatalf("rejected detach changed trading configuration: %+v", got)
	}
}
