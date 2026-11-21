package events

import (
	"encoding/json"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"nofx/store"
	"strings"
	"testing"
	"time"
)

const validLabelJSON = `{"event_type":"etf","summary":"事件摘要","associations":[{"coin":"BTC","relation":"direct","reason":"直接关联"}],"attention_reason":"需要关注","impact_conditions":["如果获批可能影响关注度"],"limitations":["信息不足"],"evidence_ids":["e","m"]}`

func labelEvent() Event {
	return Event{ID: "e", Title: "BTC ETF", Source: "test", Coins: []string{"ETH", "BTC"}, Markets: []Market{{ID: "m", Rules: "rules", Outcomes: []Outcome{{"Yes", .5}, {"No", .5}}}, {ID: "z"}}}
}
func TestLabelParser(t *testing.T) {
	in, _, _ := BuildLabelInput(labelEvent(), "fp")
	if _, err := ParseLabel(validLabelJSON, in); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(validLabelJSON, `"etf"`, `"trade"`, 1),
		strings.Replace(validLabelJSON, `"direct"`, `"buy"`, 1),
		strings.Replace(validLabelJSON, `"BTC"`, `"SOL"`, 1),
		strings.Replace(validLabelJSON, `["e","m"]`, `["invented"]`, 1),
		strings.Replace(validLabelJSON, `["e","m"]`, `[]`, 1),
		strings.Replace(validLabelJSON, `"reason":"直接关联"`, `"reason":"直接关联","leverage":10`, 1),
		strings.Replace(validLabelJSON, `"summary":"事件摘要"`, `"summary":"English only"`, 1),
		strings.TrimSuffix(validLabelJSON, "}") + `,"action":"buy"}`,
		validLabelJSON + ` {}`, "```json\n" + validLabelJSON + "\n```",
	} {
		if _, err := ParseLabel(raw, in); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestLabelHashStableAndMeaningful(t *testing.T) {
	e := labelEvent()
	m := &store.AIModel{ID: "id", Provider: "test"}
	fp := LabelModelFingerprint(m)
	_, h, err := BuildLabelInput(e, fp)
	if err != nil {
		t.Fatal(err)
	}
	e.Coins[0], e.Coins[1] = e.Coins[1], e.Coins[0]
	e.Markets[0], e.Markets[1] = e.Markets[1], e.Markets[0]
	e.Markets[1].Outcomes[0], e.Markets[1].Outcomes[1] = e.Markets[1].Outcomes[1], e.Markets[1].Outcomes[0]
	e.ReceivedAt = time.Now()
	e.PublishedAt = time.Now()
	_, same, _ := BuildLabelInput(e, fp)
	if same != h {
		t.Fatal("reordering or fetch timestamps changed hash")
	}
	e.Markets[1].Outcomes[0].Price = .51
	_, changed, _ := BuildLabelInput(e, fp)
	if changed == h {
		t.Fatal("price ignored")
	}
	m.UpdatedAt = time.Now()
	if LabelModelFingerprint(m) == fp {
		t.Fatal("updated model ignored")
	}
	in, _, _ := BuildLabelInput(e, fp)
	b, _ := json.Marshal(in)
	if strings.Contains(string(b), "received_at") {
		t.Fatal("timestamp leaked")
	}
}
func TestLabelMarketSelectionAndBounds(t *testing.T) {
	e := labelEvent()
	e.Markets = nil
	for i := 0; i < 14; i++ {
		e.Markets = append(e.Markets, Market{ID: string(rune('a' + i)), Rules: strings.Repeat("规", 4000)})
	}
	e.Markets[13].Changes = []PriceChange{{Outcome: "Yes", Significant: true, DeltaPP: 8}}
	in, _, err := BuildLabelInput(e, "f")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Markets) != 12 {
		t.Fatal(len(in.Markets))
	}
	found := false
	for _, m := range in.Markets {
		if m.ID == "n" {
			found = true
		}
		if len([]rune(m.Rules)) > 3000 {
			t.Fatal("unbounded rules")
		}
	}
	if !found {
		t.Fatal("significant market not prioritized")
	}
}

func TestLabelAttachCachedStaleAndCopy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	labels := store.NewEventLabelStore(db)
	if err := labels.InitTables(); err != nil {
		t.Fatal(err)
	}
	model := &store.AIModel{ID: "model", Name: "test", Provider: "test"}
	e := labelEvent()
	fp := LabelModelFingerprint(model)
	_, hash, _ := BuildLabelInput(e, fp)
	key := store.EventLabel{UserID: "u", EventID: e.ID, ModelID: model.ID, Fingerprint: fp, InputHash: hash}
	now := time.Now().UTC()
	_, token, err := labels.Reserve(key, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := labels.Finish(key, token, validLabelJSON, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	original := []Event{e}
	board := Board{Events: original}
	if err := AttachLabels(labels, "u", model, &board); err != nil {
		t.Fatal(err)
	}
	if board.Events[0].Label == nil || board.Events[0].Label.Stale || !board.Events[0].Label.Cached {
		t.Fatal("missing fresh cache")
	}
	if original[0].Label != nil {
		t.Fatal("mutated shared feed slice")
	}
	board.Events[0].Title = "changed"
	if err := AttachLabels(labels, "u", model, &board); err != nil {
		t.Fatal(err)
	}
	if !board.Events[0].Label.Stale {
		t.Fatal("missing stale flag")
	}
	if err := AttachLabels(labels, "other", model, &board); err != nil {
		t.Fatal(err)
	}
	if board.Events[0].Label != nil {
		t.Fatal("cross-user label")
	}
}
