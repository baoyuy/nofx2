// Export replayed events using the production parser; read-only SQLite.
package main

import (
	"encoding/json"
	"flag"
	"os"
	"sort"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gl "gorm.io/gorm/logger"
	"nofx/copytrade"
	"nofx/store"
)

type record struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	LatestROI     float64           `json:"latest_roi"`
	LatestPnL     float64           `json:"latest_pnl"`
	LatestMDD     float64           `json:"latest_mdd"`
	TrainingStyle copytrade.Style   `json:"training_style"`
	Events        []copytrade.Event `json:"events"`
}

func main() {
	path := flag.String("db", "", "read-only SQLite copy")
	out := flag.String("out", "", "JSON output")
	cut := flag.Int64("cut", 0, "style training cutoff in milliseconds")
	flag.Parse()
	if *path == "" || *out == "" || *cut <= 0 {
		panic("db/out/cut required")
	}
	db, err := gorm.Open(sqlite.Open("file:"+*path+"?mode=ro"), &gorm.Config{Logger: gl.Default.LogMode(gl.Silent)})
	must(err)
	st := store.NewCopyTradeStore(db)
	follows, err := st.ListFollows()
	must(err)
	names := map[string]store.CopyTradeFollow{}
	for _, f := range follows {
		names[f.PortfolioID] = f
	}
	ids, err := st.FillTraders()
	must(err)
	sort.Strings(ids)
	result := []record{}
	for _, id := range ids {
		rows, err := st.Fills(id, 0)
		must(err)
		fills, train := []copytrade.Fill{}, []copytrade.Fill{}
		for _, f := range rows {
			v := copytrade.Fill{Time: f.Time, Seq: f.Seq, Symbol: f.Symbol, Side: f.Side, PositionSide: f.PositionSide, Qty: f.Qty, Price: f.Price, Fee: f.Fee, RealizedPnL: f.RealizedPnL}
			fills = append(fills, v)
			if v.Time < *cut {
				train = append(train, v)
			}
		}
		_, events := copytrade.Replay(fills)
		f := names[id]
		result = append(result, record{ID: id, Name: f.Nickname, LatestROI: f.ROI, LatestPnL: f.PnL, LatestMDD: f.MDD, TrainingStyle: copytrade.StyleOf(train), Events: events})
	}
	data, err := json.Marshal(map[string]any{"cut": *cut, "created_at": time.Now().UTC(), "traders": result})
	must(err)
	must(os.WriteFile(*out, data, 0600))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
