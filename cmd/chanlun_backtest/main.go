// Command chanlun_backtest replays the 缠论 buy/sell points on historical
// Binance USDT-perp candles.
//
//	go run ./cmd/chanlun_backtest -symbol BTCUSDT -interval 1h -days 1095
//
// It reads public Binance data only. Candles are cached as CSV so reruns do
// not hit the exchange again.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"nofx/chanlun"
)

const baseURL = "https://fapi.binance.com"

var intervalMs = map[string]int64{
	"15m": 15 * 60_000, "30m": 30 * 60_000, "1h": 3600_000, "2h": 2 * 3600_000,
	"4h": 4 * 3600_000, "1d": 24 * 3600_000,
}

func main() {
	symbol := flag.String("symbol", "BTCUSDT", "Binance USDT-perp symbol")
	interval := flag.String("interval", "1h", "candle interval: 15m 30m 1h 2h 4h 1d")
	days := flag.Int("days", 1095, "how many days of history")
	fee := flag.Float64("fee", 0.0005, "fee per side")
	slip := flag.Float64("slippage", 0.0002, "slippage per fill")
	longOnly := flag.Bool("long-only", false, "ignore sell points as entries (they still close longs)")
	noTrail := flag.Bool("no-trail", false, "keep the initial stop instead of trailing it along strokes")
	kinds := flag.String("kinds", "", "comma list of signal kinds to trade, e.g. B3,S3 (empty = all)")
	from := flag.String("from", "", "only replay candles from this UTC date (2006-01-02)")
	to := flag.String("to", "", "only replay candles before this UTC date (2006-01-02)")
	align := flag.Bool("align", false, "take 3rd points only along the running higher-level move")
	noFunding := flag.Bool("no-funding", false, "ignore perpetual funding payments")
	cache := flag.String("cache", "data/klines", "candle cache directory")
	tradesOut := flag.String("trades", "", "write the trade list to this CSV")
	flag.Parse()

	step, ok := intervalMs[*interval]
	if !ok {
		fmt.Fprintf(os.Stderr, "unsupported interval %q\n", *interval)
		os.Exit(1)
	}
	bars, err := load(*symbol, *interval, step, *days, *cache)
	if err != nil {
		fmt.Fprintf(os.Stderr, "candles: %v\n", err)
		os.Exit(1)
	}
	for _, lim := range []struct {
		date   string
		before bool
	}{{*from, false}, {*to, true}} {
		if lim.date == "" {
			continue
		}
		d, err := time.Parse("2006-01-02", lim.date)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad date %q\n", lim.date)
			os.Exit(1)
		}
		if lim.before {
			bars = window(bars, 0, d.UnixMilli())
		} else {
			bars = window(bars, d.UnixMilli(), math.MaxInt64)
		}
	}
	if len(bars) < 200 {
		fmt.Fprintf(os.Stderr, "only %d candles, not enough to backtest\n", len(bars))
		os.Exit(1)
	}

	cfg := chanlun.Config{FeeRate: *fee, Slippage: *slip, LongOnly: *longOnly, Trail: !*noTrail, Align: *align}
	if !*noFunding {
		cfg.Funding, err = loadFunding(*symbol, bars[0].Time, bars[len(bars)-1].Time+step, *cache)
		if err != nil {
			fmt.Fprintf(os.Stderr, "funding: %v\n", err)
			os.Exit(1)
		}
	}
	if *kinds != "" {
		cfg.Kinds = map[string]bool{}
		for _, k := range strings.Split(*kinds, ",") {
			cfg.Kinds[strings.ToUpper(strings.TrimSpace(k))] = true
		}
	}
	res := chanlun.Run(bars, cfg)
	report(*symbol, *interval, step, bars, res, cfg)
	if *tradesOut != "" {
		if err := writeTrades(*tradesOut, res.Trades); err != nil {
			fmt.Fprintf(os.Stderr, "trades csv: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n交易明细: %s\n", *tradesOut)
	}
}

