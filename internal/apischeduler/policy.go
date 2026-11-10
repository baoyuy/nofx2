package apischeduler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func providerFor(r *http.Request) string {
	host := strings.ToLower(r.URL.Hostname())
	path := r.URL.Path
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()) {
		return ""
	}
	switch {
	case host == "fapi.binance.com" || host == "fapi.binance.me":
		return "binance-futures"
	case host == "demo-fapi.binance.com" || host == "testnet.binancefuture.com":
		return "binance-futures-testnet"
	case host == "www.binance.com" && strings.HasPrefix(path, "/bapi/futures/"):
		return "binance-copytrade"
	case host == "api.binance.com" || host == "data-api.binance.vision" || (strings.HasPrefix(host, "api") && strings.HasSuffix(host, ".binance.com")):
		return "binance-spot"
	case host == "api.bybit.com" || host == "api.bytick.com":
		return "bybit"
	case host == "api-testnet.bybit.com":
		return "bybit-testnet"
	case host == "www.okx.com" || host == "api.okx.com":
		return "okx"
	case host == "api.bitget.com":
		return "bitget"
	case host == "api.hyperliquid.xyz":
		return "hyperliquid"
	case host == "api.hyperliquid-testnet.xyz":
		return "hyperliquid-testnet"
	case host == "api.gateio.ws":
		return "gate"
	case host == "api-futures.kucoin.com":
		return "kucoin"
	case strings.HasSuffix(host, ".zklighter.elliot.ai"):
		return "lighter"
	case host == "fapi.asterdex.com":
		return "aster"
	case host == "indodax.com":
		return "indodax"
	case host == "claw402.ai":
		return "claw402"
	case host == "api.coinank.com" || host == "api.coinank.net":
		return "coinank"
	case host == "api.twelvedata.com":
		return "twelvedata"
	case host == "data.alpaca.markets":
		return "alpaca"
	default:
		return host // custom model/RPC endpoints get an isolated conservative pool
	}
}
func (s *Scheduler) policy(r *http.Request) requestPolicy {
	provider := providerFor(r)
	if provider == "" {
		return requestPolicy{}
	}
	profile, ok := s.cfg.Profiles[provider]
	if !ok {
		profile = s.cfg.Profiles[strings.TrimSuffix(provider, "-testnet")]
		if profile.WeightPerMinute == 0 {
			profile = s.cfg.Profiles["default"]
		}
	}
	weight := profile.DefaultWeight
	p := requestPolicy{provider: provider, maxConcurrent: profile.MaxConcurrent, priority: 1}
	path := r.URL.Path
	q := r.URL.Query()
	private := hasCredentials(r)
	p.signed = hasSignature(r)
	if private {
		p.priority = 2
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		p.priority = 2
	}
	if r.Method == http.MethodDelete {
		p.priority = 3
	}
	switch strings.TrimSuffix(provider, "-testnet") {
	case "binance-futures":
		weight = binanceWeight(r, weight)
		if strings.HasPrefix(path, "/futures/data/") || path == "/fapi/v1/fundingRate" || path == "/fapi/v1/fundingInfo" {
			p.limits = append(p.limits, Limit{Key: provider + ":history", Capacity: 400, Weight: 1, Window: 5 * time.Minute})
		}
		if r.Method == "POST" && (strings.HasSuffix(path, "/order") || strings.HasSuffix(path, "/algoOrder") || strings.HasSuffix(path, "/batchOrders")) {
			count := 1
			if raw := q.Get("batchOrders"); raw != "" {
				var orders []json.RawMessage
				if json.Unmarshal([]byte(raw), &orders) == nil {
					count = max(1, len(orders))
				} else {
					count = 5
				}
			}
			// Account keys are hashed and deliberately not exposed in diagnostics.
			key := provider + ":orders:" + accountKey(r)
			p.limits = append(p.limits, Limit{Key: key + ":10s", Capacity: 200, Window: 10 * time.Second, Weight: count}, Limit{Key: key + ":1m", Capacity: 600, Window: time.Minute, Weight: count})
		}
	case "binance-spot":
		if path == "/api/v3/exchangeInfo" {
			weight = 20
		}
		if path == "/api/v3/depth" {
			weight = 250
		}
		if path == "/api/v3/klines" {
			weight = 2
		}
		if path == "/api/v3/ticker/price" {
			weight = 4
		}
		if path == "/api/v3/ticker/24hr" {
			weight = 80
		}
	case "binance-copytrade":
		p.limits = append(p.limits, Limit{Key: provider + ":spacing", Capacity: 1, Window: 4 * time.Second, Weight: 1})
	case "bybit":
		// Group all API-key aliases by endpoint so they cannot multiply UID quotas.
		if private {
			capacity := 4
			if path == "/v5/order/cancel-all" {
				capacity = 1
			}
			p.limits = append(p.limits, Limit{Key: provider + ":private:" + r.Method + path, Capacity: capacity, Window: time.Second, Weight: 1})
		}
	case "okx", "bitget":
		if private {
			capacity := 1
			if strings.HasPrefix(path, "/api/v5/account/") || strings.HasPrefix(path, "/api/v5/trade/") || strings.HasPrefix(path, "/api/v5/public/") || strings.HasPrefix(path, "/api/v2/mix/") {
				capacity = 4
			}
			p.limits = append(p.limits, Limit{Key: provider + ":private:" + r.Method + path, Capacity: capacity, Window: time.Second, Weight: 1})
		}
	case "kucoin", "indodax":
		if private {
			p.limits = append(p.limits, Limit{Key: provider + ":private:" + r.Method + path, Capacity: 1, Window: time.Second, Weight: 1})
		}
	case "hyperliquid":
		body := requestObject(r)
		typ, _ := body["type"].(string)
		if path == "/info" {
			switch typ {
			case "l2Book", "allMids", "clearinghouseState", "orderStatus", "spotClearinghouseState", "exchangeStatus":
				weight = 2
			case "userRole":
				weight = 60
			case "candleSnapshot":
				weight = hyperliquidCandleWeight(body)
			case "userFills", "userFillsByTime", "historicalOrders", "recentTrades", "fundingHistory", "userFunding", "twapHistory":
				weight = 120
			case "meta", "spotMeta", "perpDexs", "metaAndAssetCtxs", "spotMetaAndAssetCtxs", "openOrders", "frontendOpenOrders", "extraAgents":
				weight = 20
			default:
				weight = 100
			} // reserve for variable response-size weights
			if body["user"] != nil {
				p.priority = 2
			}
		}
		if path == "/exchange" {
			p.signed = true
			p.priority = 3
			weight = 10 // reserve above the ordinary small-batch action weight
		}
	}
	for _, rule := range s.cfg.Rules {
		if rule.Provider == provider && rule.Method == r.Method && rule.Path == path {
			weight = rule.Weight
			if rule.Capacity > 0 {
				p.limits = append(p.limits, Limit{Key: provider + ":rule:" + rule.Method + rule.Path, Capacity: rule.Capacity, Window: time.Duration(rule.WindowSeconds) * time.Second, Weight: 1})
			}
		}
	}
	p.limits = append(p.limits, Limit{Key: provider + ":weight", Capacity: profile.WeightPerMinute, Window: time.Minute, Weight: weight}, Limit{Key: provider + ":requests", Capacity: profile.RequestsPerSecond, Window: time.Second, Weight: 1})
	// Reserve budget, not just a concurrency slot, for urgent private requests.
	// Public traffic consumes both the full pool and this narrower pool.
	if p.priority < 2 {
		p.limits = append(p.limits, Limit{Key: provider + ":background", Capacity: max(weight, profile.WeightPerMinute*4/5), Window: time.Minute, Weight: weight})
	}
	if !private && r.Method == http.MethodGet {
		p.cacheTTL = publicTTL(provider, path)
	}
	return p
}
func binanceWeight(r *http.Request, fallback int) int {
	path := r.URL.Path
	q := r.URL.Query()
	one := q.Get("symbol") != ""
	switch {
	case path == "/fapi/v1/klines" || path == "/fapi/v1/markPriceKlines" || path == "/fapi/v1/indexPriceKlines" || path == "/fapi/v1/continuousKlines":
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit == 0 {
			limit = 500
		}
		switch {
		case limit < 100:
			return 1
		case limit < 500:
			return 2
		case limit <= 1000:
			return 5
		default:
			return 10
		}
	case path == "/fapi/v1/ticker/24hr":
		if one {
			return 1
		}
		return 40
	case path == "/fapi/v1/premiumIndex":
		if one {
			return 1
		}
		return 10
	case path == "/fapi/v1/ticker/price" || path == "/fapi/v2/ticker/price" || path == "/fapi/v1/ticker/bookTicker":
		if one {
			return 2
		}
		return 5
	case path == "/fapi/v1/depth":
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 50 {
			return 2
		}
		if limit <= 100 {
			return 5
		}
		if limit <= 500 {
			return 10
		}
		return 20
	case path == "/fapi/v1/exchangeInfo" || path == "/fapi/v1/time" || path == "/fapi/v1/ping" || path == "/fapi/v1/openInterest":
		return 1
	case strings.HasSuffix(path, "/account") || strings.HasSuffix(path, "/balance") || strings.HasSuffix(path, "/positionRisk"):
		return 5
	case strings.HasSuffix(path, "/openOrders"):
		if one {
			return 1
		}
		return 40
	default:
		return fallback
	}
}
func requestObject(r *http.Request) map[string]any {
	if r.GetBody == nil {
		return nil
	}
	body, err := r.GetBody()
	if err != nil {
		return nil
	}
	defer body.Close()
	var out map[string]any
	_ = json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&out)
	return out
}
func hasCredentials(r *http.Request) bool {
	if r.URL.User != nil {
		return true
	}
	for key := range r.Header {
		key = strings.ToLower(key)
		if strings.Contains(key, "auth") || strings.Contains(key, "key") || strings.Contains(key, "sign") || strings.Contains(key, "secret") || strings.Contains(key, "payment") || key == "cookie" || key == "x-api-token" {
			return true
		}
	}
	for key := range r.URL.Query() {
		key = strings.ToLower(key)
		if strings.Contains(key, "sign") || strings.Contains(key, "key") || strings.Contains(key, "token") || key == "timestamp" || key == "user" || key == "address" || key == "secret" {
			return true
		}
	}
	return false
}
func hasSignature(r *http.Request) bool {
	for key := range r.Header {
		if strings.Contains(strings.ToLower(key), "sign") {
			return true
		}
	}
	q := r.URL.Query()
	return q.Has("signature") || q.Has("timestamp") || r.Header.Get("KC-API-TIMESTAMP") != "" || r.Header.Get("OK-ACCESS-TIMESTAMP") != ""
}
func accountKey(r *http.Request) string {
	raw := r.Header.Get("X-MBX-APIKEY")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}
