package copytrade

import "time"

// DataQuality describes independently observed portions of a trader snapshot.
// Public positions may be shown immediately, but a complete AI view also needs
// a completed trade catch-up and a recent profile for credibility scoring.
type DataQuality struct {
	PositionsFresh  bool `json:"positions_fresh"`
	FillsFresh      bool `json:"fills_fresh"`
	ProfileFresh    bool `json:"profile_fresh"`
	HistoryComplete bool `json:"history_complete"`
}

func freshAt(at time.Time, maxAge time.Duration, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= maxAge
}

func (s Snapshot) Quality(maxAge time.Duration, now time.Time) DataQuality {
	if NoData(s.Follow) {
		return DataQuality{}
	}
	// A dormant trader is asked less often on purpose: being on schedule is
	// not being stale, or every quiet trader would make the view incomplete.
	live := maxAge
	if s.dormant(now) {
		live += dormantInterval
	}
	return DataQuality{
		PositionsFresh:  s.PositionsError == "" && freshAt(s.PositionsUpdatedAt, live, now),
		FillsFresh:      s.FillsError == "" && s.HistoryComplete && freshAt(s.FillsUpdatedAt, live, now),
		ProfileFresh:    s.ProfileError == "" && freshAt(s.ProfileUpdatedAt, profileInterval+maxAge, now),
		HistoryComplete: s.HistoryComplete,
	}
}

func (q DataQuality) Complete() bool {
	return q.PositionsFresh && q.FillsFresh && q.ProfileFresh && q.HistoryComplete
}
