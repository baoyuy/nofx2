package events

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func briefingBoard(now time.Time) Board {
	return Board{Watches: []Watch{{Coin: "BTC"}, {Coin: "ETH"}}, Events: []Event{
		{ID: "a", Kind: "news", Title: "比特币新闻", Source: "news", Publisher: "媒体", Coins: []string{"BTC"}, PublishedAt: now.Add(-time.Hour), ReceivedAt: now, URL: "https://example.com/a?utm_source=x"},
		{ID: "b", Kind: "community", Title: "社区观点", Source: "community", Coins: []string{"ETH"}, PublishedAt: now.Add(-2 * time.Hour), ReceivedAt: now, URL: "https://example.com/a"},
	}}
}
func TestBriefingHashAndProvenance(t *testing.T) {
	now := time.Now().UTC()
	board := briefingBoard(now)
	in, c, _, hash, err := BuildBriefingInput(board, "fp", now)
	if err != nil || c.IncludedEvents != 2 {
		t.Fatal(c, err)
	}
	if in.Evidence[0].SharedEvidence == "" || in.Evidence[0].SharedEvidence != in.Evidence[1].SharedEvidence {
		t.Fatal("shared evidence lost")
	}
	board.Events[0], board.Events[1] = board.Events[1], board.Events[0]
	for i := range board.Events {
		board.Events[i].ReceivedAt = now.Add(time.Second)
		board.Events[i].Label = &Label{Analysis: LabelAnalysis{Summary: "不能进入输入"}}
	}
	_, _, _, same, _ := BuildBriefingInput(board, "fp", now.Add(time.Minute))
	if same != hash {
		t.Fatal("fetch/order/prior label changes hash")
	}
	_, _, _, different, _ := BuildBriefingInput(board, "other", now)
	if different == hash {
		t.Fatal("model fingerprint omitted")
	}
	board.Events[0].Summary = "更新报道"
	_, _, _, different, _ = BuildBriefingInput(board, "fp", now)
	if different == hash {
		t.Fatal("content omitted")
	}
	board.Events = append(board.Events, board.Events[0])
	_, c, _, _, _ = BuildBriefingInput(board, "fp", now)
	if c.IncludedEvents != 2 || c.OmittedEvents != 1 || len(board.Events) != 3 {
		t.Fatal(c)
	}
	board.Events[0].ReceivedAt = now.Add(-16 * time.Minute)
	board.Events[2].ReceivedAt = board.Events[0].ReceivedAt
	_, c, _, _, _ = BuildBriefingInput(board, "fp", now)
	if c.IncludedEvents != 1 {
		t.Fatal("stale input accepted", c)
	}
}
func TestBriefingCapsAndDiversity(t *testing.T) {
	now := time.Now().UTC()
	board := briefingBoard(now)
	board.Events = nil
	for i := 0; i < 100; i++ {
		coin := "BTC"
		source := "news"
		if i == 99 {
			coin = "ETH"
			source = "community"
		}
		board.Events = append(board.Events, Event{ID: fmt.Sprintf("e%03d", i), Kind: "news", Title: strings.Repeat("新", 500), Summary: strings.Repeat("文", 600), Source: source, Coins: []string{coin}, ReceivedAt: now, PublishedAt: now.Add(-time.Duration(i) * time.Minute)})
	}
	in, c, w, _, err := BuildBriefingInput(board, "fp", now)
	b, _ := json.Marshal(in)
	if err != nil || len(b) > BriefingMaxBytes || len(in.Evidence) > 60 || len(c.Coins) != 2 || c.OmittedEvents == 0 || len(w) == 0 {
		t.Fatal(c, len(b), err)
	}
	for _, e := range in.Evidence {
		if len([]rune(e.Title)) > 300 || len([]rune(e.Summary)) > 400 {
			t.Fatal("text cap")
		}
	}
	if in.Evidence[0].EventID != "e000" {
		t.Fatal("recent not prioritized")
	}
	for i := 0; i < 30; i++ {
		board.Watches = append(board.Watches, Watch{Coin: fmt.Sprintf("C%02d", i)})
	}
	_, c, w, _, _ = BuildBriefingInput(board, "fp", now)
	if len(c.Coins) > 20 || !strings.Contains(strings.Join(w, ""), "超过20") {
		t.Fatal(c, w)
	}
}
func TestBriefingParserStrict(t *testing.T) {
	now := time.Now()
	in, _, _, _, _ := BuildBriefingInput(briefingBoard(now), "fp", now)
	valid := `{"briefings":[{"coin":"BTC","summary":"报道尚待核实","groups":[{"stance":"unconfirmed","title":"待确认报道","reason":"资料有限","conditions":[],"limitations":["未核实"],"evidence_ids":["a"]}],"watch_points":[]},{"coin":"ETH","summary":"社区观点不代表事实","groups":[],"watch_points":[]}]}`
	if _, err := ParseBriefing(valid, in); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"summary":"报道尚待核实"`, `"order":"buy","summary":"报道尚待核实"`, 1),
		strings.Replace(valid, `"BTC"`, `"DOGE"`, 1), strings.Replace(valid, `"a"`, `"b"`, 1), strings.Replace(valid, `"a"`, `"unknown"`, 1),
		strings.Replace(valid, `"unconfirmed"`, `"buy"`, 1), strings.Replace(valid, `"报道尚待核实"`, `"English only"`, 1), valid + ` {}`, `null`,
	} {
		if _, err := ParseBriefing(raw, in); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}
