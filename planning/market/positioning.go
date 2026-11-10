package market

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"nofx/planning/plugins"
	"strconv"
	"strings"
	"time"
)

// Free Binance futures positioning plugins. They replace the paid Claw402
// holder analytics with public aggregate statistics.
const (
	PluginLongShort    = "long_short_ratio"
	PluginTakerFlow    = "taker_flow"
	PluginOpenInterest = "oi_trend"
)

func positioningPlugin(id string) bool {
	return id == PluginLongShort || id == PluginTakerFlow || id == PluginOpenInterest
}

// fraction formats a computed value as a plain decimal accepted by the
// plugin registry (no exponent).
func fraction(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

func parsePositive(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v, err == nil && v > 0
}

func dataAt(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

func (c *Client) positioningData(ctx context.Context, id string, q plugins.Request, out plugins.Result) (plugins.Result, error) {
	if q.Venue != "binance" || !strings.HasSuffix(q.Symbol, "USDT") {
		return out, errors.New("This plugin supports Binance USDT futures only")
	}
	base := c.BinanceFutures + "/futures/data/"
	symbol := url.QueryEscape(q.Symbol)
	switch id {
	case PluginLongShort:
		type row struct {
			Symbol    string `json:"symbol"`
			Ratio     string `json:"longShortRatio"`
			Long      string `json:"longAccount"`
			Timestamp int64  `json:"timestamp"`
		}
		var global, top []row
		if err := c.request(ctx, http.MethodGet, base+"globalLongShortAccountRatio?period=1h&limit=1&symbol="+symbol, nil, &global); err != nil {
			return out, err
		}
		if err := c.request(ctx, http.MethodGet, base+"topLongShortPositionRatio?period=1h&limit=1&symbol="+symbol, nil, &top); err != nil {
			return out, err
		}
		if len(global) != 1 || len(top) != 1 || global[0].Symbol != q.Symbol || top[0].Symbol != q.Symbol || global[0].Timestamp <= 0 {
			return out, errors.New("invalid long/short ratio response")
		}
		out.Source = base + "globalLongShortAccountRatio · topLongShortPositionRatio"
		out.Metrics = []plugins.Metric{
			{Key: "account_ratio", Label: "All accounts long/short ratio", Value: global[0].Ratio},
			{Key: "long_accounts", Label: "Share of accounts net long", Value: global[0].Long, Unit: "fraction"},
			{Key: "top_position_ratio", Label: "Top traders long/short position ratio", Value: top[0].Ratio},
			{Key: "top_long_positions", Label: "Top traders long position share", Value: top[0].Long, Unit: "fraction"},
		}
		out.Notice = "Binance 1h aggregate positioning as of " + dataAt(global[0].Timestamp) + ". A crowded side can unwind; this is context, not a direction signal."
	case PluginTakerFlow:
		var rows []struct {
			Ratio     string `json:"buySellRatio"`
			Buy       string `json:"buyVol"`
			Sell      string `json:"sellVol"`
			Timestamp int64  `json:"timestamp"`
		}
		if err := c.request(ctx, http.MethodGet, base+"takerlongshortRatio?period=1h&limit=24&symbol="+symbol, nil, &rows); err != nil {
			return out, err
		}
		if len(rows) == 0 {
			return out, errors.New("invalid taker flow response")
		}
		var buy, sell float64
		for _, r := range rows {
			b, okB := parsePositive(r.Buy)
			s, okS := parsePositive(r.Sell)
			if !okB || !okS {
				return out, errors.New("invalid taker flow volume")
			}
			buy, sell = buy+b, sell+s
		}
		last := rows[len(rows)-1]
		out.Source = base + "takerlongshortRatio"
		out.Metrics = []plugins.Metric{
			{Key: "taker_ratio_1h", Label: "Latest 1h taker buy/sell ratio", Value: last.Ratio},
			{Key: "taker_ratio_24h", Label: strconv.Itoa(len(rows)) + "h taker buy/sell ratio", Value: fraction(buy / sell)},
			{Key: "taker_buy_share_24h", Label: strconv.Itoa(len(rows)) + "h aggressive buy share", Value: fraction(buy / (buy + sell)), Unit: "fraction"},
		}
		out.Notice = "Binance taker (market-order) volume, latest period " + dataAt(last.Timestamp) + ". Above 1 means aggressive buying outweighed selling."
	case PluginOpenInterest:
		var rows []struct {
			Symbol    string `json:"symbol"`
			OI        string `json:"sumOpenInterest"`
			Value     string `json:"sumOpenInterestValue"`
			Timestamp int64  `json:"timestamp"`
		}
		if err := c.request(ctx, http.MethodGet, base+"openInterestHist?period=1h&limit=25&symbol="+symbol, nil, &rows); err != nil {
			return out, err
		}
		if len(rows) < 5 || rows[len(rows)-1].Symbol != q.Symbol {
			return out, errors.New("invalid open interest history response")
		}
		values := make([]float64, len(rows))
		for i, r := range rows {
			v, ok := parsePositive(r.Value)
			if !ok {
				return out, errors.New("invalid open interest value")
			}
			values[i] = v
		}
		n := len(values)
		last := rows[n-1]
		out.Source = base + "openInterestHist"
		out.Metrics = []plugins.Metric{
			{Key: "oi_value", Label: "Open interest value", Value: last.Value, Unit: "USDT"},
			{Key: "oi_change_4h", Label: "OI value change over 4h", Value: fraction(values[n-1]/values[n-5] - 1), Unit: "fraction"},
			{Key: "oi_change_24h", Label: "OI value change over " + strconv.Itoa(n-1) + "h", Value: fraction(values[n-1]/values[0] - 1), Unit: "fraction"},
		}
		out.Notice = "Binance hourly open interest as of " + dataAt(last.Timestamp) + ". Rising OI with rising price suggests new longs; rising OI with falling price suggests new shorts."
	default:
		return out, errors.New("unknown public data plugin")
	}
	out.Mode = "public"
	out.Observed = time.Now().UTC()
	return out, nil
}
