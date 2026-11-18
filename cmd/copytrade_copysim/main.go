// Command copytrade_copysim replays stored fills as if every position the
// lead traders opened had been copied with the same stake: a few minutes
// late, with fees, no score. It reads a database copy and Binance's public
// candles (cached on disk); nothing is written to the database.
//
//	go run ./cmd/copytrade_copysim -db data/backtest/board.db -days 14 -bars-dir data/backtest/bars5m
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"nofx/copytrade"
	"nofx/store"
)

const step = 300 // 5-minute candles

func main() {
	dbPath := flag.String("db", "", "SQLite database to read (a copy)")
	days := flag.Int("days", 14, "days to replay, ending at the newest fill")
	barsDir := flag.String("bars-dir", "", "directory to keep downloaded 5-minute candles in")
	fee := flag.Float64("fee", 0.0005, "fee on the value of every fill (taker 0.05%)")
	top := flag.Int("traders", 12, "best and worst traders to list")
	flag.Parse()
	if *dbPath == "" || *barsDir == "" {
		fmt.Fprintln(os.Stderr, "-db and -bars-dir are required")
		os.Exit(2)
	}
	if err := run(*dbPath, *barsDir, *days, *fee, *top); err != nil {
		fmt.Fprintln(os.Stderr, "copysim:", err)
		os.Exit(1)
	}
}

