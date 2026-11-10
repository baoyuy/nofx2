package trader

import "sync/atomic"

var positionHook atomic.Pointer[func(traderID string)]

// SetPositionHook sets what runs after an AI trader's own order changed a
// position, so accounts copying it act at once instead of on their timer.
func SetPositionHook(f func(traderID string)) { positionHook.Store(&f) }

func notifyPositionChange(traderID string) {
	if f := positionHook.Load(); f != nil {
		go (*f)(traderID)
	}
}
