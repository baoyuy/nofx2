// Package copytrade monitors Binance futures lead traders (copy trading).
// Binance has no public API for other traders' positions, so it reads the
// web endpoints behind binance.com's copy-trading pages: slowly, one request
// at a time, and pausing when Binance pushes back.
package copytrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/internal/apischeduler"
	"nofx/logger"
)

const (
	defaultBaseURL = "https://www.binance.com/bapi/futures"
	// Measured: 40 requests at 1.5 s spacing passed once, but a few hundred
	// requests over an hour got trade-history refused (code 11012005) and
	// then www.binance.com dropping connections from the IP. Stay well below.
	minSpacing  = 4 * time.Second
	jitter      = 400 * time.Millisecond
	backoffMin  = time.Minute
	backoffMax  = time.Hour
	userAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
	codeOK      = "000000"
	codeLimited = "11012005" // returned with HTTP 200 when calls come too fast
	codeClosed  = "11012028" // "The current portfolio is closed."
)

// ErrClosed: the lead trader closed the portfolio. Binance answers its
// endpoints with an error from then on, so asking again is pointless.
var ErrClosed = errors.New("the lead trader closed this portfolio")

// ErrPaused is returned while the client waits out a rate limit.
var ErrPaused = errors.New("copy-trade data paused after Binance rate limiting")

// Client sends every request through a gate so each IP stays under one
// request per minSpacing, whichever page or job asks. With one proxy (or
// none) that is one gate for the whole process. With several, each proxy is
// its own egress with its own gate, spacing and backoff: the request rate
// grows with their number, and Binance pushing back on one IP pauses only
// that one.
type Client struct {
	BaseURL string
	// Spacing is the minimum time between requests of one egress (tests set
	// it to 0).
	Spacing time.Duration

	mu sync.Mutex // guards the fields below; never held during a request
	// http is the first egress's client (tests replace its transport).
	http       *http.Client
	egresses   []*egress
	next       int // round-robin start
	proxyLabel string
	lastErr    string
	limitedAt  time.Time
	okAt       time.Time
	calls      []time.Time // request times in the last minute
	// formatErrs holds, per endpoint, the latest response that did not look
	// like Binance's usual data, until that endpoint answers well again.
	formatErrs map[string]formatErr
}

// egress is one way out: a proxy, or the server's own IP.
type egress struct {
	gate  sync.Mutex // serializes this egress's requests
	label string     // masked proxy URL; "" = server IP
	// name keys the shared scheduler's budget; "" shares the default one.
	name string
	http *http.Client

	// Guarded by Client.mu.
	last       time.Time
	pausedTill time.Time
	backoff    time.Duration
	busy       bool
}

// MaxProxies bounds the proxies of one monitor.
const MaxProxies = 8

type formatErr struct {
	msg string
	at  time.Time
}

// ErrFormat marks a response whose fields look renamed or missing: the
// endpoint answered but the data is not what this code reads.
var ErrFormat = errors.New("Binance changed the data format")

// checkFormat records whether endpoint's data looked right; it returns an
// ErrFormat error when it did not.
func (c *Client) checkFormat(endpoint string, ok bool, detail string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ok {
		delete(c.formatErrs, endpoint)
		return nil
	}
	if c.formatErrs == nil {
		c.formatErrs = map[string]formatErr{}
	}
	c.formatErrs[endpoint] = formatErr{msg: detail, at: time.Now()}
	logger.Warnf("copy-trade: %s: unexpected data: %s", endpoint, detail)
	return fmt.Errorf("%w: %s: %s", ErrFormat, endpoint, detail)
}

func NewClient() *Client {
	c := &Client{BaseURL: defaultBaseURL, Spacing: minSpacing}
	_ = c.SetProxy("")
	return c
}

// SetProxy routes requests through proxyURL (http, https, socks5 or
// socks5h; credentials allowed). Empty uses the process environment proxy,
// or a direct connection when there is none.
func (c *Client) SetProxy(proxyURL string) error {
	if strings.TrimSpace(proxyURL) == "" {
		return c.SetProxies(nil)
	}
	return c.SetProxies([]string{proxyURL})
}

// SplitProxies reads proxy URLs separated by commas, spaces or new lines.
func SplitProxies(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t' })
}

