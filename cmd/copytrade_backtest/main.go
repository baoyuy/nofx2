// Command copytrade_backtest replays the stored fills of lead traders and
// prints the record each signal definition would have had. It only reads the
// database (open a copy, never the live file of a running server).
//
//	go run ./cmd/copytrade_backtest -db data/data.db -days 21 -bars-cache /tmp/bars.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"nofx/copytrade"
	"nofx/store"
	"nofx/trader/binance"
)

func main() {
	dbPath := flag.String("db", "", "SQLite database to read (a copy)")
	days := flag.Int("days", 21, "days to replay, ending at the newest fill")
	cache := flag.String("bars-cache", "", "file to keep downloaded candles in between runs")
	warmup := flag.Int("warmup", 3, "days of fills to have before the first signal")
	flag.String("horizon", "", "print only this horizon: 4h, 24h or 72h")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "-db is required")
		os.Exit(2)
	}
	if err := run(*dbPath, *days, *warmup, *cache); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}

func run(dbPath string, days, warmup int, cachePath string) error {
	db, err := gorm.Open(sqlite.Open("file:"+dbPath+"?mode=ro"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return err
	}
	st := store.NewCopyTradeStore(db)
	follows, err := st.ListFollows()
	if err != nil {
		return err
	}
	in := copytrade.BacktestInput{Fills: map[string][]copytrade.Fill{}, Follows: map[string]store.CopyTradeFollow{}}
	for _, f := range follows {
		in.Follows[f.PortfolioID] = f
	}
	ids, err := st.FillTraders()
	if err != nil {
		return err
	}
	symbols := map[string]bool{"BTCUSDT": true}
	var oldest, newest int64
	total := 0
	for _, id := range ids {
		rows, err := st.Fills(id, 0)
		if err != nil {
			return err
		}
		fills := make([]copytrade.Fill, len(rows))
		for i, r := range rows {
			fills[i] = copytrade.Fill{Time: r.Time, Seq: r.Seq, Symbol: r.Symbol, Side: r.Side, PositionSide: r.PositionSide,
				Qty: r.Qty, Price: r.Price, Fee: r.Fee, RealizedPnL: r.RealizedPnL}
			symbols[r.Symbol] = true
			if oldest == 0 || r.Time < oldest {
				oldest = r.Time
			}
			newest = max(newest, r.Time)
		}
		in.Fills[id] = fills
		total += len(fills)
	}
	if total == 0 {
		return fmt.Errorf("no fills in %s", dbPath)
	}
	in.To = time.UnixMilli(newest).Truncate(time.Hour)
	in.From = in.To.Add(-time.Duration(days) * 24 * time.Hour)
	if first := time.UnixMilli(oldest).Add(time.Duration(warmup) * 24 * time.Hour); in.From.Before(first) {
		in.From = first
	}
	fmt.Fprintf(os.Stderr, "%d traders (%d still followed), %d fills, %d symbols; replaying %s → %s\n",
		len(ids), len(follows), total, len(symbols), in.From.UTC().Format("2006-01-02 15:04"), in.To.UTC().Format("2006-01-02 15:04"))

	in.Bars, err = loadBars(symbols, in.To, cachePath)
	if err != nil {
		return err
	}
	// Stock and commodity contracts are their own class, as on the server.
	copytrade.ClassOf = func(symbol string) string {
		switch {
		case symbol == "BTCUSDT" || symbol == "ETHUSDT":
			return copytrade.ClassMajor
		case binance.IsTradFiSymbol(symbol):
			return copytrade.ClassTradFi
		}
		return copytrade.ClassAlt
	}
	started := time.Now()
	res := copytrade.Backtest(in)
	fmt.Fprintf(os.Stderr, "replayed %d hours, %d signal rows in %s\n", res.Hours, res.Signals, time.Since(started).Round(time.Second))
	print(res)
	return nil
}

// loadBars downloads up to 1500 closed hourly candles per symbol, reusing the
// cache file when it reaches to.
func loadBars(symbols map[string]bool, to time.Time, cachePath string) (map[string][]copytrade.Bar, error) {
	bars := map[string][]copytrade.Bar{}
	if cachePath != "" {
		if raw, err := os.ReadFile(cachePath); err == nil {
			_ = json.Unmarshal(raw, &bars)
		}
	}
	list := make([]string, 0, len(symbols))
	for s := range symbols {
		list = append(list, s)
	}
	sort.Strings(list)
	fetched, missing := 0, 0
	for _, symbol := range list {
		if b, ok := bars[symbol]; ok && (len(b) == 0 || b[len(b)-1].Time >= to.Unix()-3600) {
			continue // cached, or known to have no candles
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		rows, err := binance.GetPublicKlines(ctx, symbol, "1h", 1500)
		cancel()
		if err != nil {
			missing++
			bars[symbol] = []copytrade.Bar{} // delisted or not a futures symbol
			continue
		}
		if len(rows) > 0 && time.Unix(rows[len(rows)-1].Time, 0).Add(time.Hour).After(time.Now()) {
			rows = rows[:len(rows)-1]
		}
		out := make([]copytrade.Bar, len(rows))
		for i, r := range rows {
			out[i] = copytrade.Bar{Time: r.Time, Close: r.Close}
		}
		bars[symbol] = out
		if fetched++; fetched%25 == 0 {
			fmt.Fprintf(os.Stderr, "candles: %d downloaded\n", fetched)
		}
	}
	fmt.Fprintf(os.Stderr, "candles: %d symbols (%d downloaded now, %d without data)\n", len(list), fetched, missing)
	if cachePath != "" && fetched+missing > 0 {
		if raw, err := json.Marshal(bars); err == nil {
			_ = os.WriteFile(cachePath, raw, 0o600)
		}
	}
	return bars, nil
}

func print(res copytrade.BacktestResult) {
	titles := map[string]string{
		copytrade.GroupConfidence: "Confidence (who holds) — top 30 an hour, by band",
		copytrade.GroupOpeners:    "Confidence signals by fresh openers on the leading side",
		copytrade.GroupShadow:     "Fresh-call score (who opened lately) — by band",
		copytrade.GroupCalls:      "Fresh-call signals by independent callers",
	}
	horizon := flag.Lookup("horizon").Value.String()
	for _, group := range []string{copytrade.GroupConfidence, copytrade.GroupOpeners, copytrade.GroupShadow, copytrade.GroupCalls} {
		fmt.Printf("\n%s\n", titles[group])
		fmt.Printf("%-5s %-5s %-4s %6s %6s %8s | %6s %8s\n", "band", "side", "hrz", "calls", "hit%", "avg%", "beat%", "excess%")
		for _, r := range res.Rates {
			if r.Group != group || r.Samples == 0 || (horizon != "" && r.Horizon != horizon) {
				continue
			}
			fmt.Printf("%-5s %-5s %-4s %6d %6.1f %8.2f | %6.1f %8.2f\n", r.Band, r.Direction, r.Horizon, r.Samples,
				100*r.Rate, 100*r.AvgReturn, 100*r.ExcessRate, 100*r.AvgExcess)
		}
	}
	fmt.Println("\nhit% / avg%: price moved the called way, mean move. beat% / excess%: the same against BTC over the same hours.")
	fmt.Println("A run of the same call hour after hour counts once, from its first hour. Traders are those whose fills are stored")
	fmt.Println("(the survivors), all positions are inferred from fills, and every trader scores the same: compare the rows, do not")
	fmt.Println("read them as expected returns.")
}