func report(symbol, interval string, step int64, bars []chanlun.Bar, res chanlun.Result, cfg chanlun.Config) {
	day := func(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02") }
	first, last := bars[0], bars[len(bars)-1]
	years := float64(last.Time-first.Time) / (365.25 * 24 * 3600_000)
	perYear := 365.25 * 24 * 3600_000 / float64(step)

	fmt.Printf("%s %s  %s ~ %s  (%d 根K线, %.2f 年)\n", symbol, interval, day(first.Time), day(last.Time), len(bars), years)
	fmt.Printf("手续费 %.3f%%/边  滑点 %.3f%%/笔  %s  %s\n", cfg.FeeRate*100, cfg.Slippage*100,
		map[bool]string{true: "只做多", false: "多空双向"}[cfg.LongOnly],
		map[bool]string{true: "跟踪止损", false: "固定止损"}[cfg.Trail])
	fmt.Printf("资金费率 %d 次结算  三类点顺大级别过滤 %v\n", len(cfg.Funding), cfg.Align)
	fmt.Printf("结构: %d 笔端点, %d 线段端点, %d 高级别线段端点, %d 个线段中枢\n信号: %s\n\n",
		res.Points, res.Segments, res.Big, res.Pivots, counts(res.Signals))

	hold := make([]float64, len(bars))
	for i, b := range bars {
		hold[i] = b.Close / first.Close
	}
	fmt.Printf("%-10s %10s %10s %10s %8s\n", "", "总收益", "年化", "最大回撤", "夏普")
	line := func(name string, eq []float64) {
		total := eq[len(eq)-1]/eq[0] - 1
		cagr := math.Pow(eq[len(eq)-1]/eq[0], 1/years) - 1
		fmt.Printf("%-10s %9.1f%% %9.1f%% %9.1f%% %8.2f\n", name, total*100, cagr*100, maxDD(eq)*100, sharpe(eq, perYear))
	}
	line("缠论策略", append([]float64{1}, res.Equity...))
	line("买入持有", hold)

	ts := res.Trades
	if len(ts) == 0 {
		fmt.Println("\n没有成交")
		return
	}
	fmt.Printf("\n%-6s %5s %7s %9s %9s %9s %8s\n", "类型", "笔数", "胜率", "平均", "平均盈", "平均亏", "盈亏比")
	groups := map[string][]chanlun.Trade{}
	for _, t := range ts {
		groups[t.Kind] = append(groups[t.Kind], t)
	}
	names := make([]string, 0, len(groups))
	for k := range groups {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		tradeLine(k, groups[k])
	}
	tradeLine("合计", ts)

	var held int
	stops := 0
	for _, t := range ts {
		held += t.ExitIdx - t.EntryIdx
		if t.Reason == "stop" {
			stops++
		}
	}
	fmt.Printf("\n持仓时间占比 %.0f%%  平均持仓 %.1f 根K线  止损出场 %d/%d\n",
		float64(held)/float64(len(bars))*100, float64(held)/float64(len(ts)), stops, len(ts))

	fmt.Printf("\n%-6s %10s %10s %6s\n", "年份", "策略", "买入持有", "笔数")
	type yr struct {
		eq0, eq1, px0, px1 float64
		n                  int
	}
	byYear := map[int]*yr{}
	var order []int
	prev := 1.0
	for i, b := range bars {
		y := time.UnixMilli(b.Time).UTC().Year()
		r := byYear[y]
		if r == nil {
			r = &yr{eq0: prev, px0: b.Open}
			byYear[y] = r
			order = append(order, y)
		}
		r.eq1, r.px1 = res.Equity[i], b.Close
		prev = res.Equity[i]
	}
	for _, t := range ts {
		byYear[time.UnixMilli(t.EntryTime).UTC().Year()].n++
	}
	for _, y := range order {
		r := byYear[y]
		fmt.Printf("%-6d %9.1f%% %9.1f%% %6d\n", y, (r.eq1/r.eq0-1)*100, (r.px1/r.px0-1)*100, r.n)
	}
}

func tradeLine(name string, ts []chanlun.Trade) {
	var win, loss, sumWin, sumLoss, sum float64
	for _, t := range ts {
		sum += t.Return
		if t.Return > 0 {
			win++
			sumWin += t.Return
		} else {
			loss++
			sumLoss -= t.Return
		}
	}
	avgWin, avgLoss, pf := 0.0, 0.0, math.NaN()
	if win > 0 {
		avgWin = sumWin / win
	}
	if loss > 0 {
		avgLoss = sumLoss / loss
		pf = sumWin / sumLoss
	}
	n := float64(len(ts))
	fmt.Printf("%-6s %5d %6.1f%% %8.2f%% %8.2f%% %8.2f%% %8.2f\n", name, len(ts), win/n*100, sum/n*100, avgWin*100, -avgLoss*100, pf)
}

func counts(m map[string]int) string {
	var parts []string
	for _, k := range []string{"B1", "B2", "B3", "S1", "S2", "S3"} {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

func maxDD(eq []float64) float64 {
	peak, dd := eq[0], 0.0
	for _, v := range eq {
		if v > peak {
			peak = v
		}
		if d := 1 - v/peak; d > dd {
			dd = d
		}
	}
	return -dd
}

func sharpe(eq []float64, perYear float64) float64 {
	if len(eq) < 3 {
		return 0
	}
	var sum, sq float64
	n := float64(len(eq) - 1)
	for i := 1; i < len(eq); i++ {
		r := eq[i]/eq[i-1] - 1
		sum += r
		sq += r * r
	}
	mean := sum / n
	sd := math.Sqrt(sq/n - mean*mean)
	if sd == 0 {
		return 0
	}
	return mean / sd * math.Sqrt(perYear)
}

func writeTrades(path string, ts []chanlun.Trade) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"kind", "side", "entry_time", "exit_time", "entry", "exit", "return_pct", "bars", "reason"})
	for _, t := range ts {
		side := "short"
		if t.Long {
			side = "long"
		}
		_ = w.Write([]string{
			t.Kind, side,
			time.UnixMilli(t.EntryTime).UTC().Format(time.RFC3339), time.UnixMilli(t.ExitTime).UTC().Format(time.RFC3339),
			strconv.FormatFloat(t.Entry, 'f', 2, 64), strconv.FormatFloat(t.Exit, 'f', 2, 64),
			strconv.FormatFloat(t.Return*100, 'f', 3, 64), strconv.Itoa(t.ExitIdx - t.EntryIdx), t.Reason,
		})
	}
	w.Flush()
	return w.Error()
}

