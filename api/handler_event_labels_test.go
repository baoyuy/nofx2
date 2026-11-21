package api

import (
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"net/http/httptest"
	"nofx/store"
	"strings"
	"testing"
)

func TestEventLabelRequestStrict(t *testing.T) {
	if _, err := decodeEventLabelRequest(strings.NewReader(`{"event_id":"e","model_id":"m"}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `null`, `{"event_id":"e","model_id":"m","event":{"title":"injected"}}`, `{"event_id":"e","model_id":"m"} {}`, `{"event_id":"e","model_id":""}`} {
		if _, err := decodeEventLabelRequest(strings.NewReader(raw)); err == nil {
			t.Fatal(raw)
		}
	}
}
func TestEventLabelAuthAndInvalidBeforeStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		auth bool
		body string
		code int
	}{{false, `{}`, 401}, {true, `{"event_id":"e","model_id":"m","title":"untrusted"}`, 400}, {true, strings.Repeat(" ", 2100), 400}} {
		r := gin.New()
		s := &Server{}
		r.POST("/events/labels", func(c *gin.Context) {
			if tc.auth {
				c.Set("user_id", "alice")
			}
			s.handleEventLabel(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/events/labels", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
	}
}
func TestEventLabelModelOwnershipAndEnabled(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	if err = db.AutoMigrate(&store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}
	// No credentials required for negative checks, and no model call is made.
	if err = db.Create(&store.AIModel{ID: "m", UserID: "bob", Name: "model", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.eventLabelModel("alice", "m"); err == nil {
		t.Fatal("foreign model accepted")
	}
	db.Model(&store.AIModel{}).Where("id = ?", "m").Updates(map[string]any{"user_id": "alice", "enabled": false})
	if _, err = s.eventLabelModel("alice", "m"); err == nil {
		t.Fatal("disabled model accepted")
	}
	db.Model(&store.AIModel{}).Where("id = ?", "m").Update("enabled", true)
	if _, err = s.eventLabelModel("alice", "m"); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
