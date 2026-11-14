package api

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"nofx/trader/binance"
)

func chartTestRequest() chartDrawingsRequest {
	r := chartDrawingsRequest{Symbol: "BTCUSDT", Interval: "1m", ModelID: "owned-model"}
	for i := 0; i < 30; i++ {
		r.Candles = append(r.Candles, binance.MarketKline{Time: 1700000000 + int64(i)*60, Open: 100, High: 110, Low: 90, Close: 100, Volume: 1})
	}
	return r
}

func TestChartRequestValidation(t *testing.T) {
	now := int64(1800000000)
	if err := validateChartRequest(chartTestRequest(), now); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*chartDrawingsRequest){
		"too few":         func(r *chartDrawingsRequest) { r.Candles = r.Candles[:29] },
		"too many":        func(r *chartDrawingsRequest) { r.Candles = make([]binance.MarketKline, 501) },
		"unknown kind":    func(r *chartDrawingsRequest) { r.Kinds = []string{"triangle"} },
		"duplicate kind":  func(r *chartDrawingsRequest) { r.Kinds = []string{"trend", "trend"} },
		"empty kinds":     func(r *chartDrawingsRequest) { r.Kinds = []string{} },
		"duplicate time":  func(r *chartDrawingsRequest) { r.Candles[1].Time = r.Candles[0].Time },
		"reverse time":    func(r *chartDrawingsRequest) { r.Candles[1].Time = r.Candles[0].Time - 1 },
		"milliseconds":    func(r *chartDrawingsRequest) { r.Candles[29].Time *= 1000 },
		"open candle":     func(r *chartDrawingsRequest) { r.Candles[29].Time = now - 59 },
		"zero time":       func(r *chartDrawingsRequest) { r.Candles[0].Time = 0 },
		"nan":             func(r *chartDrawingsRequest) { r.Candles[0].High = math.NaN() },
		"inf":             func(r *chartDrawingsRequest) { r.Candles[0].Close = math.Inf(1) },
		"zero price":      func(r *chartDrawingsRequest) { r.Candles[0].Low = 0 },
		"bad bounds":      func(r *chartDrawingsRequest) { r.Candles[0].Low = 105 },
		"negative volume": func(r *chartDrawingsRequest) { r.Candles[0].Volume = -1 },
		"interval":        func(r *chartDrawingsRequest) { r.Interval = "1M" },
		"missing model":   func(r *chartDrawingsRequest) { r.ModelID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := chartTestRequest()
			mutate(&r)
			if validateChartRequest(r, now) == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	for interval := range binance.ValidMarketIntervals {
		if chartIntervalSeconds(interval) <= 0 {
			t.Errorf("unsupported valid interval %s", interval)
		}
	}
	r := chartTestRequest()
	r.Candles[29].Time = now - 60
	if err := validateChartRequest(r, now); err != nil {
		t.Fatal("closed boundary rejected", err)
	}
}

func chartTestReply(kind, anchors string) string {
	return `{"summary":"Snapshot interpretation only","drawings":[{"kind":"` + kind + `","anchors":` + anchors + `,"reason":"Visible pivots","invalidation":"Pivot structure changes"}],"warnings":[]}`
}

func TestChartDrawingsParsing(t *testing.T) {
	r := chartTestRequest()
	low := `[{"index":0,"field":"low"}]`
	pair := `[{"index":0,"field":"low"},{"index":1,"field":"high"}]`
	triple := `[{"index":0,"field":"low"},{"index":1,"field":"high"},{"index":2,"field":"low"}]`
	for kind, n := range chartDrawingKinds {
		anchors := pair
		if n == 1 {
			anchors = low
		}
		if n == 3 {
			anchors = triple
		}
		if _, err := parseChartDrawings(chartTestReply(kind, anchors), r); err != nil {
			t.Errorf("valid %s: %v", kind, err)
		}
	}
	empty := `{"summary":"No convincing shapes","drawings":[],"warnings":[]}`
	for _, raw := range []string{empty, "```json\n" + empty + "\n```", "```\n" + empty + "\n```"} {
		a, err := parseChartDrawings(raw, r)
		if err != nil || a == nil || len(a.Drawings) != 0 {
			t.Fatalf("no-shape/fence: %v", err)
		}
	}
	cases := map[string]string{
		"negative index":        chartTestReply("support", `[{"index":-1,"field":"low"}]`),
		"outside index":         chartTestReply("support", `[{"index":30,"field":"low"}]`),
		"missing index":         chartTestReply("support", `[{"field":"low"}]`),
		"null index":            chartTestReply("support", `[{"index":null,"field":"low"}]`),
		"fraction index":        chartTestReply("support", `[{"index":1.5,"field":"low"}]`),
		"free coords":           chartTestReply("support", `[{"index":0,"field":"low","price":100}]`),
		"field":                 chartTestReply("support", `[{"index":0,"field":"open"}]`),
		"kind":                  chartTestReply("triangle", pair),
		"count":                 chartTestReply("zone", triple),
		"same index":            chartTestReply("zone", `[{"index":0,"field":"low"},{"index":0,"field":"high"}]`),
		"reversed":              chartTestReply("trend", `[{"index":2,"field":"low"},{"index":1,"field":"high"}]`),
		"flat trend":            chartTestReply("trend", `[{"index":0,"field":"low"},{"index":1,"field":"low"}]`),
		"flat zone":             chartTestReply("zone", `[{"index":0,"field":"low"},{"index":1,"field":"low"}]`),
		"collinear channel":     chartTestReply("channel", `[{"index":0,"field":"low"},{"index":1,"field":"low"},{"index":2,"field":"low"}]`),
		"pitchfork order":       chartTestReply("pitchfork", `[{"index":0,"field":"low"},{"index":2,"field":"high"},{"index":1,"field":"low"}]`),
		"zone order":            chartTestReply("zone", `[{"index":2,"field":"low"},{"index":1,"field":"high"}]`),
		"pitchfork alternation": chartTestReply("pitchfork", `[{"index":0,"field":"low"},{"index":1,"field":"low"},{"index":2,"field":"high"}]`),
		"prose":                 "Here is JSON: " + empty,
		"trailing":              empty + empty,
		"null":                  "null",
		"missing warnings":      `{"summary":"None","drawings":[]}`,
		"unknown property":      strings.TrimSuffix(empty, "}") + `,"trade":"buy"}`,
		"unclosed fence":        "```json\n" + empty,
		"summary limit":         `{"summary":"` + strings.Repeat("a", 2001) + `","drawings":[],"warnings":[]}`,
		"warning limit":         `{"summary":"None","drawings":[],"warnings":["` + strings.Repeat("a", 501) + `"]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseChartDrawings(raw, r); err == nil {
				t.Fatal("accepted invalid output")
			}
		})
	}
	r.Kinds = []string{"support"}
	if _, err := parseChartDrawings(chartTestReply("trend", pair), r); err == nil {
		t.Fatal("ignored allowed kinds")
	}
	var a chartDrawingsAnalysis
	if err := json.Unmarshal([]byte(chartTestReply("support", low)), &a); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*chartDrawingsAnalysis)
	}{
		{"drawing limit", func(a *chartDrawingsAnalysis) {
			d := a.Drawings[0]
			for len(a.Drawings) < 21 {
				a.Drawings = append(a.Drawings, d)
			}
		}},
		{"reason limit", func(a *chartDrawingsAnalysis) { a.Drawings[0].Reason = strings.Repeat("x", 1001) }},
		{"invalidation limit", func(a *chartDrawingsAnalysis) { a.Drawings[0].Invalidation = strings.Repeat("x", 1001) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var b chartDrawingsAnalysis
			raw, _ := json.Marshal(a)
			_ = json.Unmarshal(raw, &b)
			test.mutate(&b)
			raw, _ = json.Marshal(b)
			if _, err := parseChartDrawings(string(raw), r); err == nil {
				t.Fatal("accepted oversized field")
			}
		})
	}
}

func TestChartDrawingsHandlerRejectsBeforeAI(t *testing.T) {
	// A nil store would panic if any of these requests reached runRealAITest.
	s := &Server{}
	for _, tc := range []struct {
		name, body string
		auth       bool
		status     int
	}{
		{"auth", `{}`, false, 401},
		{"invalid snapshot", `{"model_id":"x"}`, true, 400},
		{"malformed", `{"candles":`, true, 400},
		{"oversize", `{"symbol":"` + strings.Repeat("x", chartDrawingBodyLimit) + `"}`, true, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/market/chart-drawings", strings.NewReader(tc.body))
			if tc.auth {
				c.Set("user_id", "user")
			}
			s.handleChartDrawings(c)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	r := chartTestRequest()
	r.Candles[29].Time = time.Now().Unix()
	raw, _ := json.Marshal(r)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", "user")
	c.Request = httptest.NewRequest("POST", "/market/chart-drawings", strings.NewReader(string(raw)))
	s.handleChartDrawings(c)
	if w.Code != 400 {
		t.Fatalf("open candle status %d", w.Code)
	}
}
