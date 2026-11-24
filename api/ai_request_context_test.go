package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"nofx/mcp"
)

type cancelledModelTransport struct{ started chan struct{} }

func (tr cancelledModelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	close(tr.started)
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestModelTestPropagatesTaskDeadlineToTransport(t *testing.T) {
	s, _ := coinPoolServer(t)
	if err := s.store.AIModel().Create("alice", "latency-model", "Latency test", "deepseek", true, "test-key", ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	tr := cancelledModelTransport{started: make(chan struct{})}
	start := time.Now()
	_, err := s.runRealAITestContext(ctx, "alice", "latency-model", "system", "user", mcp.WithHTTPClient(&http.Client{Transport: tr}), mcp.WithMaxRetries(1))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("task deadline did not cancel model call: %v", err)
	}
	select {
	case <-tr.started:
	default:
		t.Fatal("request never reached transport")
	}
}
