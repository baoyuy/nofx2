package copytrade

// classifyEvents labels fills by what they imply for a directional reading.
// Adds to a losing position are risk information, not stronger conviction.
func classifyEvents(events []Event, positions []Position) []Event {
	out := append([]Event(nil), events...)
	atLoss := map[string]bool{}
	for _, p := range positions {
		atLoss[p.Symbol+"|"+p.Side] = p.atLoss()
	}
	for i := range out {
		e := &out[i]
		switch e.Action {
		case "open":
			e.SignalType = "directional_open"
		case "add":
			if atLoss[e.Symbol+"|"+e.Side] {
				e.SignalType = "adverse_add"
			} else {
				e.SignalType = "trend_add"
			}
		case "reduce":
			e.SignalType = "reduce"
		case "close":
			e.SignalType = "exit"
		}
	}
	return out
}
