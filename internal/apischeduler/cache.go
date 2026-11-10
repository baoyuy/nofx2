package apischeduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
)

const maxSharedResponse = 8 << 20

type cachedResponse struct {
	status       int
	header       http.Header
	body         []byte
	proto        string
	major, minor int
}
type cacheEntry struct {
	response  cachedResponse
	expires   time.Time
	transport http.RoundTripper // Retain identity until eviction; a proxy transport's address must not be reused.
}
type flight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	response *cachedResponse
	err      error
	noShare  bool
}

// Only explicitly whitelisted anonymous public GETs qualify. Transport identity
// prevents cached results bypassing another client's proxy/TLS policy.
func cacheKey(r *http.Request, base http.RoundTripper, ttl time.Duration) string {
	if ttl <= 0 || r.Method != http.MethodGet || r.Body != nil || hasCredentials(r) || r.URL.Opaque != "" {
		return ""
	}
	control := strings.ToLower(r.Header.Get("Cache-Control"))
	if strings.Contains(control, "no-cache") || strings.Contains(control, "no-store") || r.Header.Get("Pragma") == "no-cache" || r.Header.Get("Range") != "" {
		return ""
	}
	for key := range r.URL.Query() {
		switch key {
		case "symbol", "pair", "interval", "limit", "startTime", "endTime", "period", "symbols":
		default:
			return ""
		}
	}
	for key := range r.Header {
		switch strings.ToLower(key) {
		case "accept", "accept-encoding", "user-agent", "content-type", "cache-control":
		default:
			return ""
		}
	}
	v := reflect.ValueOf(base)
	if !v.IsValid() || v.Kind() != reflect.Pointer {
		return ""
	}
	u := *r.URL
	u.RawQuery = u.Query().Encode()
	u.Fragment = ""
	return fmt.Sprintf("%T:%p|%s|%s|%s|%s|%s", base, base, u.String(), r.Header.Get("Accept"), r.Header.Get("Accept-Encoding"), r.Header.Get("User-Agent"), r.Header.Get("Content-Type"))
}
func (c cachedResponse) response(r *http.Request) *http.Response {
	return &http.Response{StatusCode: c.status, Status: fmt.Sprintf("%d %s", c.status, http.StatusText(c.status)), Header: c.header.Clone(), Body: io.NopCloser(bytes.NewReader(c.body)), ContentLength: int64(len(c.body)), Request: r, Proto: c.proto, ProtoMajor: c.major, ProtoMinor: c.minor}
}
func (s *Scheduler) countCache(provider string, coalesced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if coalesced {
		s.stat(provider).Coalesced++
	} else {
		s.stat(provider).CacheHits++
	}
}
func (s *Scheduler) cachedRoundTrip(r *http.Request, t *Transport, key string, p requestPolicy) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	if hit, ok := s.cache[key]; ok {
		if time.Now().Before(hit.expires) {
			s.cacheMu.Unlock()
			s.countCache(p.provider, false)
			return hit.response.response(r), nil
		}
		s.cacheBytes -= len(hit.response.body)
		delete(s.cache, key)
	}
	f := s.flights[key]
	owner := f == nil
	if owner {
		// One departing caller cannot cancel another consumer's read. Cancel the
		// independent, bounded read only when the last consumer leaves.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		f = &flight{done: make(chan struct{}), cancel: cancel, waiters: 1}
		s.flights[key] = f
		go s.fetchShared(r.Clone(ctx), t, key, p, f)
	} else {
		f.waiters++
	}
	s.cacheMu.Unlock()
	if !owner {
		s.countCache(p.provider, true)
	}
	select {
	case <-r.Context().Done():
		s.cacheMu.Lock()
		f.waiters--
		if f.waiters == 0 {
			if s.flights[key] == f {
				delete(s.flights, key)
			}
			f.cancel()
		}
		s.cacheMu.Unlock()
		return nil, r.Context().Err()
	case <-f.done:
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		if f.err != nil {
			return nil, f.err
		}
		if f.noShare && !owner {
			return nil, errors.New("upstream public response is private; not shared or retried")
		}
		return f.response.response(r), nil
	}
}
func (s *Scheduler) fetchShared(r *http.Request, t *Transport, key string, p requestPolicy, f *flight) {
	defer f.cancel()
	resp, err := t.roundTrip(r, p)
	var result *cachedResponse
	cacheable, noShare := false, false
	if err == nil {
		if resp == nil || resp.Body == nil {
			err = errors.New("API scheduler received an empty public response")
		} else {
			raw, e := io.ReadAll(io.LimitReader(resp.Body, maxSharedResponse+1))
			err = e
			if len(raw) > maxSharedResponse {
				err = errors.New("public API response exceeds scheduler shared-response limit")
			} else if e == nil {
				result = &cachedResponse{status: resp.StatusCode, header: resp.Header.Clone(), body: raw, proto: resp.Proto, major: resp.ProtoMajor, minor: resp.ProtoMinor}
				control := strings.ToLower(resp.Header.Get("Cache-Control"))
				noShare = resp.Header.Get("Set-Cookie") != "" || strings.Contains(control, "private")
				cacheable = resp.StatusCode == 200 && json.Valid(raw) && !noShare && resp.Header.Get("Vary") == "" && !strings.Contains(control, "no-store") && !strings.Contains(control, "no-cache") && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json")
			}
			_ = resp.Body.Close()
		}
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	f.response, f.err, f.noShare = result, err, noShare
	if s.flights[key] == f {
		delete(s.flights, key)
		if cacheable && result != nil && err == nil && f.waiters > 0 && r.Context().Err() == nil {
			for len(s.cache) >= s.cfg.CacheEntries || s.cacheBytes+len(result.body) > s.cfg.CacheBytes {
				oldest := ""
				at := time.Time{}
				for k, v := range s.cache {
					if oldest == "" || v.expires.Before(at) {
						oldest, at = k, v.expires
					}
				}
				if oldest == "" {
					break
				}
				s.cacheBytes -= len(s.cache[oldest].response.body)
				delete(s.cache, oldest)
			}
			if len(result.body) <= s.cfg.CacheBytes {
				s.cache[key] = cacheEntry{response: *result, expires: time.Now().Add(p.cacheTTL), transport: t.base}
				s.cacheBytes += len(result.body)
			}
		}
	}
	close(f.done)
}
