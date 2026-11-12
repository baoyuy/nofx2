package copytrade

import (
	"context"
	"errors"
	"strings"
	"time"
)

type refreshTask uint8

const (
	refreshPositions refreshTask = iota
	refreshFills
	refreshProfile
)

// Choose the oldest deadline, positions first on ties. Each history page gets
// a new deadline so already-due live work and other traders get a turn.
func (s *Snapshot) task() (refreshTask, time.Time) {
	kind, due := refreshProfile, s.profileDue
	if !NoData(s.Follow) {
		kind, due = refreshFills, s.fillsDue
		if s.Follow.PositionShow && !s.positionDue.After(due) {
			kind, due = refreshPositions, s.positionDue
		}
		if s.profileDue.Before(due) {
			kind, due = refreshProfile, s.profileDue
		}
	}
	return kind, maxTime(due, s.nextDue)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Perform one live request, one history page, or a low-rate profile update.
// Never hold m.mu while waiting on the network.
func (m *Monitor) refresh(ctx context.Context, id string) {
	m.mu.RLock()
	snap, ok := m.snaps[id]
	if !ok {
		m.mu.RUnlock()
		return
	}
	kind, _ := snap.task()
	f, pending := snap.Follow, snap.filling
	m.mu.RUnlock()
	started := time.Now()
	var err error
	var live []LivePosition
	switch kind {
	case refreshPositions:
		live, err = m.Client.Positions(ctx, id)
	case refreshFills:
		pending, err = m.syncFillsPage(ctx, &f, pending)
	case refreshProfile:
		err = m.syncProfile(ctx, &f)
	}
	now := time.Now()
	retry := now.Add(errorInterval)
	var limited *limitedError
	paused := errors.Is(err, ErrPaused) || errors.As(err, &limited)
	if paused {
		// Wait for the pause; with another proxy still free, only a moment.
		if st := m.Client.Status(); st.Paused {
			retry = maxTime(now.Add(time.Minute), st.PausedUntil)
		} else {
			retry = now.Add(10 * time.Second)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.snaps[id]
	if !ok || cur != snap {
		return // unfollowed / followed again meanwhile
	}
	var due *time.Time
	var taskError *string
	switch kind {
	case refreshPositions:
		due, taskError = &cur.positionDue, &cur.PositionsError
	case refreshFills:
		due, taskError = &cur.fillsDue, &cur.FillsError
	case refreshProfile:
		due, taskError = &cur.profileDue, &cur.ProfileError
	}
	if err != nil {
		*taskError = err.Error()
		cur.updateError()
		*due = retry
		if paused && m.Client.Status().Paused {
			cur.nextDue = retry
		}
		return
	}
	cur.Follow = f
	*taskError = ""
	cur.updateError()
	interval := coldInterval
	positions := false
	switch kind {
	case refreshPositions:
		// Empty-but-public positions must not turn into inferred holdings.
		cur.live = append([]LivePosition{}, live...)
		cur.UpdatedAt = now
		cur.PositionsUpdatedAt = now
		interval = positionInterval
		positions = true
	case refreshFills:
		cur.filling = pending
		cur.HistoryComplete = pending == nil
		if pending == nil {
			cur.pendingSince = time.Time{}
		} else if cur.pendingSince.IsZero() {
			cur.pendingSince = now
		}
		if pending == nil {
			cur.FillsUpdatedAt = time.UnixMilli(f.FillsSyncedAt)
			if !f.PositionShow {
				cur.PositionsUpdatedAt = cur.FillsUpdatedAt
			}
		}
		if pending == nil && !f.PositionShow {
			cur.UpdatedAt = now
		}
	case refreshProfile:
		if f.ProfileAt > 0 {
			cur.ProfileUpdatedAt = time.UnixMilli(f.ProfileAt)
		}
		interval = profileInterval
	}
	m.rebuild(cur, cur.live)
	dormant := cur.dormant(now)
	if kind == refreshFills {
		if pending != nil {
			*due = now // yield after every page
			return
		}
		switch {
		case cur.Hot:
			interval = hotInterval
		case dormant:
			interval = dormantInterval
		}
	}
	if positions && len(cur.Positions) == 0 {
		// Nothing held: a new position may show a little later.
		interval = idlePositionInterval
		if dormant {
			interval = dormantPositionInterval
		}
	}
	if kind == refreshProfile && f.ProfileAt > 0 {
		*due = maxTime(time.UnixMilli(f.ProfileAt).Add(profileInterval), now)
		return
	}
	// Network/queue time should not get added to every polling interval.
	// Scheduling no earlier than now avoids a catch-up burst.
	*due = maxTime(started.Add(interval), now)
}

// dormant tells a trader who holds nothing and has not traded for
// dormantAfter: polled at dormantInterval.
func (s *Snapshot) dormant(now time.Time) bool {
	return len(s.Positions) == 0 && s.Follow.LastTradeTime > 0 &&
		now.Sub(time.UnixMilli(s.Follow.LastTradeTime)) > dormantAfter
}

func (s *Snapshot) updateError() {
	var parts []string
	for _, msg := range []string{s.PositionsError, s.FillsError, s.ProfileError} {
		if msg != "" {
			parts = append(parts, msg)
		}
	}
	s.Error = strings.Join(parts, "; ")
}
