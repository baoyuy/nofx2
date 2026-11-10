package marketboard

import (
	"context"
	"errors"
	"sort"
	"time"
)

// flowWindows maps a window to the candles that cover it.
var flowWindows = map[string]struct {
	interval string
	bars     int
}{
	"1h":  {"5m", 12},
	"4h":  {"15m", 16},
	"24h": {"1h", 24},
}

// FlowItem is one market's taker net flow. Numbers are strings to match the
// dashboard's existing response shape.
type FlowItem struct {
	Key          string `json:"key"`
	MarketType   string `json:"marketType"`
	Symbol       string `json:"symbol"`
	NetFlow      string `json:"netFlow"`
	BuyNotional  string `json:"buyNotional"`
	SellNotional string `json:"sellNotional"`
	Trades       int64  `json:"trades"`
	LatestPrice  string `json:"latestPrice"`
}

type FlowData struct {
	By      string     `json:"by"`
	Window  string     `json:"window"`
	Source  string     `json:"source"`
	AsOf    int64      `json:"asOf"`
	Inflow  []FlowItem `json:"inflow"`
	Outflow []FlowItem `json:"outflow"`
}

type FlowResponse struct {
	Data FlowData `json:"data"`
}

// Flow ranks the most traded perpetuals by taker net flow over window:
// taker buy notional minus taker sell notional. Inflow lists the largest
// positive flows, outflow the largest negative ones.
func (c *Client) Flow(ctx context.Context, window string, limit int) (FlowResponse, error) {
	w, ok := flowWindows[window]
	if !ok {
		window, w = "1h", flowWindows["1h"]
	}
	v, err := c.cachedDo(ctx, "flow:"+window, func(ctx context.Context) (any, error) {
		tickers, err := c.topByVolume(ctx, universe)
		if err != nil {
			return nil, err
		}
		items := make([]FlowItem, len(tickers))
		nets := make([]float64, len(tickers))
		ok := make([]bool, len(tickers))
		failed := forEach(tickers, func(i int, t Ticker) error {
			candles, err := c.klines(ctx, t.Symbol, w.interval, w.bars)
			if err != nil || len(candles) == 0 {
				return errors.New("candles unavailable")
			}
			var buy, total float64
			var trades int64
			for _, k := range candles {
				buy += k.TakerBuyQuote
				total += k.QuoteVolume
				trades += k.Trades
			}
			sell := total - buy
			nets[i], ok[i] = buy-sell, true
			items[i] = FlowItem{
				Key: t.Symbol, MarketType: "core_perp", Symbol: BaseSymbol(t.Symbol),
				NetFlow: fmtNum(buy - sell), BuyNotional: fmtNum(buy), SellNotional: fmtNum(sell),
				Trades: trades, LatestPrice: fmtNum(candles[len(candles)-1].Close),
			}
			return nil
		})
		if len(tickers) == 0 || failed > len(tickers)/2 {
			return nil, errors.New("Binance candles unavailable for net flow")
		}
		var in, out []int
		for i := range items {
			if !ok[i] {
				continue
			}
			if nets[i] >= 0 {
				in = append(in, i)
			} else {
				out = append(out, i)
			}
		}
		sort.SliceStable(in, func(a, b int) bool { return nets[in[a]] > nets[in[b]] })
		sort.SliceStable(out, func(a, b int) bool { return nets[out[a]] < nets[out[b]] })
		data := FlowData{By: "netFlow", Window: window, Source: "binance", AsOf: time.Now().UnixMilli(), Inflow: []FlowItem{}, Outflow: []FlowItem{}}
		for _, i := range in {
			data.Inflow = append(data.Inflow, items[i])
		}
		for _, i := range out {
			data.Outflow = append(data.Outflow, items[i])
		}
		return data, nil
	})
	if err != nil {
		return FlowResponse{}, err
	}
	data := v.(FlowData)
	if limit > 0 && len(data.Inflow) > limit {
		data.Inflow = data.Inflow[:limit]
	}
	if limit > 0 && len(data.Outflow) > limit {
		data.Outflow = data.Outflow[:limit]
	}
	return FlowResponse{Data: data}, nil
}
