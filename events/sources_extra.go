package events

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// extraEntry caches one extra-source result with its TTL anchor.
type extraEntry struct {
	at       time.Time
	events   []Event
	warnings []string
}

func (f *Feed) extraGet(key string, ttl time.Duration, now time.Time) (extraEntry, bool) {
	f.extraMu.Lock()
	defer f.extraMu.Unlock()
	e, ok := f.extraCache[key]
	if ok && now.Sub(e.at) < ttl {
		return e, true
	}
	return extraEntry{}, false
}

func (f *Feed) extraPut(key string, e extraEntry) {
	f.extraMu.Lock()
	defer f.extraMu.Unlock()
	if len(f.extraCache) >= 256 {
		f.extraCache = map[string]extraEntry{}
	}
	f.extraCache[key] = e
}

func shortID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

// globalEvents runs the market-wide / calendar sources once per build and
// attaches each event to the coins (and their traders) in the current dynamic
// scope. Nothing here is bound to a fixed coin list.
func (f *Feed) globalEvents(ctx context.Context, watches []Watch, now time.Time) ([]Event, []string) {
	if !f.extraEnabled {
		return nil, nil
	}
	coins := make([]string, 0, len(watches))
	tradersByCoin := map[string][]TraderRef{}
	for _, w := range watches {
		if w.Coin == "" {
			continue
		}
		coins = append(coins, w.Coin)
		for _, ref := range w.Traders {
			tradersByCoin[w.Coin] = appendRef(tradersByCoin[w.Coin], ref)
		}
	}
	type res struct {
		evs []Event
		w   []string
	}
	fns := []func(context.Context, time.Time, []string, map[string][]TraderRef) ([]Event, []string){
		f.macroEvents, f.earningsEvents, f.cryptoCalendarEvents, f.fearGreedEvents, f.okxAnnouncements, f.bybitAnnouncements, f.rssEvents, f.cryptoPanicEvents, f.telegramEvents,
	}
	ch := make(chan res, len(fns))
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Add(1)
		go func(fn func(context.Context, time.Time, []string, map[string][]TraderRef) ([]Event, []string)) {
			defer wg.Done()
			evs, w := fn(ctx, now, coins, tradersByCoin)
			ch <- res{evs, w}
		}(fn)
	}
	wg.Wait()
	close(ch)
	var out []Event
	var warns []string
	for r := range ch {
		out = append(out, r.evs...)
		warns = append(warns, r.w...)
	}
	return out, warns
}

// ---------- Macro calendar (free ForexFactory weekly JSON) ----------

type ffEvent struct {
	Title    string `json:"title"`
	Country  string `json:"country"`
	Date     string `json:"date"`
	Impact   string `json:"impact"`
	Forecast string `json:"forecast"`
	Previous string `json:"previous"`
	Actual   string `json:"actual"`
}

func (f *Feed) macroEvents(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if e, ok := f.extraGet("macro", time.Hour, now); ok {
		return e.events, e.warnings
	}
	var raw []ffEvent
	if err := f.get(ctx, f.macroURL, &raw); err != nil {
		w := []string{"宏观日历不可用: " + err.Error()}
		f.extraPut("macro", extraEntry{at: now, warnings: w})
		return nil, w
	}
	var traders []TraderRef
	for _, c := range coins {
		for _, ref := range tradersByCoin[c] {
			traders = appendRef(traders, ref)
		}
	}
	out := []Event{}
	for _, e := range raw {
		if !strings.EqualFold(strings.TrimSpace(e.Impact), "High") || !strings.EqualFold(strings.TrimSpace(e.Country), "USD") {
			continue
		}
		when, err := time.Parse(time.RFC3339, strings.TrimSpace(e.Date))
		if err != nil || when.Before(now.Add(-time.Hour)) || when.After(now.Add(7*24*time.Hour)) {
			continue
		}
		summary := ""
		if e.Forecast != "" || e.Previous != "" || e.Actual != "" {
			summary = fmt.Sprintf("实际 %s / 预期 %s / 前值 %s", orDash(e.Actual), orDash(e.Forecast), orDash(e.Previous))
		}
		if e.Actual != "" && e.Forecast != "" {
			if a, ok1 := parseNum(e.Actual); ok1 {
				if f, ok2 := parseNum(e.Forecast); ok2 {
					switch {
					case a > f:
						summary += "（高于预期）"
					case a < f:
						summary += "（低于预期）"
					default:
						summary += "（符合预期）"
					}
				}
			}
		}
		ev := Event{
			ID: "macro:" + shortID(e.Title, e.Date, e.Country), Kind: "macro", Source: "Macro",
			Title: fmt.Sprintf("%s（%s 高影响）", e.Title, e.Country), Summary: summary,
			PublishedAt: when, ReceivedAt: now, EndAt: &when,
			Markets: []Market{}, Coins: append([]string{}, coins...), Traders: traders,
		}
		out = append(out, ev)
	}
	res := extraEntry{at: now, events: out}
	f.extraPut("macro", res)
	return out, nil
}