// load returns closed candles for the window, from the cache when it already
// covers it.
func load(symbol, interval string, step int64, days int, dir string) ([]chanlun.Bar, error) {
	// Align to whole days so reruns on the same day reuse the cache.
	end := time.Now().UTC().Truncate(24 * time.Hour).UnixMilli()
	start := end - int64(days)*24*3600_000
	path := filepath.Join(dir, fmt.Sprintf("%s_%s.csv", symbol, interval))

	if bars, err := readCSV(path); err == nil && len(bars) > 0 &&
		bars[0].Time <= start+step && bars[len(bars)-1].Time >= end-step {
		return window(bars, start, end), nil
	}
	fmt.Fprintf(os.Stderr, "拉取 %s %s K线...\n", symbol, interval)
	bars, err := fetch(symbol, interval, step, start, end)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		if err := writeCSV(path, bars); err != nil {
			fmt.Fprintf(os.Stderr, "cache not written: %v\n", err)
		}
	}
	return window(bars, start, end), nil
}

// loadFunding returns the funding settlements in [start, end), cached like
// the candles.
func loadFunding(symbol string, start, end int64, dir string) ([]chanlun.Funding, error) {
	path := filepath.Join(dir, symbol+"_funding.csv")
	pick := func(all []chanlun.Funding) []chanlun.Funding {
		var out []chanlun.Funding
		for _, f := range all {
			if f.Time >= start && f.Time < end {
				out = append(out, f)
			}
		}
		return out
	}
	const eightHours = 8 * 3600_000
	if f, err := os.Open(path); err == nil {
		rows, err := csv.NewReader(f).ReadAll()
		f.Close()
		var all []chanlun.Funding
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			t, err1 := strconv.ParseInt(r[0], 10, 64)
			v, err2 := strconv.ParseFloat(r[1], 64)
			if err1 == nil && err2 == nil {
				all = append(all, chanlun.Funding{Time: t, Rate: v})
			}
		}
		if err == nil && len(all) > 0 && all[0].Time <= start+eightHours && all[len(all)-1].Time >= end-eightHours-1 {
			return pick(all), nil
		}
	}
	fmt.Fprintf(os.Stderr, "拉取 %s 资金费率...\n", symbol)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := &http.Client{Timeout: 25 * time.Second}
	var all []chanlun.Funding
	for cursor := start; cursor < end; {
		time.Sleep(400 * time.Millisecond)
		q := url.Values{}
		q.Set("symbol", symbol)
		q.Set("startTime", strconv.FormatInt(cursor, 10))
		q.Set("endTime", strconv.FormatInt(end-1, 10))
		q.Set("limit", "1000")
		var raw []struct {
			FundingTime int64  `json:"fundingTime"`
			FundingRate string `json:"fundingRate"`
		}
		if err := getJSON(ctx, c, baseURL+"/fapi/v1/fundingRate?"+q.Encode(), &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}
		for _, r := range raw {
			v, _ := strconv.ParseFloat(r.FundingRate, 64)
			all = append(all, chanlun.Funding{Time: r.FundingTime, Rate: v})
		}
		next := all[len(all)-1].Time + 1
		if next <= cursor {
			break
		}
		cursor = next
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		if f, err := os.Create(path); err == nil {
			w := csv.NewWriter(f)
			_ = w.Write([]string{"time", "rate"})
			for _, r := range all {
				_ = w.Write([]string{strconv.FormatInt(r.Time, 10), strconv.FormatFloat(r.Rate, 'f', -1, 64)})
			}
			w.Flush()
			f.Close()
		}
	}
	return pick(all), nil
}

