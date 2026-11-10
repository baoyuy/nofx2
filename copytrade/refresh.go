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
	if !s.Follow.Private {
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
		retry = maxTime(now.Add(time.Minute), m.Client.Status().PausedUntil)
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
		if paused {
			cur.nextDue = retry
		}
		return
	}
	cur.Follow = f
	*taskError = ""
	cur.updateError()
	interval := coldInterval
	switch kind {
	case refreshPositions:
		// Empty-but-public positions must not turn into inferred holdings.
		cur.live = append([]LivePosition{}, live...)
		cur.UpdatedAt = now
		cur.PositionsUpdatedAt = now
		interval = positionInterval
	case refreshFills:
		cur.filling = pending
		cur.HistoryComplete = pending == nil
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
	if kind == refreshFills {
		if pending != nil {
			*due = now // yield after every page
			return
		}
		if cur.Hot {
			interval = hotInterval
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

func (s *Snapshot) updateError() {
	var parts []string
	for _, msg := range []string{s.PositionsError, s.FillsError, s.ProfileError} {
		if msg != "" {
			parts = append(parts, msg)
		}
	}
	s.Error = strings.Join(parts, "; ")
}
