package lighter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScheduledLighterSDKReads(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("account_index") != "123" || r.URL.Query().Get("api_key_index") != "2" {
			t.Error("SDK query changed")
		}
		switch r.URL.Path {
		case "/api/v1/nextNonce":
			w.Write([]byte(`{"code":200,"nonce":17}`))
		case "/api/v1/apikeys":
			w.Write([]byte(`{"code":200,"api_keys":[{"public_key":"test-public-key"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := newScheduledLighterHTTPClient(context.Background(), s.URL)
	if n, e := c.GetNextNonce(123, 2); e != nil || n != 17 {
		t.Fatalf("nonce: %d %v", n, e)
	}
	if k, e := c.GetApiKey(123, 2); e != nil || k != "test-public-key" {
		t.Fatalf("key: %q %v", k, e)
	}
}
func TestScheduledLighterSDKRejectsMalformedAndFailedReads(t *testing.T) {
	for _, body := range []string{`{"code":400,"nonce":1}`, `{"code":200}`, `{"code":200,"nonce":-1}`, `invalid`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer s.Close()
			c := newScheduledLighterHTTPClient(context.Background(), s.URL)
			if _, e := c.GetNextNonce(123, 2); e == nil {
				t.Fatal("accepted invalid nonce response")
			}
			if _, e := c.GetApiKey(123, 2); e == nil {
				t.Fatal("accepted invalid key response")
			}
		})
	}
}