func window(bars []chanlun.Bar, start, end int64) []chanlun.Bar {
	out := bars[:0:0]
	for _, b := range bars {
		if b.Time >= start && b.Time < end {
			out = append(out, b)
		}
	}
	return out
}

func fetch(symbol, interval string, step, start, end int64) ([]chanlun.Bar, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := &http.Client{Timeout: 25 * time.Second}
	var out []chanlun.Bar
	for cursor := start; cursor < end; {
		// The live app shares this egress IP: stay far below the rate limit.
		time.Sleep(400 * time.Millisecond)
		q := url.Values{}
		q.Set("symbol", symbol)
		q.Set("interval", interval)
		q.Set("startTime", strconv.FormatInt(cursor, 10))
		q.Set("endTime", strconv.FormatInt(end-1, 10))
		q.Set("limit", "1500")
		var raw [][]json.RawMessage
		if err := getJSON(ctx, c, baseURL+"/fapi/v1/klines?"+q.Encode(), &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}
		for _, r := range raw {
			if len(r) < 5 {
				continue
			}
			out = append(out, chanlun.Bar{Time: int64(num(r[0])), Open: num(r[1]), High: num(r[2]), Low: num(r[3]), Close: num(r[4])})
		}
		next := out[len(out)-1].Time + step
		if next <= cursor {
			break
		}
		cursor = next
	}
	return out, nil
}

func getJSON(ctx context.Context, c *http.Client, u string, out any) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			last = err
			time.Sleep(time.Duration(attempt+1) * 700 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusTeapot {
			return fmt.Errorf("binance rate limited this IP (%d); wait before retrying", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("HTTP %d: %.120s", resp.StatusCode, body)
			time.Sleep(time.Duration(attempt+1) * 700 * time.Millisecond)
			continue
		}
		return json.Unmarshal(body, out)
	}
	return last
}

func num(raw json.RawMessage) float64 {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	var f float64
	_ = json.Unmarshal(raw, &f)
	return f
}

func readCSV(path string) ([]chanlun.Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	var out []chanlun.Bar
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		t, err := strconv.ParseInt(r[0], 10, 64)
		if err != nil {
			continue // header
		}
		p := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		out = append(out, chanlun.Bar{Time: t, Open: p(r[1]), High: p(r[2]), Low: p(r[3]), Close: p(r[4])})
	}
	return out, nil
}

func writeCSV(path string, bars []chanlun.Bar) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"time", "open", "high", "low", "close"})
	for _, b := range bars {
		p := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
		_ = w.Write([]string{strconv.FormatInt(b.Time, 10), p(b.Open), p(b.High), p(b.Low), p(b.Close)})
	}
	w.Flush()
	return w.Error()
}
