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
		board, err = explosive.Shared.Board(ctx, true)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		os.Exit(1)
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(board)
		return
	}
	fmt.Printf("scanned=%d candidates=%d rows=%d took=%s\n",
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
		fmt.Printf("%2d %-12s strength=%5.1f risk=%.2f score=%5.1f %-12s 24h=%+7.2f%% 5m=%+5.2f%% volx=%4.1f oi=%+6.2f%% fund=%+.4f%% parts[speed=%.2f vol=%.2f oi=%.2f struct=%.2f rel=%.2f] %v\n",
			rank, r.Symbol, r.Strength, r.Risk, r.Score, r.State, r.Change24h, r.Change5m, r.VolRatio,
			r.OIChange1h*100, r.Funding*100, r.Speed, r.Volume, r.OpenInt, r.Structure, r.RelStrength, r.Tags)
	}
}
