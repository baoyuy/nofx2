package marketboard

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func awaitRefresh(t *testing.T, c *Client, key string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		active := c.refreshing[key]
		c.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background refresh did not finish")
}

func TestStaleCacheReturnsImmediatelyAndRefreshesOnce(t *testing.T) {
	c := New()
	c.cache["board"] = cached{at: time.Now().Add(-cacheTTL - time.Second), v: "old"}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (any, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return "new", nil
	}
	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		v, err := c.cachedDo(ctx, "board", load)
		cancel()
		if err != nil || v != "old" {
			t.Fatalf("stale request blocked on upstream: %v %v", v, err)
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh not started")
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate scans: %d", calls.Load())
	}
	close(release)
	awaitRefresh(t, c, "board")
	v, err := c.cachedDo(context.Background(), "board", load)
	if err != nil || v != "new" || calls.Load() != 1 {
		t.Fatalf("refreshed value not reused: %v %v calls=%d", v, err, calls.Load())
	}
}

func TestStaleRefreshFailureBacksOffWithoutErasingData(t *testing.T) {
	c := New()
	at := time.Now().Add(-cacheTTL - time.Second)
	c.cache["board"] = cached{at: at, v: "old"}
	var calls atomic.Int32
	load := func(context.Context) (any, error) {
		calls.Add(1)
		return nil, errors.New("upstream unavailable")
	}
	_, _ = c.cachedDo(context.Background(), "board", load)
	awaitRefresh(t, c, "board")
	for i := 0; i < 20; i++ {
		v, err := c.cachedDo(context.Background(), "board", load)
		if err != nil || v != "old" {
			t.Fatalf("usable snapshot lost: %v %v", v, err)
		}
	}
	if calls.Load() != 1 || !c.cache["board"].at.Equal(at) {
		t.Fatal("refresh failure caused retry storm or falsely updated snapshot time")
	}
}

func TestExpiredCacheDoesNotServeOldDataAndCallerCanCancel(t *testing.T) {
	c := New()
	c.cache["board"] = cached{at: time.Now().Add(-staleTTL - time.Second), v: "too old"}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (any, error) {
		close(started)
		<-release
		defer close(done)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return "new", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := c.cachedDo(ctx, "board", load)
		returned <- err
	}()
	<-started
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expired cache must not pretend to be fresh: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller cancellation ignored")
	}
	close(release)
	<-done
	// Shared work survives caller cancellation and is reused by later readers.
	v, err := c.cachedDo(context.Background(), "board", load)
	if err != nil || v != "new" {
		t.Fatalf("shared refresh canceled with first caller: %v %v", v, err)
	}
}

func TestDirectionTimestampDoesNotResetWhenCachedItemsAreReranked(t *testing.T) {
	old := time.Now().Add(-time.Minute).UnixMilli()
	board := rankDirection([]DirectionItem{{Symbol: "BTC", fetchedAt: old}}, 1)
	if board.AsOf != old {
		t.Fatal("cached direction items advertised as freshly scanned")
	}
}

func TestHardExpiredAndColdFailuresRespectRetryDelay(t *testing.T) {
	for _, existing := range []bool{false, true} {
		c := New()
		if existing {
			c.cache["board"] = cached{at: time.Now().Add(-staleTTL - time.Second), v: "expired"}
		}
		var calls atomic.Int32
		load := func(context.Context) (any, error) {
			calls.Add(1)
			return nil, errors.New("upstream unavailable")
		}
		for i := 0; i < 20; i++ {
			v, err := c.cachedDo(context.Background(), "board", load)
			if err == nil || v != nil {
				t.Fatalf("must return failure, not expired data: %v %v", v, err)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("failure retry storm: %d", calls.Load())
		}
	}
}
