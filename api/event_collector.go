package api

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"nofx/events"
	"nofx/logger"
	"nofx/market"
)

// eventLocalRetention is how long local event data is kept before the collector
// prunes it. Kept together with the collector so storage stays bounded.
const eventLocalRetention = 4 * 24 * time.Hour

// eventCollectorInterval is the background collection cadence. Override with
// EVENT_COLLECT_INTERVAL_MINUTES (1-120); default 10 minutes.
func eventCollectorInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("EVENT_COLLECT_INTERVAL_MINUTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 120 {
			return time.Duration(n) * time.Minute
		}
	}
	return 10 * time.Minute
}

// StartEventCollector runs a server-side, page-independent event collection loop
// so history and change detection keep accumulating even when nobody has the
// Events page open. It only fetches public data and persists snapshots; it never
// calls a model and never trades. Rows older than the retention window are
// pruned on every pass, so local storage cannot grow without bound.
func (s *Server) StartEventCollector(ctx context.Context) {
	interval := eventCollectorInterval()
	go func() {
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
			return
		}
		s.runEventCollection(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runEventCollection(ctx)
			}
		}
	}()
	logger.Infof("📥 Event collector started (every %s, local retention %s)", interval, eventLocalRetention)
}

func (s *Server) runEventCollection(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	traders, err := s.store.Trader().ListAll()
	if err != nil {
		logger.Warnf("event collector: list traders: %v", err)
		return
	}
	users := map[string]bool{}
	for _, t := range traders {
		if t.IsRunning && strings.TrimSpace(t.UserID) != "" {
			users[t.UserID] = true
		}
	}
	opts := events.HistoryOptions{Window: time.Hour, ThresholdPP: 5, EndingWithin: 24 * time.Hour}
	for userID := range users {
		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		board, err := s.buildEventBoard(cctx, userID, opts)
		cancel()
		if err != nil {
			logger.Warnf("event collector: user %s: %v", userID, err)
			continue
		}
		logger.Infof("📥 Event collector: user %s collected %d events, %d warnings", userID, len(board.Events), len(board.Warnings))
		if cards, cerr := s.store.EventCards().ListByUser(userID); cerr != nil {
			logger.Warnf("event collector: cards check failed: %v", cerr)
		} else {
			logger.Infof("event collector: event_cards for %s = %d", userID, len(cards))
		}
	}
	cutoff := time.Now().UTC().Add(-eventLocalRetention)
	if err := s.store.EventHistory().PruneOlderThan(cutoff); err != nil {
		logger.Warnf("event collector: prune history: %v", err)
	}
	if err := s.store.EventLabels().DeleteOlderThan(cutoff); err != nil {
		logger.Warnf("event collector: prune labels: %v", err)
	}
	if err := s.store.EventTradeTags().DeleteOlderThan(cutoff); err != nil {
		logger.Warnf("event collector: prune trade tags: %v", err)
	}
	if err := s.store.EventCards().DeleteOlderThan(cutoff); err != nil {
		logger.Warnf("event collector: prune event cards: %v", err)
	}
	if err := s.store.EventCards().DeleteExpired(time.Now().UTC(), 48*time.Hour, 6*time.Hour); err != nil {
		logger.Warnf("event collector: expire event cards: %v", err)
	}
	s.evaluateEventCards()
}

func envFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}

// evaluateEventCards resolves pending directional cards whose horizon has
// elapsed against the current price, recording hit/miss for the hit-rate loop.
func (s *Server) evaluateEventCards() {
	now := time.Now().UTC()
	horizon := time.Duration(envFloat("EVENT_CARD_EVAL_HOURS", 4.0) * float64(time.Hour))
	threshold := envFloat("EVENT_CARD_EVAL_THRESHOLD_PCT", 0.5)
	pending, err := s.store.EventCards().ListPendingForEval(now, horizon)
	if err != nil {
		logger.Warnf("event collector: list pending cards: %v", err)
		return
	}
	evaluated := 0
	for _, c := range pending {
		if c.Symbol == "" || c.EntryPrice <= 0 {
			continue
		}
		md, err := market.Get(c.Symbol)
		if err != nil || md == nil || md.CurrentPrice <= 0 {
			continue
		}
		pct := (md.CurrentPrice - c.EntryPrice) / c.EntryPrice * 100
		status := "neutral"
		switch c.Direction {
		case "bullish":
			if pct > threshold {
				status = "hit"
			} else if pct < -threshold {
				status = "miss"
			}
		case "bearish":
			if pct < -threshold {
				status = "hit"
			} else if pct > threshold {
				status = "miss"
			}
		}
		if err := s.store.EventCards().MarkEvaluated(c.UserID, c.TraderID, c.Coin, c.CardID, status, pct, now); err != nil {
			logger.Warnf("event collector: mark evaluated: %v", err)
			continue
		}
		evaluated++
	}
	if evaluated > 0 {
		logger.Infof("📊 Event card evaluation: %d cards resolved", evaluated)
	}
}
