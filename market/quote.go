package market

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"nofx/internal/apischeduler"
	"nofx/provider/hyperliquid"
	"strconv"
	"strings"
	"time"
)

// Quote is an independent observation, not an indicator input or an execution
// guarantee. ObservedAt is local receipt time; AsOf is the exchange timestamp.
// Unknown exchange timestamps stay zero (receipt time must not conceal stale data).
type Quote struct {
	Price      float64
	Source     string
	AsOf       time.Time
	ObservedAt time.Time
}

const QuoteMaxAge = 30 * time.Second
const clockSkew = 5 * time.Second

func validPrice(p float64) bool { return p > 0 && !math.IsNaN(p) && !math.IsInf(p, 0) }

func (q *Quote) Fresh(now time.Time) bool {
	return q != nil && validPrice(q.Price) && !q.AsOf.IsZero() && !q.ObservedAt.IsZero() &&
		now.Sub(q.AsOf) <= QuoteMaxAge && now.Sub(q.AsOf) >= -clockSkew &&
		now.Sub(q.ObservedAt) <= QuoteMaxAge && now.Sub(q.ObservedAt) >= -clockSkew
}

// SetQuote changes only the current reference, never OHLCV/EMA/RSI/price changes.
func (d *Data) SetQuote(q *Quote) {
	d.Quote = q
	d.CurrentPrice = 0
	if q != nil && validPrice(q.Price) {
		d.CurrentPrice = q.Price
	}
}

// OpeningAllowed is deliberately fail-closed for unknown timestamps. Existing
// holdings may still be managed or exited regardless of this opening-only gate.
func (d *Data) OpeningAllowed(now time.Time) bool {
	if d == nil || !d.Quote.Fresh(now) || d.IndicatorIssue != "" ||
		candleTimeIssue(d.IndicatorAsOf, d.IndicatorTimeframe, now) != "" {
		return false
	}
	for tf, s := range d.TimeframeData {
		if s == nil || s.FreshnessIssue != "" || candleTimeIssue(s.AsOf, tf, now) != "" {
			return false
		}
	}
	return true
}

// Candle times are period boundaries, not quote timestamps. A current unfinished
// candle's scheduled CloseTime is in the future and must not be reported as as-of.
func candleTimeIssue(open time.Time, tf string, now time.Time) string {
	duration, err := candleTFDuration(tf)
	if err != nil || open.IsZero() {
		return "unknown candle timestamp/timeframe"
	}
	if open.After(now.Add(clockSkew)) {
		return "candle timestamp is in the future"
	}
	if now.Sub(open) > duration+2*time.Minute {
		return "stale candle timestamp"
	}
	return ""
}

func candleMetadata(klines []Kline, tf string, now time.Time) (time.Time, string) {
	if len(klines) == 0 {
		return time.Time{}, "candles unavailable"
	}
	last := klines[len(klines)-1]
	var asOf time.Time
	if last.OpenTime > 0 {
		asOf = time.UnixMilli(last.OpenTime)
	}
	if issue := candleTimeIssue(asOf, tf, now); issue != "" {
		return asOf, issue
	}
	duration, _ := candleTFDuration(tf)
	// Validate known close timestamps too, but don't substitute scheduled close
	// for observation time or silently turn seconds into milliseconds.
	if last.CloseTime > 0 && (last.CloseTime < last.OpenTime || last.CloseTime > last.OpenTime+duration.Milliseconds()+1000) {
		return asOf, "invalid candle close timestamp"
	}
	for i := 1; i < len(klines); i++ {
		if klines[i].OpenTime <= klines[i-1].OpenTime {
			return asOf, "non-monotonic candle timestamps"
		}
	}
	if isStaleData(klines, "") {
		return asOf, "consecutive candle price freeze"
	}
	return asOf, ""
}

// GetReferenceQuote makes exactly one public, uncached exchange read, admitted
// by the shared scheduler. No paid provider, retry, or cross-exchange fallback.
func GetReferenceQuote(symbol, exchange string) (*Quote, error) {
	return getReferenceQuote(NewAPIClient().client, symbol, exchange)
}

