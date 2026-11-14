// Command explosive_backtest rebuilds the explosive board from historical
// candles and measures which features actually predicted the rest of the day.
//
//	HTTPS_PROXY=socks5h://127.0.0.1:1080 go run ./cmd/explosive_backtest -days 30
//
// It reads public Binance data only and writes a CSV of the replay.
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/explosive"
	"nofx/explosive/backtest"
)

const baseURL = "https://fapi.binance.com"

func main() {
	days := flag.Int("days", 30, "how many days to replay")
	minGain := flag.Float64("min-gain", 0.2, "only replay symbols that moved at least this much in a day")
	workers := flag.Int("workers", 8, "parallel symbol fetches")
	limit := flag.Int("symbols", 0, "cap the number of candidate symbols (0 = all)")
	maxSymbols := flag.Int("max-symbols", 0, "only scan this many of the most liquid symbols (0 = all)")
	out := flag.String("out", "/tmp/explosive_backtest.csv", "CSV output path")
	timeout := flag.Duration("timeout", 40*time.Minute, "overall timeout")
	rps := flag.Float64("rps", 2, "requests per second; keep low: the live app shares this egress IP")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	c := &http.Client{Timeout: 25 * time.Second}
	limiter = time.NewTicker(time.Duration(float64(time.Second) / *rps))
	defer limiter.Stop()

	symbols, err := perps(ctx, c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "exchangeInfo: %v\n", err)
		os.Exit(1)
	}
	if *maxSymbols > 0 && len(symbols) > *maxSymbols {
		ranked, err := byVolume(ctx, c, symbols)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tickers: %v\n", err)
			os.Exit(1)
		}
		symbols = ranked[:*maxSymbols]
	}
	fmt.Printf("perpetuals: %d\n", len(symbols))

	end := time.Now().UnixMilli()
	start := end - int64(*days+2)*24*3600*1000

	// Candidates: symbols with at least one large daily move in the window.
	type cand struct{ symbol string }
	var cands []cand
	var mu sync.Mutex
	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup
	for _, sym := range symbols {
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			bars, err := klines(ctx, c, sym, "1d", start, end, 0)
			if err != nil || len(bars) < 2 {
				return
			}
			best := 0.0
			for _, b := range bars {
				if b.Open > 0 {
					best = math.Max(best, math.Abs(b.Close/b.Open-1))
				}
			}
			if best < *minGain {
				return
			}
			mu.Lock()
			cands = append(cands, cand{sym})
			mu.Unlock()
		}(sym)
	}
	wg.Wait()
	sort.Slice(cands, func(i, j int) bool { return cands[i].symbol < cands[j].symbol })
	if *limit > 0 && len(cands) > *limit {
		cands = cands[:*limit]
	}
	fmt.Printf("candidate symbols: %d (daily move >= %.0f%%)\n", len(cands), *minGain*100)

	// BTC benchmark per Beijing day.
	btcBars, err := all5m(ctx, c, "BTCUSDT", start, end)
	if err != nil {
		fmt.Fprintf(os.Stderr, "btc klines: %v\n", err)
		os.Exit(1)
	}
	btcDay := map[int64]float64{}
	for _, b := range btcBars {
		d := backtest.DayStart(b.Time)
		if _, ok := btcDay[d]; !ok {
			btcDay[d] = -999 // mark seen
		}
	}
	for d := range btcDay {
		btcDay[d] = backtest.DayGain(btcBars, d)
	}
	fmt.Printf("btc days: %d\n", len(btcDay))

	var rows []backtest.Row
	done := 0
	for _, cd := range cands {
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			bars, err := all5m(ctx, c, sym, start, end)
			if err != nil {
				return
			}
			rs := backtest.BuildRows(sym, bars, btcDay, 0.05, 5e6)
			mu.Lock()
			rows = append(rows, rs...)
			done++
			if done%25 == 0 {
				fmt.Printf("  replayed %d/%d symbols, %d rows\n", done, len(cands), len(rows))
			}
			mu.Unlock()
		}(cd.symbol)
	}
	wg.Wait()
	fmt.Printf("rows: %d across %d days\n", len(rows), distinctDays(rows))
	if len(rows) == 0 {
		fmt.Println("no rows: widen -days or lower -min-gain")
		return
	}
	writeCSV(*out, rows)
	fmt.Printf("dataset: %s\n", *out)

	feats := backtest.Features()
	fmt.Println("\nfeature IC (remaining to day close):")
	type fIC struct {
		name  string
		pool  float64
		daily float64
	}
	var ics []fIC
	for _, f := range feats {
		ics = append(ics, fIC{f.Name, backtest.Spearman(rows, f.Get), backtest.DailyIC(rows, f.Get)})
	}
	sort.Slice(ics, func(i, j int) bool { return math.Abs(ics[i].daily) > math.Abs(ics[j].daily) })
	for _, ic := range ics {
		fmt.Printf("  %-14s pooled %+.3f   daily %+.3f\n", ic.name, ic.pool, ic.daily)
	}

	bands := []string{"<1%", "1-2%", "2-5%", "5-10%", "≥10%"}
	edges := []float64{0, 1, 2, 5, 10}

	cur := func(r backtest.Row) float64 {
		return explosive.Potential(r.DayGain, r.ElapsedH, r.VolSlope, 0, false, r.HighDist, 0)
	}
	fmt.Printf("\ncurrent model: pooled IC %+.3f  daily IC %+.3f\n",
		backtest.Spearman(rows, cur), backtest.DailyIC(rows, cur))
	printBands(backtest.Bands(rows, cur, bands, edges))

	weights := backtest.FitWeights(rows, feats)
	fmt.Println("\nIC-weighted model:")
	for _, f := range feats {
		if w := weights[f.Name]; w != 0 {
			fmt.Printf("  %-14s %+.3f\n", f.Name, w)
		}
	}
	fitted := backtest.Score(rows, feats, weights)
	fmt.Printf("  pooled IC %+.3f  daily IC %+.3f\n",
		backtest.Spearman(rows, fitted), backtest.DailyIC(rows, fitted))
	qNames, qEdges := quantiles(rows, fitted)
	printBands(backtest.Bands(rows, fitted, qNames, qEdges))
}