func parseNum(s string) (float64, bool) {
	t := strings.ToUpper(strings.TrimSpace(s))
	if t == "" || t == "—" {
		return 0, false
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(t, "K"):
		mult, t = 1e3, strings.TrimSuffix(t, "K")
	case strings.HasSuffix(t, "M"):
		mult, t = 1e6, strings.TrimSuffix(t, "M")
	case strings.HasSuffix(t, "B"):
		mult, t = 1e9, strings.TrimSuffix(t, "B")
	case strings.HasSuffix(t, "%"):
		t = strings.TrimSuffix(t, "%")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil {
		return 0, false
	}
	return v * mult, true
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// ---------- Earnings calendar (free Alpha Vantage EARNINGS_CALENDAR CSV) ----------

func (f *Feed) earningsEvents(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if e, ok := f.extraGet("earn", 12*time.Hour, now); ok {
		return e.events, e.warnings
	}
	scope := map[string]bool{}
	for _, c := range coins {
		scope[strings.ToUpper(c)] = true
	}
	key := envOr("ALPHAVANTAGE_API_KEY", "demo")
	url := f.earnURL
	if strings.Contains(url, "?") {
		url += "&apikey=" + key
	} else {
		url += "?apikey=" + key
	}
	text, err := f.getText(ctx, url)
	if err != nil {
		w := []string{"财报日历不可用: " + err.Error()}
		f.extraPut("earn", extraEntry{at: now, warnings: w})
		return nil, w
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 2 {
		w := []string{"财报日历解析失败"}
		f.extraPut("earn", extraEntry{at: now, warnings: w})
		return nil, w
	}
	idx := map[string]int{}
	for i, h := range rows[0] {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	symCol, ok1 := idx["symbol"]
	dateCol, ok2 := idx["reportdate"]
	nameCol, okN := idx["name"]
	timeCol, okT := idx["timeofday"]
	if !ok1 || !ok2 {
		w := []string{"财报日历缺少列"}
		f.extraPut("earn", extraEntry{at: now, warnings: w})
		return nil, w
	}
	out := []Event{}
	for _, r := range rows[1:] {
		if symCol >= len(r) || dateCol >= len(r) {
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(r[symCol]))
		if sym == "" || !scope[sym] {
			continue
		}
		day, err := time.Parse("2006-01-02", strings.TrimSpace(r[dateCol]))
		if err != nil || day.Before(now.AddDate(0, 0, -1)) || day.After(now.AddDate(0, 0, 60)) {
			continue
		}
		when := day.Add(23*time.Hour + 59*time.Minute)
		name := sym
		if okN && nameCol < len(r) && strings.TrimSpace(r[nameCol]) != "" {
			name = strings.TrimSpace(r[nameCol])
		}
		session := ""
		if okT && timeCol < len(r) {
			session = strings.ReplaceAll(strings.TrimSpace(r[timeCol]), "-", "")
		}
		out = append(out, Event{
			ID: "earnings:" + shortID(sym, strings.TrimSpace(r[dateCol])), Kind: "earnings", Source: "Earnings",
			Title: fmt.Sprintf("%s 财报（%s %s）", sym, name, session), Summary: "",
			PublishedAt: now, ReceivedAt: now, EndAt: &when,
			Markets: []Market{}, Coins: []string{sym}, Traders: tradersByCoin[sym],
		})
	}
	f.extraPut("earn", extraEntry{at: now, events: out})
	return out, nil
}

// ---------- Crypto calendar (built-in / configurable JSON) ----------

type cryptoCalEntry struct {
	Date       string `json:"date"`
	Coin       string `json:"coin"`
	Type       string `json:"type"` // unlock | upgrade | etf | listing | other
	Title      string `json:"title"`
	Importance int    `json:"importance"`
}

func (f *Feed) cryptoCalendarEvents(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if strings.TrimSpace(f.cryptoCalPath) == "" {
		return nil, nil
	}
	if e, ok := f.extraGet("cryptocal", time.Hour, now); ok {
		return e.events, e.warnings
	}
	data, err := os.ReadFile(f.cryptoCalPath)
	if err != nil {
		w := []string{"币圈日历读取失败: " + err.Error()}
		f.extraPut("cryptocal", extraEntry{at: now, warnings: w})
		return nil, w
	}
	var entries []cryptoCalEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		w := []string{"币圈日历 JSON 无效: " + err.Error()}
		f.extraPut("cryptocal", extraEntry{at: now, warnings: w})
		return nil, w
	}
	scope := map[string]bool{}
	for _, c := range coins {
		scope[strings.ToUpper(c)] = true
	}
	out := []Event{}
	for _, e := range entries {
		coin := strings.ToUpper(strings.TrimSpace(e.Coin))
		if coin == "" || !scope[coin] {
			continue
		}
		when, err := time.Parse(time.RFC3339, strings.TrimSpace(e.Date))
		if err != nil || when.Before(now.Add(-24*time.Hour)) || when.After(now.Add(30*24*time.Hour)) {
			continue
		}
		out = append(out, Event{
			ID: "cryptocal:" + shortID(coin, e.Date, e.Title), Kind: "crypto_calendar", Source: "Crypto Calendar",
			Title: e.Title, Summary: "类型: " + e.Type, PublishedAt: now, ReceivedAt: now, EndAt: &when,
			Markets: []Market{}, Coins: []string{coin}, Traders: tradersByCoin[coin],
		})
	}
	f.extraPut("cryptocal", extraEntry{at: now, events: out})
	return out, nil
}

// ---------- Fear & Greed index (free alternative.me, no key) ----------

type fngResponse struct {
	Data []struct {
		Value               string `json:"value"`
		ValueClassification string `json:"value_classification"`
		Timestamp           string `json:"timestamp"`
	} `json:"data"`
}

func fngClassificationCN(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "extreme fear":
		return "极度恐慌"
	case "fear":
		return "恐慌"
	case "neutral":
		return "中性"
	case "greed":
		return "贪婪"
	case "extreme greed":
		return "极度贪婪"
	}
	return c
}

func (f *Feed) fearGreedEvents(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if e, ok := f.extraGet("feargreed", 30*time.Minute, now); ok {
		return e.events, e.warnings
	}
	var resp fngResponse
	if err := f.get(ctx, f.fearGreedURL, &resp); err != nil || len(resp.Data) == 0 {
		var w []string
		if err != nil {
			w = []string{"恐惧贪婪指数不可用: " + err.Error()}
		}
		f.extraPut("feargreed", extraEntry{at: now, warnings: w})
		return nil, w
	}
	latest := resp.Data[0]
	summary := ""
	if len(resp.Data) > 1 {
		var cur, prev int
		fmt.Sscanf(latest.Value, "%d", &cur)
		fmt.Sscanf(resp.Data[1].Value, "%d", &prev)
		summary = fmt.Sprintf("较昨日 %+d（前值 %d）", cur-prev, prev)
	}
	var traders []TraderRef
	for _, c := range coins {
		for _, ref := range tradersByCoin[c] {
			traders = appendRef(traders, ref)
		}
	}
	ev := Event{
		ID: "feargreed:" + shortID(latest.Timestamp, latest.Value), Kind: "sentiment", Source: "Fear & Greed",
		Title: fmt.Sprintf("恐惧贪婪指数 %s（%s）", latest.Value, fngClassificationCN(latest.ValueClassification)),
		Summary: summary, PublishedAt: now, ReceivedAt: now,
		Markets: []Market{}, Coins: append([]string{}, coins...), Traders: traders,
	}
	out := []Event{ev}
	f.extraPut("feargreed", extraEntry{at: now, events: out})
	return out, nil
}

// ---------- Telegram public channels (t.me/s/<channel> web preview) ----------

var (
	tgPostRe = regexp.MustCompile(`data-post="([^"]+)"`)
	tgTextRe = regexp.MustCompile(`(?s)<div class="tgme_widget_message_text[^"]*"[^>]*>(.*?)</div>`)
	tgTimeRe = regexp.MustCompile(`(?s)<time[^>]*datetime="([^"]+)"`)
)

func (f *Feed) telegramEvents(ctx context.Context, now time.Time, coins []string, tradersByCoin map[string][]TraderRef) ([]Event, []string) {
	if len(f.tgChannels) == 0 {
		return nil, nil
	}
	var out []Event
	var warns []string
	for _, ch := range f.tgChannels {
		key := "tg:" + ch
		if e, ok := f.extraGet(key, 2*time.Minute, now); ok {
			out = append(out, e.events...)
			warns = append(warns, e.warnings...)
			continue
		}
		htmlStr, err := f.getText(ctx, "https://t.me/s/"+ch)
		if err != nil {
			warns = append(warns, "Telegram 频道 "+ch+" 不可用: "+err.Error())
			f.extraPut(key, extraEntry{at: now, warnings: []string{"Telegram 频道 " + ch + " 不可用: " + err.Error()}})
			continue
		}
		dayEvents := parseTelegramChannel(htmlStr, ch, coins, tradersByCoin, now)
		f.extraPut(key, extraEntry{at: now, events: dayEvents})
		out = append(out, dayEvents...)
	}
	return out, warns
}

func parseTelegramChannel(htmlStr, channel string, coins []string, tradersByCoin map[string][]TraderRef, now time.Time) []Event {
	locs := tgPostRe.FindAllStringSubmatchIndex(htmlStr, -1)
	out := []Event{}
	for i, loc := range locs {
		start := loc[1]
		end := len(htmlStr)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		window := htmlStr[start:end]
		post := htmlStr[loc[2]:loc[3]]
		text := ""
		if m := tgTextRe.FindStringSubmatch(window); len(m) == 2 {
			text = plainText(m[1], 1200)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		var published time.Time
		if m := tgTimeRe.FindStringSubmatch(window); len(m) == 2 {
			published = parseTime(m[1])
		}
		if published.IsZero() || published.Before(now.Add(-7*24*time.Hour)) || published.After(now.Add(10*time.Minute)) {
			continue
		}
		matched := []string{}
		traders := []TraderRef{}
		for _, coin := range coins {
			if articleMatches(coin, text, false) {
				matched = append(matched, coin)
				for _, ref := range tradersByCoin[coin] {
					traders = appendRef(traders, ref)
				}
			}
		}
		if len(matched) == 0 {
			continue
		}
		out = append(out, Event{
			ID: "telegram:" + shortID(channel, post), Kind: "news", Source: "Telegram",
			Title: truncateRunes(text, 120), Summary: text,
			Publisher: "@" + channel, PublishedAt: published, ReceivedAt: now,
			Markets: []Market{}, Coins: matched, Traders: traders,
		})
	}
	return out
}

// getJSONHeaders fetches JSON with explicit headers (some public endpoints reject
// the default UA). Honors the shared request semaphore.
func (f *Feed) getJSONHeaders(ctx context.Context, raw string, headers map[string]string, out any) error {
	select {
	case f.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-f.sem }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NOFX-Events/1.0")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func (f *Feed) getText(ctx context.Context, raw string) (string, error) {
	select {
	case f.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-f.sem }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(b), err
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// sortEvents keeps merge output deterministic; used by Build.
func sortEventsByTimeDesc(evs []Event) {
	sort.Slice(evs, func(i, j int) bool { return evs[i].PublishedAt.After(evs[j].PublishedAt) })
}
