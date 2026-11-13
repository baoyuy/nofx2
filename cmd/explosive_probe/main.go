// Command explosive_probe prints the explosive board once, for checking the
// live feed from a machine that can reach Binance.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"nofx/explosive"
)

func main() {
	jsonOut := flag.Bool("json", false, "print the board as JSON")
	watch := flag.Int("watch", 0, "poll the board once a second for N seconds and print the top row")
	all := flag.Bool("all", false, "print every refined row, not just the top 20")
	symbol := flag.String("symbol", "", "focus on one symbol, e.g. AKEUSDT")
	timeout := flag.Duration("timeout", 90*time.Second, "scan timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	start := time.Now()
	var board explosive.Board
	var err error
	if *all || *symbol != "" {
		board, err = explosive.Shared.ScanAll(ctx)
	} else {
		// First read may wait for a cold scan; the second shows the warm cost
		// the page actually pays on every refresh.
		board, err = explosive.Shared.Board(ctx, true)
		if err == nil {
			second := time.Now()
			board, err = explosive.Shared.Board(ctx, false)
			if !*jsonOut {
				fmt.Printf("cold=%s warm=%s age=%ds\n",
					second.Sub(start).Round(time.Millisecond), time.Since(second).Round(time.Millisecond), board.AgeSeconds)
			}
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		os.Exit(1)
	}
	if *watch > 0 {
		_, _ = explosive.Shared.Board(ctx, true) // warm up
		for i := 0; i < *watch; i++ {
			board, err := explosive.Shared.Board(ctx, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "board: %v\n", err)
				return
			}
			if len(board.Rows) > 0 {
				r := board.Rows[0]
				fmt.Printf("%s %-12s price=%-12.6f 24h=%+7.2f%% 5m=%+6.2f%% strength=%5.1f age=%ds %s\n",
					time.Now().Format("15:04:05"), r.Symbol, r.Price, r.Change24h, r.Change5m, r.Strength, board.AgeSeconds, r.State)
			}
			time.Sleep(time.Second)
		}
		return
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(board)
		return
	}
	fmt.Printf("scanned=%d candidates=%d rows=%d total=%s\n",
		board.Scanned, board.Candidates, len(board.Rows), time.Since(start).Round(time.Second))
	if board.Reason != "" {
		fmt.Printf("reason: %s\n", board.Reason)
	}
	show := board.Rows
	if *symbol != "" {
		show = show[:0]
		for _, r := range board.Rows {
			if r.Symbol == *symbol {
				show = append(show, r)
			}
		}
	}
	for i, r := range show {
		rank := i + 1
		for j := range board.Rows {
			if board.Rows[j].Symbol == r.Symbol {
				rank = j + 1
			}
		}
		fmt.Printf("%2d %-12s pred=%+6.2f%% day=%+7.2f%% ran=%4.1fh slope=%+6.1f%% %-12s volx=%4.1f oi=%+6.2f%% fund=%+.4f%% %v\n",
			rank, r.Symbol, r.PredictedRemaining, r.DayGain*100, r.ElapsedH, r.VolumeSlope*100, r.State,
			r.VolRatio, r.OIChange1h*100, r.Funding*100, r.Tags)
	}
}