// SetProxies routes requests through the given proxies in turn, each at its
// own pace. None uses the process environment proxy or a direct connection.
// Repeats count once; at most MaxProxies.
func (c *Client) SetProxies(proxyURLs []string) error {
	var list []*egress
	seen := map[string]bool{}
	for _, raw := range proxyURLs {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		u, err := ParseProxy(raw)
		if err != nil {
			return err
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(u)
		list = append(list, &egress{label: MaskProxy(raw), name: raw,
			http: apischeduler.Wrap(&http.Client{Timeout: 20 * time.Second, Transport: transport})})
	}
	if len(list) > MaxProxies {
		return fmt.Errorf("at most %d proxies", MaxProxies)
	}
	switch len(list) {
	case 0:
		transport := http.DefaultTransport.(*http.Transport).Clone()
		list = []*egress{{http: apischeduler.Wrap(&http.Client{Timeout: 20 * time.Second, Transport: transport})}}
	case 1:
		// One way out is the whole process's budget, as it always was.
		list[0].name = ""
	}
	labels := make([]string, 0, len(list))
	for _, e := range list {
		if e.label != "" {
			labels = append(labels, e.label)
		}
	}
	c.mu.Lock()
	c.egresses, c.next = list, 0
	c.http = list[0].http
	c.proxyLabel = strings.Join(labels, ", ")
	c.mu.Unlock()
	return nil
}

// pick returns the egress to use: the idle one that can send soonest, taken
// in turn. It returns nil when every egress is waiting out a rate limit.
func (c *Client) pick(now time.Time) *egress {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.egresses) == 0 {
		return nil
	}
	// Tests swap the first client's transport after construction.
	c.egresses[0].http = c.http
	var best *egress
	var bestAt time.Time
	bestBusy := true
	for i := range c.egresses {
		e := c.egresses[(c.next+i)%len(c.egresses)]
		if now.Before(e.pausedTill) {
			continue
		}
		at := e.last.Add(c.Spacing)
		if best == nil || (bestBusy && !e.busy) || (bestBusy == e.busy && at.Before(bestAt)) {
			best, bestAt, bestBusy = e, at, e.busy
		}
	}
	if best != nil {
		c.next = (c.next + 1) % len(c.egresses)
		best.busy = true
	}
	return best
}

// ParseProxy validates a proxy URL such as http://user:pass@1.2.3.4:8080.
func ParseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("invalid proxy URL")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("proxy must start with http://, https://, socks5:// or socks5h://")
	}
	if u.Hostname() == "" || u.Port() == "" {
		return nil, errors.New("proxy needs a host and a port, e.g. http://1.2.3.4:8080")
	}
	if p, err := strconv.Atoi(u.Port()); err != nil || p < 1 || p > 65535 {
		return nil, errors.New("invalid proxy port")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("proxy URL must not have a path or query")
	}
	return u, nil
}

// MaskProxy hides the password: http://user:***@1.2.3.4:8080.
func MaskProxy(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	out := u.Scheme + "://"
	if u.User != nil {
		out += u.User.Username()
		if _, ok := u.User.Password(); ok {
			out += ":***"
		}
		out += "@"
	}
	return out + u.Host
}