func publicTTL(provider, path string) time.Duration {
	if provider != "binance-futures" && provider != "binance-spot" {
		return 0
	}
	switch path {
	case "/fapi/v1/exchangeInfo", "/api/v3/exchangeInfo":
		return 5 * time.Minute
	case "/fapi/v1/klines", "/api/v3/klines", "/fapi/v1/ticker/24hr", "/api/v3/ticker/24hr":
		return 2 * time.Second
	case "/fapi/v1/premiumIndex":
		return time.Second
	case "/fapi/v1/openInterest", "/fapi/v1/fundingRate", "/futures/data/openInterestHist":
		return 10 * time.Second
	default:
		return 0 // order books, timestamps and execution price checks are never reused
	}
}

// feedback never reads bodies: payment and streaming responses must stay intact.
func (s *Scheduler) feedback(req *http.Request, p requestPolicy, resp *http.Response) {
	if resp == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	paused := time.Time{}
	if resp.StatusCode == 429 || resp.StatusCode == 418 || (resp.StatusCode == 403 && (p.provider == "bybit" || strings.HasPrefix(p.provider, "binance"))) {
		delay := time.Minute
		if resp.StatusCode == 418 {
			delay = 15 * time.Minute
		}
		if resp.StatusCode == 403 {
			delay = 10 * time.Minute
		}
		if sec, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); err == nil && sec >= 0 && sec < 86400*365 {
			delay = max(delay, time.Duration(sec)*time.Second)
		} else if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			delay = max(delay, at.Sub(now))
		}
		paused = now.Add(delay)
		s.stat(p.provider).RateLimited++
	}
	// X-MBX headers include usage from outside this process on the same IP.
	header := resp.Header.Get("X-MBX-USED-WEIGHT-1M")
	observed, e := strconv.Atoi(header)
	for _, l := range p.limits {
		if s.windows[l.Key] == nil {
			s.windows[l.Key] = &window{}
		}
		w := s.windows[l.Key]
		if paused.After(w.pausedUntil) {
			w.pausedUntil = paused
		}
		if strings.HasSuffix(l.Key, ":weight") && e == nil && observed >= 0 {
			until := now.Truncate(time.Minute).Add(time.Minute)
			if !now.Before(w.observedUntil) || observed > w.observed {
				w.observed = observed
				w.observedAt = now
				w.observedUntil = until
			}
		}
	}
	// Bybit remaining quota can reach zero in an HTTP-200 response.
	if p.provider == "bybit" && resp.Header.Get("X-Bapi-Limit-Status") == "0" {
		until := now.Add(time.Second)
		if ms, err := strconv.ParseInt(resp.Header.Get("X-Bapi-Limit-Reset-Timestamp"), 10, 64); err == nil && time.UnixMilli(ms).After(until) {
			until = time.UnixMilli(ms)
		}
		for _, l := range p.limits {
			w := s.windows[l.Key]
			if until.After(w.pausedUntil) {
				w.pausedUntil = until
			}
		}
	}
	s.signal()
}

// Candle responses add weight per returned batch. Bound the number by the
// requested range plus inclusive endpoints, and by the upstream's 5,000-bar cap.
// If the body cannot be classified, reserve the maximum rather than undercount.
func hyperliquidCandleWeight(body map[string]any) int {
	req, ok := body["req"].(map[string]any)
	if !ok {
		return 120
	}
	interval, _ := req["interval"].(string)
	start, a := req["startTime"].(float64)
	end, b := req["endTime"].(float64)
	if !a || !b || end < start || len(interval) < 2 {
		return 120
	}
	n, err := strconv.Atoi(interval[:len(interval)-1])
	if err != nil || n <= 0 || n > 10000 {
		return 120
	}
	units := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour, 'M': 28 * 24 * time.Hour}
	unit := units[interval[len(interval)-1]]
	if unit == 0 {
		return 120
	}
	step := float64(unit/time.Millisecond) * float64(n)
	bars := (end-start)/step + 2
	if bars > 5000 {
		bars = 5000
	}
	return 20 + (int(bars)+59)/60
}
