package apischeduler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Profile contains local operating budgets, not a claim about upstream quotas.
// Keep these below documented limits and share a single process per egress IP.
type Profile struct {
	WeightPerMinute   int `json:"weight_per_minute"`
	RequestsPerSecond int `json:"requests_per_second"`
	MaxConcurrent     int `json:"max_concurrent"`
	DefaultWeight     int `json:"default_weight"`
}

// Rule overrides an endpoint's weight and optional independent window. Path is
// exact; no query or credentials belong in configuration. Provider is a profile key.
type Rule struct {
	Provider      string `json:"provider"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	Weight        int    `json:"weight"`
	Capacity      int    `json:"capacity,omitempty"`
	WindowSeconds int    `json:"window_seconds,omitempty"`
}
type Config struct {
	Profiles     map[string]Profile `json:"profiles"`
	Rules        []Rule             `json:"rules,omitempty"`
	MaxQueue     int                `json:"-"`
	MaxQueueWait time.Duration      `json:"-"`
	CacheEntries int                `json:"-"`
	CacheBytes   int                `json:"-"`
}

func DefaultConfig() Config {
	return Config{Profiles: map[string]Profile{
		"default":           {60, 2, 4, 1},
		"binance-futures":   {1200, 15, 8, 40},
		"binance-spot":      {2400, 10, 8, 100},
		"binance-copytrade": {12, 1, 1, 1},
		"bybit":             {1800, 40, 8, 1},
		"okx":               {600, 10, 8, 1},
		"bitget":            {600, 10, 8, 1},
		"hyperliquid":       {600, 10, 8, 100},
		"gate":              {300, 5, 6, 1},
		"kucoin":            {120, 3, 4, 5},
		"lighter":           {60, 2, 4, 1},
		"aster":             {300, 5, 6, 10},
		"indodax":           {60, 2, 4, 1},
		"claw402":           {60, 3, 4, 1},
		"coinank":           {60, 2, 4, 1},
		"twelvedata":        {6, 1, 2, 1},
		"alpaca":            {120, 4, 4, 1},
	}, MaxQueue: 512, MaxQueueWait: 20 * time.Second, CacheEntries: 512, CacheBytes: 32 << 20}
}
func (c Config) validate() error {
	if c.MaxQueue < 1 || c.MaxQueueWait <= 0 || c.CacheEntries < 1 || c.CacheBytes < 1 {
		return fmt.Errorf("invalid API scheduler queue/cache configuration")
	}
	if _, ok := c.Profiles["default"]; !ok {
		return fmt.Errorf("API scheduler requires default profile")
	}
	for name, p := range c.Profiles {
		if name == "" || p.WeightPerMinute < 1 || p.WeightPerMinute > 1000000 || p.RequestsPerSecond < 1 || p.RequestsPerSecond > 10000 || p.MaxConcurrent < 1 || p.MaxConcurrent > 256 || p.DefaultWeight < 1 || p.DefaultWeight > p.WeightPerMinute {
			return fmt.Errorf("invalid API scheduler profile %q", name)
		}
	}
	seen := map[string]bool{}
	for _, r := range c.Rules {
		if _, ok := c.Profiles[r.Provider]; !ok {
			return fmt.Errorf("unknown API scheduler rule provider %q", r.Provider)
		}
		key := r.Provider + "|" + r.Method + "|" + r.Path
		if r.Weight < 1 || r.Weight > c.Profiles[r.Provider].WeightPerMinute || !strings.HasPrefix(r.Path, "/") || strings.ContainsAny(r.Path, "?#") || r.Method == "" || seen[key] || (r.Capacity == 0) != (r.WindowSeconds == 0) || r.Capacity < 0 || r.WindowSeconds < 0 || r.WindowSeconds > 86400 {
			return fmt.Errorf("invalid API scheduler endpoint rule for %q", r.Provider)
		}
		seen[key] = true
	}
	return nil
}

var process struct {
	sync.Mutex
	scheduler *Scheduler
}

// ConfigureFromEnv validates overrides after .env is loaded and before the API or
// traders start. Invalid overrides fail closed rather than silently raising limits.
func ConfigureFromEnv() error {
	cfg := DefaultConfig()
	if raw := strings.TrimSpace(os.Getenv("NOFX_API_SCHEDULER_CONFIG")); raw != "" {
		var overrides struct {
			Profiles map[string]Profile `json:"profiles"`
			Rules    []Rule             `json:"rules"`
		}
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&overrides); err != nil {
			return fmt.Errorf("invalid NOFX_API_SCHEDULER_CONFIG JSON")
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("invalid trailing NOFX_API_SCHEDULER_CONFIG data")
		}
		for k, v := range overrides.Profiles {
			cfg.Profiles[k] = v
		}
		cfg.Rules = overrides.Rules
	}
	s, err := New(cfg)
	if err != nil {
		return err
	}
	process.Lock()
	defer process.Unlock()
	if process.scheduler != nil {
		return fmt.Errorf("API scheduler already initialized; configure before requests start")
	}
	process.scheduler = s
	return nil
}
func shared() *Scheduler {
	process.Lock()
	defer process.Unlock()
	if process.scheduler == nil {
		process.scheduler, _ = New(DefaultConfig())
	}
	return process.scheduler
}

// Snapshot returns process-wide counters without exposing request details.
func Snapshot() []Stats { return shared().Snapshot() }

type processTransport struct{ base http.RoundTripper }

func (t *processTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return (&Transport{base: t.base, scheduler: shared()}).RoundTrip(r)
}
func (t *processTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// Wrap clones only the client value. Original proxies/TLS, hooks, redirect rules,
// timeouts, and single-use payment transport settings remain intact.
func Wrap(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	if _, ok := clone.Transport.(*processTransport); ok {
		return &clone
	}
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &processTransport{base: base}
	return &clone
}
func ProcessRoundTripper() http.RoundTripper                            { return &processTransport{base: http.DefaultTransport} }
func Do(client *http.Client, req *http.Request) (*http.Response, error) { return Wrap(client).Do(req) }
func Get(client *http.Client, rawURL string) (*http.Response, error)    { return Wrap(client).Get(rawURL) }
func Post(client *http.Client, rawURL, contentType string, body io.Reader) (*http.Response, error) {
	return Wrap(client).Post(rawURL, contentType, body)
}

// BaseTransport removes only our transparent scheduling decorators so a caller
// can clone concrete TLS/proxy settings for a single-use paid connection.
// The paid request must subsequently be sent through Do or Wrap.
func BaseTransport(base http.RoundTripper) http.RoundTripper {
	for {
		switch t := base.(type) {
		case *processTransport:
			base = t.base
		case *Transport:
			base = t.base
		default:
			return base
		}
	}
}
