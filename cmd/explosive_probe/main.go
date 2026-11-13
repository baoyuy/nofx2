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
	timeout := flag.Duration("timeout", 90*time.Second, "scan timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	start := time.Now()
	board, err := explosive.Shared.Board(ctx, true)
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
	for i, r := range board.Rows {
		fmt.Printf("%2d %-12s %5.1f %-12s 24h=%+7.2f%% 5m=%+5.2f%% volx=%4.1f oi=%+6.2f%% fund=%+.4f%% %v\n",
			i+1, r.Symbol, r.Score, r.State, r.Change24h, r.Change5m, r.VolRatio, r.OIChange1h*100, r.Funding*100, r.Tags)
	}
}
