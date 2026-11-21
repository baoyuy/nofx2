package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"nofx/events"
	"nofx/store"
)

func TestEventBriefingRequestStrict(t *testing.T) {
	if _, err := decodeEventBriefingRequest(strings.NewReader(`{"model_id":"m"}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `null`, `{"model_id":"m","event_id":"e"}`, `{"model_id":"m","events":[]}`, `{"model_id":"m"} {}`, strings.Repeat(" ", 2049) + `{"model_id":"m"}`} {
		if _, err := decodeEventBriefingRequest(strings.NewReader(raw)); err == nil {
			t.Fatal(raw)
		}
	}
}
func TestEventBriefingAuthAndStrictBeforeStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		method, body string
		auth         bool
		code         int
	}{{"GET", "", false, 401}, {"POST", "{}", false, 401}, {"POST", `{"model_id":"m","title":"injected"}`, true, 400}, {"POST", strings.Repeat(" ", 2050), true, 400}, {"GET", "", true, 400}} {
		r := gin.New()
		s := &Server{}
		r.Handle(tc.method, "/events/briefing", func(c *gin.Context) {
			if tc.auth {
				c.Set("user_id", "alice")
			}
			s.handleEventBriefing(c)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, "/events/briefing", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}
func TestEventBriefingCacheAndSharedLimits(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	labels := store.NewEventLabelStore(db)
	if err = labels.InitTables(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	model := &store.AIModel{ID: "m", Name: "模型"}
	key := store.EventLabel{UserID: "alice", EventID: events.BriefingEventID, ModelID: "m", Fingerprint: "fp", InputHash: "hash"}
	_, token, err := labels.Reserve(key, now)
	if err != nil {
		t.Fatal(err)
	}
	other := key
	other.EventID = "raw-event"
	other.InputHash = "new"
	_, _, err = labels.Reserve(other, now)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	briefingLimitResponse(c, err)
	if w.Code != 409 {
		t.Fatal(w.Code, err)
	}
	result := events.Briefing{ModelID: "m", InputHash: "hash", Coverage: events.BriefingCoverage{TotalEvents: 9, IncludedEvents: 1, OmittedEvents: 8, Coins: []string{"BTC"}}, Briefings: []events.CoinBriefing{{Coin: "BTC", Summary: "原始摘要", Groups: []events.BriefingGroup{}, WatchPoints: []string{}}}, Warnings: []string{}}
	raw, _ := json.Marshal(result)
	if err = labels.Finish(key, token, string(raw), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	row, _, err := labels.Reserve(key, now.Add(2*time.Second))
	if err != nil || row == nil {
		t.Fatal("cache hit must bypass quota", err)
	}
	got, err := cachedEventBriefing(row, model, "changed")
	if err != nil || !got.Cached || !got.Stale || got.Coverage.TotalEvents != 9 {
		t.Fatal(got, err)
	}
	got, err = cachedEventBriefing(row, model, "hash")
	if err != nil || got.Stale {
		t.Fatal(got, err)
	}
	if _, err = cachedEventBriefing(row, &store.AIModel{ID: "other"}, "hash"); err == nil {
		t.Fatal("cross-model cache")
	}
	for _, args := range [][4]string{{"bob", events.BriefingEventID, "m", "fp"}, {"alice", events.BriefingEventID, "other", "fp"}, {"alice", events.BriefingEventID, "m", "changed"}} {
		r, e := labels.Latest(args[0], args[1], args[2], args[3])
		if e != nil || r != nil {
			t.Fatal("cache isolation", r, e)
		}
	}
	_, _, err = labels.Reserve(other, now.Add(2*time.Second))
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	briefingLimitResponse(c, err)
	if w.Code != 429 {
		t.Fatal(w.Code, err)
	}
	// Shared rolling hourly quota includes both raw labels and briefing versions.
	for i := 1; i < 120; i++ {
		k := key
		k.InputHash = fmt.Sprint(i)
		at := now.Add(time.Duration(i)*10*time.Second + time.Second)
		_, tok, e := labels.Reserve(k, at)
		if e != nil {
			t.Fatal(i, e)
		}
		if e = labels.Finish(k, tok, "", at); e != nil {
			t.Fatal(e)
		}
	}
	other.InputHash = "hour-limit"
	_, _, err = labels.Reserve(other, now.Add(120*10*time.Second+2*time.Second))
	if err == nil {
		t.Fatal("hourly quota bypassed")
	}
}
func TestEventBriefingRoutesAndModelIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	s := &Server{}
	s.registerEventRoutes(r.Group("/api"))
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/api/events/briefing", strings.NewReader(`{"model_id":"m"}`)))
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	// Ownership/disabled/credential checks are the same shared helper as labels.
	TestEventLabelModelOwnershipAndEnabled(t)
}
