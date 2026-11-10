package market

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type quoteTransport func(*http.Request) (*http.Response, error)

func (f quoteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQuotePublicReadIsFreshUncachedAndExchangeSpecific(t *testing.T) {
	now := time.Now()
	cases := []struct {
		exchange, symbol, host, path, response string
		want                                   float64
	}{
		{"binance_paper", "SOLUSDT", "fapi.binance.com", "/fapi/v1/premiumIndex", fmt.Sprintf(`{"symbol":"SOLUSDT","markPrice":"95.87","time":%d}`, now.UnixMilli()), 95.87},
		{"binance", "SNDKUSDT", "fapi.binance.com", "/fapi/v1/premiumIndex", fmt.Sprintf(`{"symbol":"SNDKUSDT","markPrice":"1100","time":%d}`, now.UnixMilli()), 1100},
		{"bybit", "SOLUSDT", "api.bybit.com", "/v5/market/tickers", fmt.Sprintf(`{"retCode":0,"time":%d,"result":{"list":[{"symbol":"SOLUSDT","lastPrice":"95.87"}]}}`, now.UnixMilli()), 95.87},
		{"okx", "SOLUSDT", "www.okx.com", "/api/v5/market/ticker", fmt.Sprintf(`{"code":"0","data":[{"last":"95.87","ts":"%d"}]}`, now.UnixMilli()), 95.87},
		{"bitget", "SOLUSDT", "api.bitget.com", "/api/v2/mix/market/ticker", fmt.Sprintf(`{"code":"00000","data":[{"lastPr":"95.87","ts":"%d"}]}`, now.UnixMilli()), 95.87},
		{"hyperliquid", "xyz:SNDK", "api.hyperliquid.xyz", "/info", fmt.Sprintf(`{"time":%d,"levels":[[{"px":"1099"}],[{"px":"1101"}]]}`, now.UnixMilli()), 1100},
	}
	for _, c := range cases {
		t.Run(c.exchange+c.symbol, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: quoteTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != c.host || r.URL.Path != c.path {
					t.Errorf("wrong route: %s", r.URL)
				}
				if r.Header.Get("Cache-Control") != "no-cache, no-store" {
					t.Error("fresh reference must disable cache without bypassing scheduler")
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("X-MBX-APIKEY") != "" {
					t.Error("public read must not use credentials")
				}
				if c.exchange == "hyperliquid" {
					b, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(b), `"coin":"xyz:SNDK"`) {
						t.Errorf("HIP3 namespace lost: %s", b)
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(c.response))}, nil
			})}
			for i := 0; i < 2; i++ {
				q, err := getReferenceQuote(client, c.symbol, c.exchange)
				if err != nil || q.Price != c.want || !q.Fresh(time.Now()) {
					t.Fatalf("quote=%+v err=%v", q, err)
				}
			}
			if calls != 2 {
				t.Fatalf("quote must not cache/coalesce: %d calls", calls)
			}
		})
	}
}

func TestQuoteRejectsInvalidAndDoesNotInventTimestamps(t *testing.T) {
	for _, c := range []struct {
		body           string
		invalid, fresh bool
	}{
		{`{"symbol":"SOLUSDT","markPrice":"95.87"}`, false, false},
		{`{"symbol":"SOLUSDT","markPrice":"95.87","time":1700000000000}`, false, false},
		{fmt.Sprintf(`{"symbol":"SOLUSDT","markPrice":"95.87","time":%d}`, time.Now().Add(time.Hour).UnixMilli()), false, false},
		{`{"symbol":"SOLUSDT","markPrice":"NaN"}`, true, false},
		{`{"symbol":"SOLUSDT","markPrice":"+Inf"}`, true, false},
		{`{"symbol":"SOLUSDT","markPrice":"0"}`, true, false},
		{`{"symbol":"ETHUSDT","markPrice":"95.87"}`, true, false},
	} {
		client := &http.Client{Transport: quoteTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(c.body))}, nil
		})}
		q, err := getReferenceQuote(client, "SOLUSDT", "binance_paper")
		if (err != nil) != c.invalid {
			t.Fatalf("body=%s quote=%+v error=%v", c.body, q, err)
		}
		if !c.invalid && q.Fresh(time.Now()) != c.fresh {
			t.Fatalf("old/unknown timestamp accepted: %+v", q)
		}
	}
}

func TestQuoteCandleTimestampsAndIndicatorSeparation(t *testing.T) {
	now := time.Now()
	open := now.Truncate(time.Minute)
	candles := []Kline{{OpenTime: open.UnixMilli(), CloseTime: open.Add(time.Minute).UnixMilli() - 1, Open: 96, High: 97, Low: 95, Close: 96.48, Volume: 25}}
	asOf, issue := candleMetadata(candles, "1m", now)
	if !asOf.Equal(open) || issue != "" {
		t.Fatalf("scheduled future close must not become as-of: %v %s", asOf, issue)
	}
	series := calculateTimeframeSeries(candles, "1m", 1)
	data := &Data{IndicatorPrice: 96.48, IndicatorAsOf: asOf, IndicatorTimeframe: "1m", CurrentEMA20: 96, TimeframeData: map[string]*TimeframeSeriesData{"1m": series}}
	data.SetQuote(&Quote{Price: 95.87, Source: "test", AsOf: now, ObservedAt: now})
	if data.CurrentPrice != 95.87 || data.IndicatorPrice != 96.48 || series.Klines[0].Close != 96.48 || data.CurrentEMA20 != 96 || !data.OpeningAllowed(now) {
		t.Fatalf("quote rewrote indicators or fresh data rejected: %+v", data)
	}
	for _, stamp := range []int64{0, now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli(), now.Unix()} {
		bad := append([]Kline(nil), candles...)
		bad[0].OpenTime = stamp
		_, issue = candleMetadata(bad, "1m", now)
		if issue == "" {
			t.Errorf("invalid/stale timestamp %d accepted", stamp)
		}
	}
	data.IndicatorAsOf = now.Add(-time.Hour)
	if data.OpeningAllowed(now) {
		t.Error("fresh quote must not conceal stale indicators")
	}
	data.IndicatorAsOf = now
	series.FreshnessIssue = "consecutive candle price freeze"
	if data.OpeningAllowed(now) {
		t.Error("fresh primary must not conceal frozen secondary timeframe")
	}
	data.SetQuote(nil)
	if data.CurrentPrice != 0 || data.OpeningAllowed(now) {
		t.Error("candle-only data masquerades as live quote")
	}
}
