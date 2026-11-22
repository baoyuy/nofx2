package kernel

// StableRank is one coin from the calibrated 4h "stable" ranking, used by the
// program-stable (rule-based, no-AI) strategy. Price is the current last price.
type StableRank struct {
	Symbol    string
	Direction string // long | short
	Stable    float64
	AI        float64
	Price     float64
}

// StableRankingResolver returns the current scored+directional stable ranking.
// The API package sets it at startup (it has the copy-trade + score data).
var StableRankingResolver func(userID string) []StableRank
