package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPISchedulerStatusRequiresAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{router: gin.New()}
	s.setupRoutes()
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/api-scheduler/status", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unprotected global scheduler counters: %d", w.Code)
	}
}
func TestAPISchedulerStatusShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	s := &Server{}
	router.GET("/status", s.handleAPISchedulerStatus)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/status", nil))
	var body struct {
		Scope     string            `json:"scope"`
		Providers []json.RawMessage `json:"providers"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Scope != "process" || body.Providers == nil {
		t.Fatalf("unexpected diagnostic response: %s", w.Body.String())
	}
}
