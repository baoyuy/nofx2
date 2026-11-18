// Command copytrade_fetch fills a separate SQLite file with the trade history
// of the public lead traders at the top of Binance's leaderboard, for
// copytrade_backtest. It never touches the server's database, and it stops at
// the first sign of rate limiting: what was read by then is kept and usable.
// Run it again to continue: traders already read in full are skipped.
//
//	go run ./cmd/copytrade_fetch -db data/backtest/board.db -n 100
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"nofx/copytrade"
	"nofx/store"
)

func main() {
	dbPath := flag.String("db", "", "SQLite file to write (created if missing; not the app's data.db)")
	n := flag.Int("n", 100, "public traders to read, in leaderboard order")
	days := flag.Int("days", 30, "days of fills to read back")
	maxPages := flag.Int("max-pages", 30, "pages of 200 fills at most per trader")
	timeRange := flag.String("time-range", "90D", "leaderboard period: 7D, 30D or 90D")
	sortBy := flag.String("sort", "COPIER_PNL", "leaderboard order: COPIER_PNL, ROI or PNL")
	spacing := flag.Duration("spacing", 4*time.Second, "time between requests")
	flag.Parse()
	if *dbPath == "" || filepath.Base(*dbPath) == "data.db" {
		fmt.Fprintln(os.Stderr, "-db is required and must not be the app's data.db")
		os.Exit(2)
	}
	if err := run(*dbPath, *n, *days, *maxPages, *timeRange, *sortBy, *spacing); err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		os.Exit(1)
	}
}

func run(dbPath string, n, days, maxPages int, timeRange, sortBy string, spacing time.Duration) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return err
	}
	st := store.NewCopyTradeStore(db)
	if err := st.InitTables(); err != nil {
		return err
	}
	done := map[string]bool{}
	follows, err := st.ListFollows()
	if err != nil {
		return err
	}
	for _, f := range follows {
		if f.FillsSyncedAt > 0 {
			done[f.PortfolioID] = true
		}
	}
	c := copytrade.NewClient()
	c.Spacing = spacing
	ctx := context.Background()
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	started := time.Now()
	rank, taken, skipped, requests, fillsTotal := 0, 0, map[string]int{}, 0, 0
	stop := func(err error) error {
		fmt.Fprintf(os.Stderr, "\nSTOPPED: %v\n", err)
		return nil
	}
	report := func() {
		fmt.Fprintf(os.Stderr, "\n%d traders stored (%d already there), %d fills read now, about %d requests in %s; skipped: %v\n",
			taken, len(done), fillsTotal, requests, time.Since(started).Round(time.Second), skipped)
	}
	defer report()
	for page := 1; taken < n && page <= 50; page++ {
		board, err := c.Leaderboard(ctx, copytrade.LeaderQuery{TimeRange: timeRange, SortBy: sortBy, Page: page})
		requests++
		if err != nil {
			return stop(err)
		}
		if len(board.List) == 0 {
			break
		}
		for _, r := range board.List {
			if taken >= n {
				break
			}
			rank++
			if r.PortfolioType != "PUBLIC" {
				skipped[r.PortfolioType]++
				continue
			}
			if done[r.PortfolioID] {
				taken++
				continue
			}
			d, err := c.Detail(ctx, r.PortfolioID)
			requests++
			if errors.Is(err, copytrade.ErrClosed) {
				skipped["closed"]++
				continue
			}
			if err != nil {
				if copytrade.IsLimited(err) {
					return stop(err)
				}
				skipped["profile error"]++
				fmt.Fprintf(os.Stderr, "#%d %s: profile: %v\n", rank, r.Nickname, err)
				continue
			}
			f := store.CopyTradeFollow{PortfolioID: r.PortfolioID, Source: "board"}
			copytrade.ApplyDetail(&f, d)
			f.ROI, f.PnL, f.MDD, f.WinRate = float64(r.ROI), float64(r.PnL), float64(r.MDD), float64(r.WinRate)
			f.ProfileAt = time.Now().UnixMilli()
			if err := st.SaveFollow(&f); err != nil {
				return err
			}
			rows, reached, err := c.FetchFills(ctx, r.PortfolioID, since, maxPages)
			requests += (len(rows)+copytrade.TradePageSize-1)/copytrade.TradePageSize + 1
			if err != nil && !copytrade.IsLimited(err) && len(rows) == 0 {
				skipped["history error"]++
				fmt.Fprintf(os.Stderr, "#%d %s: history: %v\n", rank, f.Nickname, err)
				continue
			}
			// Only a history read without an error counts as done: a later
			// run reads the others again.
			synced, until := int64(0), int64(0)
			if len(rows) > 0 {
				until = rows[0].Time
			}
			if err == nil {
				synced = time.Now().UnixMilli()
			}
			if serr := st.ReplaceFills(r.PortfolioID, 0, copytrade.StoredFills(r.PortfolioID, rows), until, synced); serr != nil {
				return serr
			}
			fillsTotal += len(rows)
			span := 0.0
			if len(rows) > 0 {
				span = float64(rows[0].Time-rows[len(rows)-1].Time) / 86400000
			}
			note := ""
			if !reached {
				note = " (history capped: older fills not read)"
			}
			if len(rows) > 0 || err == nil {
				taken++
			}
			fmt.Fprintf(os.Stderr, "#%d %-24s %5d fills over %4.1f days%s\n", rank, f.Nickname, len(rows), span, note)
			if err != nil {
				return stop(err)
			}
		}
	}
	return nil
}
