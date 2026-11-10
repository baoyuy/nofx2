package mcp

import (
	"net/http"
	"testing"

	"nofx/internal/apischeduler"
)

func TestDefaultHTTPClientUsesSchedulerAndPreservesInjection(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HTTPClient.Transport == apischeduler.BaseTransport(cfg.HTTPClient.Transport) {
		t.Fatal("production default client is not scheduled")
	}
	injected := &http.Client{}
	c := NewClient(WithHTTPClient(injected)).(*Client)
	if c.HTTPClient != injected {
		t.Fatal("explicit HTTP client injection was replaced")
	}
}