func run(dbPath, barsDir string, days int, fee float64, top int) error {
	db, err := gorm.Open(sqlite.Open("file:"+dbPath+"?mode=ro"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return err
	}
	st := store.NewCopyTradeStore(db)
	in := copytrade.CopyInput{Fills: map[string][]copytrade.Fill{}, Names: map[string]string{}, Step: step, Fee: fee}
	follows, err := st.ListFollows()
	if err != nil {
		return err
	}
	for _, f := range follows {
		in.Names[f.PortfolioID] = f.Nickname
	}
	ids, err := st.FillTraders()
	if err != nil {
		return err
	}
	var newest int64
	for _, id := range ids {
		rows, err := st.Fills(id, 0)
		if err != nil {
			return err
		}
		fills := make([]copytrade.Fill, len(rows))
		for i, r := range rows {
			fills[i] = copytrade.Fill{Time: r.Time, Seq: r.Seq, Symbol: r.Symbol, Side: r.Side, PositionSide: r.PositionSide,
				Qty: r.Qty, Price: r.Price, Fee: r.Fee, RealizedPnL: r.RealizedPnL}
			newest = max(newest, r.Time)
		}
		in.Fills[id] = fills
	}
	if newest == 0 {
		return fmt.Errorf("no fills in %s", dbPath)
	}
	in.To = time.UnixMilli(newest).Truncate(5 * time.Minute)
	in.From = in.To.Add(-time.Duration(days) * 24 * time.Hour)
	// Candles for the coins traded in the window only.
	symbols := map[string]bool{"BTCUSDT": true}
	for _, fills := range in.Fills {
		for _, f := range fills {
			if f.Time >= in.From.UnixMilli() {
				symbols[f.Symbol] = true
			}
		}
	}
	in.Bars, err = loadBars(symbols, in.From, in.To, barsDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d traders, %d coins; copying positions opened %s → %s\n", len(ids), len(symbols),
		in.From.UTC().Format("2006-01-02 15:04"), in.To.UTC().Format("2006-01-02 15:04"))
	print(copytrade.CopyBacktest(in), top)
	return nil
}

// loadBars downloads the window's 5-minute candles per symbol from Binance's
// public futures API, one file per symbol, reusing files that reach to.
func loadBars(symbols map[string]bool, from, to time.Time, dir string) (map[string][]copytrade.Bar, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	list := make([]string, 0, len(symbols))
	for s := range symbols {
		list = append(list, s)
	}
	sort.Strings(list)
	out := make(map[string][]copytrade.Bar, len(list))
	client := &http.Client{Timeout: 20 * time.Second}
	fetched, missing := 0, 0
	for _, symbol := range list {
		path := filepath.Join(dir, url.PathEscape(symbol)+".json")
		var bars []copytrade.Bar
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &bars) == nil {
			if len(bars) == 0 || (bars[0].Time <= from.Unix()+step && bars[len(bars)-1].Time >= to.Unix()-2*step) {
				out[symbol] = bars
				continue
			}
		}
		bars = bars[:0]
		ok := true
		for start := from.Unix() - step; start < to.Unix(); {
			u := fmt.Sprintf("https://fapi.binance.com/fapi/v1/klines?symbol=%s&interval=5m&limit=1000&startTime=%d",
				url.QueryEscape(symbol), start*1000)
			resp, err := client.Get(u)
			if err != nil {
				return nil, fmt.Errorf("candles of %s: %w", symbol, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 418 {
				return nil, fmt.Errorf("candles of %s: Binance rate limited (HTTP %d); run again later, downloaded files are kept", symbol, resp.StatusCode)
			}
			if resp.StatusCode != http.StatusOK {
				ok = false // not a futures symbol any more
				break
			}
			var rows [][]json.RawMessage
			if json.Unmarshal(body, &rows) != nil || len(rows) == 0 {
				break
			}
			for _, r := range rows {
				if len(r) < 5 {
					continue
				}
				var open int64
				var c string
				if json.Unmarshal(r[0], &open) != nil || json.Unmarshal(r[4], &c) != nil {
					continue
				}
				v, _ := strconv.ParseFloat(c, 64)
				// Closed candles only.
				if open/1000+step <= time.Now().Unix() {
					bars = append(bars, copytrade.Bar{Time: open / 1000, Close: v})
				}
			}
			var last int64
			_ = json.Unmarshal(rows[len(rows)-1][0], &last)
			start = last/1000 + step
			if len(rows) < 1000 {
				break
			}
			time.Sleep(120 * time.Millisecond)
		}
		if !ok {
			missing++
			bars = []copytrade.Bar{}
		}
		out[symbol] = bars
		if raw, err := json.Marshal(bars); err == nil {
			_ = os.WriteFile(path, raw, 0o600)
		}
		if fetched++; fetched%40 == 0 {
			fmt.Fprintf(os.Stderr, "candles: %d of %d coins\n", fetched, len(list))
		}
		time.Sleep(120 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "candles: %d coins (%d downloaded now, %d without data)\n", len(list), fetched, missing)
	return out, nil
}

func print(rows []copytrade.CopyRow, top int) {
	head := func(title string) {
		fmt.Printf("\n%s\n", title)
		fmt.Printf("%-46s %5s %6s %6s %7s %7s %6s %7s %8s %5s %6s %7s %6s\n", "group", "who", "trips", "win%", "avg%", "med%", "beat%", "exces%", "total", "grow", "open", "ROI%", "maxDD")
	}
	line := func(r copytrade.CopyRow) {
		fmt.Printf("%-46s %5d %6d %6.1f %7.2f %7.2f %6.1f %7.2f %8.2f %5.2f %6d %7.1f %6.2f\n", r.Label, r.Traders, r.Trips, 100*r.Win,
			100*r.Avg, 100*r.Med, 100*r.Beat, 100*r.AvgExcess, r.Total, r.Peak, r.MaxOpen, 100*r.ROI, r.MaxDD)
	}
	for _, tb := range []struct{ key, title string }{
		{"how", "How it is copied — every position of every trader"},
		{"style", "Every action copied, up to 5 min late — by the trader's style"},
		{"style-open", "Open only, out when they close — by the trader's style"},
		{"side", "Every action copied — by side"},
		{"half", "Every action copied — by half of the period"},
		{"agree", "Open only, out when they close — alone, or with another trader opening the same way"},
		{"agree-clean", "The same, without averaging-down and high-frequency traders"},
		{"select", "Second half only, traders grouped by how copying them did in the first half"},
	} {
		head(tb.title)
		for _, r := range rows {
			if r.Table == tb.key {
				line(r)
			}
		}
	}
	var traders []copytrade.CopyRow
	for _, r := range rows {
		if r.Table == "trader" && r.Trips >= 5 {
			traders = append(traders, r)
		}
	}
	sort.SliceStable(traders, func(a, b int) bool { return traders[a].Total > traders[b].Total })
	positive := 0
	for _, r := range traders {
		if r.Total > 0 {
			positive++
		}
	}
	head(fmt.Sprintf("Per trader (5+ positions): %d of %d made money when copied. Best and worst by total", positive, len(traders)))
	for i, r := range traders {
		if i < top || i >= len(traders)-top {
			line(r)
		}
	}
	fmt.Println("\ntrips: positions copied. win%: made money after fees. avg/med: return per position on one stake. total: sum, in stakes.")
	fmt.Println("beat% / exces%: against holding the same side of BTC over each position's life (share that did better, mean difference).")
	fmt.Println("grow: median of how many times its opening fill a position grew to through adds. open: most positions held at once.")
	fmt.Println("Every-action rows size a position so its largest exposure is one stake; open-only rows put one stake on the open.")
	fmt.Println("ROI%: total over that, i.e. on the capital needed to take every position, no leverage. maxDD: deepest fall of the total.")
	fmt.Println("Entries and exits are 5-minute closes; the fee is taken on every fill; funding and slippage are not modelled.")
}