// Status reports whether data is flowing, for the page's status bar.
type Status struct {
	Paused         bool      `json:"paused"`
	PausedUntil    time.Time `json:"paused_until,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastLimitedAt  time.Time `json:"last_limited_at,omitempty"`
	LastOKAt       time.Time `json:"last_ok_at,omitempty"`
	RequestsPerMin int       `json:"requests_per_min"`
	Proxy          string    `json:"proxy"` // masked; empty = server IP
	// FormatErrors lists endpoints whose latest answer looked malformed.
	FormatErrors []string `json:"format_errors,omitempty"`
	// Proxies is how many ways out are in use (1 with no proxy), and
	// ProxiesLimited how many of them wait out a rate limit.
	Proxies        int `json:"proxies"`
	ProxiesLimited int `json:"proxies_limited"`
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	proxy := c.proxyLabel
	now := time.Now()
	n := 0
	for _, t := range c.calls {
		if now.Sub(t) < time.Minute {
			n++
		}
	}
	var bad []string
	for ep, e := range c.formatErrs {
		bad = append(bad, ep+": "+e.msg)
	}
	sort.Strings(bad)
	// Paused only when no egress can send; until the first one can again.
	paused, until, limited := len(c.egresses) > 0, time.Time{}, 0
	for _, e := range c.egresses {
		if !now.Before(e.pausedTill) {
			paused = false
			continue
		}
		limited++
		if until.IsZero() || e.pausedTill.Before(until) {
			until = e.pausedTill
		}
	}
	if !paused {
		until = time.Time{}
	}
	return Status{Paused: paused, PausedUntil: until, LastError: c.lastErr,
		LastLimitedAt: c.limitedAt, LastOKAt: c.okAt, RequestsPerMin: n, Proxy: proxy, FormatErrors: bad,
		Proxies: len(c.egresses), ProxiesLimited: limited}
}

type envelope struct {
	Code    string          `json:"code"`
	Message *string         `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// do sends one request (POST when body is not nil) and decodes data into out.
func (c *Client) do(ctx context.Context, path string, body, out any) error {
	e := c.pick(time.Now())
	if e == nil {
		return ErrPaused
	}
	defer func() {
		c.mu.Lock()
		e.busy = false
		c.mu.Unlock()
	}()
	e.gate.Lock()
	defer e.gate.Unlock()
	c.mu.Lock()
	now := time.Now()
	paused := now.Before(e.pausedTill)
	wait := c.Spacing - now.Sub(e.last)
	if c.Spacing > 0 {
		wait += time.Duration(rand.Int63n(int64(jitter)))
	}
	c.mu.Unlock()
	if paused {
		return ErrPaused
	}
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	e.last = time.Now()
	c.calls = append(c.calls, e.last)
	for len(c.calls) > 0 && e.last.Sub(c.calls[0]) > time.Minute {
		c.calls = c.calls[1:]
	}
	hc := e.http
	c.mu.Unlock()

	err := c.send(apischeduler.WithEgress(ctx, e.name), hc, path, body, out)
	if err != nil && !errors.Is(err, ErrClosed) {
		endpoint := path
		if i := strings.IndexByte(endpoint, '?'); i >= 0 {
			endpoint = endpoint[:i]
		}
		logger.Warnf("copy-trade: %s: %v", endpoint, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var limited *limitedError
	switch {
	case errors.Is(err, ErrClosed):
		// A plain answer about one portfolio, not a failure of the data flow.
		e.backoff = 0
		c.lastErr = ""
		c.okAt = time.Now()
	case errors.As(err, &limited):
		e.backoff = min(max(e.backoff*2, backoffMin), backoffMax)
		e.pausedTill = time.Now().Add(e.backoff)
		c.limitedAt = time.Now()
		c.lastErr = limited.Error()
	case err != nil:
		c.lastErr = err.Error()
	default:
		e.backoff = 0
		c.lastErr = ""
		c.okAt = time.Now()
	}
	return err
}

type limitedError struct{ reason string }

func (e *limitedError) Error() string { return "Binance rate limited the request: " + e.reason }

func (c *Client) send(ctx context.Context, hc *http.Client, path string, body, out any) error {
	method, reader := http.MethodGet, io.Reader(nil)
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		method, reader = http.MethodPost, bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("clienttype", "web")
	resp, err := hc.Do(req)
	if err != nil {
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return errors.New("Binance did not answer in time")
		}
		// Binance blocks an IP by dropping the TLS handshake; back off as
		// for rate limiting (a real outage backs off the same way).
		msg := err.Error()
		if strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset") {
			return &limitedError{reason: "connection dropped"}
		}
		// A proxy refusing the tunnel answers the CONNECT itself; hammering
		// it does not help either.
		if strings.Contains(msg, "Forbidden") || strings.Contains(msg, "Too Many Requests") {
			return &limitedError{reason: "the proxy refused the connection"}
		}
		return fmt.Errorf("could not reach Binance: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusForbidden, 418:
		return &limitedError{reason: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	case http.StatusOK:
	default:
		return fmt.Errorf("Binance HTTP %d", resp.StatusCode)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// A challenge page is HTML, not JSON.
		return &limitedError{reason: "blocked by a verification page"}
	}
	switch env.Code {
	case codeOK:
	case codeLimited:
		return &limitedError{reason: "code " + codeLimited}
	case codeClosed:
		return ErrClosed
	default:
		msg := ""
		if env.Message != nil {
			msg = *env.Message
		}
		return fmt.Errorf("Binance code %s %s", env.Code, msg)
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("Binance changed the copy-trade data format: %w", err)
	}
	return nil
}

// Num decodes numbers Binance sends as JSON numbers, strings or null.
type Num float64

func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a number: %s", b)
	}
	*n = Num(v)
	return nil
}