func getReferenceQuote(client *http.Client, symbol, exchange string) (*Quote, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if exchange == "" {
		exchange = "binance"
	}
	symbol = NormalizeFor(symbol, exchange)
	var endpoint, body, source string
	switch exchange {
	case "binance", "binance_paper", "aster":
		host := "https://fapi.binance.com"
		if exchange == "aster" {
			host = "https://fapi.asterdex.com"
		}
		endpoint = host + "/fapi/v1/premiumIndex?symbol=" + url.QueryEscape(symbol)
		source = exchange + ":public_mark_price"
	case "bybit":
		endpoint = "https://api.bybit.com/v5/market/tickers?category=linear&symbol=" + url.QueryEscape(symbol)
		source = "bybit:public_last_trade"
	case "okx":
		inst := strings.TrimSuffix(symbol, "USDT") + "-USDT-SWAP"
		endpoint = "https://www.okx.com/api/v5/market/ticker?instId=" + url.QueryEscape(inst)
		source = "okx:public_last_trade"
	case "bitget":
		endpoint = "https://api.bitget.com/api/v2/mix/market/ticker?productType=USDT-FUTURES&symbol=" + url.QueryEscape(symbol)
		source = "bitget:public_last_trade"
	case "hyperliquid":
		coin := hyperliquid.FormatCoinForAPI(symbol)
		payload, _ := json.Marshal(map[string]string{"type": "l2Book", "coin": coin})
		body = string(payload)
		endpoint = "https://api.hyperliquid.xyz/info"
		source = "hyperliquid:public_book_mid"
	default:
		return nil, fmt.Errorf("fresh public reference quote unsupported for %s", exchange)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	method := http.MethodGet
	if body != "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	// Explicitly opt out of scheduler cache/coalescing without bypassing budgets.
	req.Header.Set("Cache-Control", "no-cache, no-store")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := apischeduler.Do(client, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reference quote HTTP %d", resp.StatusCode)
	}
	var raw struct {
		Symbol    string `json:"symbol"`
		Price     string `json:"price"`
		MarkPrice string `json:"markPrice"`
		Time      int64  `json:"time"`
		RetCode   int    `json:"retCode"`
		Code      string `json:"code"`
		Result    struct {
			List []struct {
				Symbol    string `json:"symbol"`
				LastPrice string `json:"lastPrice"`
			} `json:"list"`
		} `json:"result"`
		Data []struct {
			Last   string `json:"last"`
			LastPr string `json:"lastPr"`
			Ts     string `json:"ts"`
		} `json:"data"`
		Levels [][]struct {
			Px string `json:"px"`
		} `json:"levels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	price, stamp := raw.Price, raw.Time
	switch exchange {
	case "binance", "binance_paper", "aster":
		if raw.Symbol != symbol {
			return nil, fmt.Errorf("reference quote symbol mismatch")
		}
		price = raw.MarkPrice
	case "bybit":
		if raw.RetCode != 0 || len(raw.Result.List) != 1 || raw.Result.List[0].Symbol != symbol {
			return nil, fmt.Errorf("invalid bybit quote")
		}
		price = raw.Result.List[0].LastPrice
	case "okx", "bitget":
		if (raw.Code != "0" && raw.Code != "00000") || len(raw.Data) != 1 {
			return nil, fmt.Errorf("invalid %s quote", exchange)
		}
		price = raw.Data[0].Last
		if exchange == "bitget" {
			price = raw.Data[0].LastPr
		}
		stamp, _ = strconv.ParseInt(raw.Data[0].Ts, 10, 64)
	case "hyperliquid":
		if len(raw.Levels) != 2 || len(raw.Levels[0]) == 0 || len(raw.Levels[1]) == 0 {
			return nil, fmt.Errorf("empty hyperliquid book")
		}
		bid, _ := strconv.ParseFloat(raw.Levels[0][0].Px, 64)
		ask, _ := strconv.ParseFloat(raw.Levels[1][0].Px, 64)
		if !validPrice(bid) || !validPrice(ask) || ask < bid {
			return nil, fmt.Errorf("invalid hyperliquid book")
		}
		price = strconv.FormatFloat((bid+ask)/2, 'f', -1, 64)
	}
	p, err := strconv.ParseFloat(price, 64)
	if err != nil || !validPrice(p) {
		return nil, fmt.Errorf("invalid reference quote price")
	}
	q := &Quote{Price: p, Source: source, ObservedAt: time.Now()}
	if stamp > 0 {
		q.AsOf = time.UnixMilli(stamp)
	}
	// Return stale/unknown observations with honest timestamps for diagnostics;
	// Fresh/OpeningAllowed, not HTTP success, decides opening eligibility.
	return q, nil
}

func candleTFDuration(tf string) (time.Duration, error) {
	if d, err := TFDuration(tf); err == nil {
		return d, nil
	}
	switch tf {
	case "8h":
		return 8 * time.Hour, nil
	case "3d":
		return 72 * time.Hour, nil
	case "1w":
		return 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported candle timeframe %q", tf)
	}
}