func printBands(bs []backtest.Band) {
	fmt.Println("  band      n     up     avgRet   avgDD    sustained")
	for _, b := range bs {
		if b.Samples == 0 {
			continue
		}
		fmt.Printf("  %-8s %4d  %5.0f%%  %+7.2f%%  %+7.2f%%  %5.0f%%\n",
			b.Name, b.Samples, b.Up*100, b.AvgRet*100, b.AvgDD*100, b.Sustained*100)
	}
}

func quantiles(rows []backtest.Row, score func(backtest.Row) float64) ([]string, []float64) {
	vals := make([]float64, len(rows))
	for i, r := range rows {
		vals[i] = score(r)
	}
	sort.Float64s(vals)
	at := func(q float64) float64 {
		i := int(q * float64(len(vals)-1))
		return vals[i]
	}
	edges := []float64{at(0.2), at(0.4), at(0.6), at(0.8)}
	names := []string{"Q1", "Q2", "Q3", "Q4", "Q5"}
	return names, edges
}

func distinctDays(rows []backtest.Row) int {
	m := map[int64]bool{}
	for _, r := range rows {
		m[r.Day] = true
	}
	return len(m)
}

func writeCSV(path string, rows []backtest.Row) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "csv: %v\n", err)
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"symbol", "day", "hour", "price", "day_gain", "elapsed_h", "vol_slope", "high_dist",
		"change_1h", "change_4h", "atr_pct", "taker_ratio", "efficiency", "rel_strength", "quote_vol", "remaining", "max_dd"})
	for _, r := range rows {
		_ = w.Write([]string{r.Symbol, strconv.FormatInt(r.Day, 10), strconv.Itoa(r.Hour),
			f2(r.Price), f4(r.DayGain), f2(r.ElapsedH), f4(r.VolSlope), f4(r.HighDist),
			f4(r.Change1h), f4(r.Change4h), f4(r.ATRPct), f4(r.TakerRatio), f4(r.Efficiency), f4(r.RelStrength),
			fmt.Sprintf("%.0f", r.QuoteVol), f4(r.Remaining), f4(r.MaxDD)})
	}
	w.Flush()
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
func f4(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

// byVolume orders symbols by 24h turnover, most liquid first.
func byVolume(ctx context.Context, c *http.Client, symbols []string) ([]string, error) {
	var rows []struct{ Symbol, QuoteVolume string }
	if err := getJSON(ctx, c, baseURL+"/fapi/v1/ticker/24hr", &rows); err != nil {
		return nil, err
	}
	vol := make(map[string]float64, len(rows))
	for _, r := range rows {
		if v, err := strconv.ParseFloat(r.QuoteVolume, 64); err == nil {
			vol[r.Symbol] = v
		}
	}
	out := append([]string(nil), symbols...)
	sort.Slice(out, func(i, j int) bool { return vol[out[i]] > vol[out[j]] })
	return out, nil
}

// perps lists tradable USDT-margined crypto perpetuals.
func perps(ctx context.Context, c *http.Client) ([]string, error) {
	var info struct {
		Symbols []struct {
			Symbol, QuoteAsset, MarginAsset, Status, ContractType, UnderlyingType string
		}
	}
	if err := getJSON(ctx, c, baseURL+"/fapi/v1/exchangeInfo", &info); err != nil {
		return nil, err
	}
	var out []string
	for _, s := range info.Symbols {
		if s.Status != "TRADING" || s.QuoteAsset != "USDT" || s.MarginAsset != "USDT" || s.ContractType != "PERPETUAL" {
			continue
		}
		if strings.HasPrefix(s.ContractType, "TRADIFI") || (s.UnderlyingType != "" && s.UnderlyingType != "COIN") {
			continue
		}
		out = append(out, s.Symbol)
	}
	return out, nil
}

// all5m pages through 5m klines for the whole window.
func all5m(ctx context.Context, c *http.Client, symbol string, start, end int64) ([]backtest.Bar, error) {
	var out []backtest.Bar
	cursor := start
	for cursor < end {
		bars, err := klines(ctx, c, symbol, "5m", cursor, end, 1500)
		if err != nil {
			return nil, err
		}
		if len(bars) == 0 {
			break
		}
		out = append(out, bars...)
		next := bars[len(bars)-1].Time + 5*60*1000
		if next <= cursor {
			break
		}
		cursor = next
	}
	return out, nil
}

func klines(ctx context.Context, c *http.Client, symbol, interval string, start, end int64, limit int) ([]backtest.Bar, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("interval", interval)
	q.Set("startTime", strconv.FormatInt(start, 10))
	q.Set("endTime", strconv.FormatInt(end, 10))
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var raw [][]json.RawMessage
	if err := getJSON(ctx, c, baseURL+"/fapi/v1/klines?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	out := make([]backtest.Bar, 0, len(raw))
	for _, r := range raw {
		if len(r) < 10 {
			continue
		}
		out = append(out, backtest.Bar{
			Time: int64(num(r[0])), Open: num(r[1]), High: num(r[2]), Low: num(r[3]),
			Close: num(r[4]), Volume: num(r[5]), Quote: num(r[7]), Taker: num(r[9]),
		})
	}
	return out, nil
}

// limiter paces every request: the backtest, the local app and the live server
// all leave through the same IP, so bursts get the whole IP banned.
var limiter *time.Ticker

func pace() {
	if limiter != nil {
		<-limiter.C
	}
}

func getJSON(ctx context.Context, c *http.Client, u string, out any) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		pace()
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
			return fmt.Errorf("binance rate limited this IP (%d): %s\nreduce -rps and wait before retrying",
				resp.StatusCode, banMessage(body))
		}
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 120))
			time.Sleep(time.Duration(attempt+1) * 700 * time.Millisecond)
			continue
		}
		if err := json.Unmarshal(body, out); err != nil {
			return err
		}
		return nil
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

// banMessage extracts Binance's "banned until <ms>" text when present.
func banMessage(body []byte) string {
	s := string(body)
	if i := strings.Index(s, "banned until"); i >= 0 {
		end := i + len("banned until")
		for end < len(s) && (s[end] == ' ' || (s[end] >= '0' && s[end] <= '9')) {
			end++
		}
		return s[i:end]
	}
	return truncate(s, 160)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
