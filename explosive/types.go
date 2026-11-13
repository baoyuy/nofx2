package explosive

import "time"

// Kline is one closed candle. Volume is base-asset volume.
type Kline struct {
	Time   int64
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// State is where a coin sits in its move. A high score in the wrong state is
// a trap: "overheated" coins are the ones that already ran.
type State string

const (
	StateStarting     State = "starting"     // just broke out, volume arriving
	StateAccelerating State = "accelerating" // price and participation both expanding
	StateOverheated   State = "overheated"   // funding or distance says crowded
	StateFading       State = "fading"       // open interest and volume falling
	StateFalling      State = "falling"      // the last few 5m bars are selling off
)

// Row is one coin's reading.
type Row struct {
	Symbol string  `json:"symbol"`
	Price  float64 `json:"price"`
	// Change24h and Change5m are percent; Change5m is the last closed 5m bar.
	Change24h float64 `json:"change_24h"`
	Change5m  float64 `json:"change_5m"`
	Change15m float64 `json:"change_15m"`
	// Volume24h is USDT.
	Volume24h float64 `json:"volume_24h"`
	// VolRatio is the last 5m volume over the median of the 20 before it.
	VolRatio float64 `json:"vol_ratio"`
	// OIChange1h is the open-interest change over the last hour, as a
	// fraction (0.03 = +3%). OIKnown is false when the request failed: the
	// open-interest part is then neutral rather than zero.
	OIChange1h float64 `json:"oi_change_1h"`
	OIKnown    bool    `json:"oi_known"`
	// Funding is the current funding rate per 8h, as a fraction.
	Funding float64 `json:"funding"`
	// ATRPct is the 5m average true range as percent of price.
	ATRPct float64 `json:"atr_pct"`
	// DistanceHigh is how far price sits below the last 24h high, percent.
	DistanceHigh float64 `json:"distance_high"`
	// EMA20/60/120 come from 1h closes.
	EMA20  float64 `json:"ema20"`
	EMA60  float64 `json:"ema60"`
	EMA120 float64 `json:"ema120"`
	// Strength is the momentum reading, 0-100, before any risk discount. The
	// board is ordered by it: a coin in the middle of a move is shown even
	// when its risk flags are up, instead of being buried.
	Strength float64 `json:"strength"`
	// Risk is the product of the risk discounts, 1 = none.
	Risk float64 `json:"risk"`
	// Score is Strength × Risk, the health-adjusted reading.
	Score float64 `json:"score"`
	// Parts of Strength, all 0-1.
	Speed       float64  `json:"speed"`
	Volume      float64  `json:"volume"`
	OpenInt     float64  `json:"open_interest"`
	Structure   float64  `json:"structure"`
	RelStrength float64  `json:"rel_strength"`
	State       State    `json:"state"`
	Tags        []string `json:"tags"`
}

// Board is one screening pass.
type Board struct {
	Rows []Row `json:"rows"`
	// Scanned counts tickers considered, Candidates those that passed the
	// liquidity and freshness gates.
	Scanned    int       `json:"scanned"`
	Candidates int       `json:"candidates"`
	FetchedAt  time.Time `json:"fetched_at"`
	// AgeSeconds is the age of the expensive data (candles, open interest).
	// Prices and 24h changes come from the live feed and are fresher.
	AgeSeconds int  `json:"age_seconds"`
	Stale      bool `json:"stale"`
	// Refreshing is true while a background pass is running.
	Refreshing bool `json:"refreshing"`
	// Reason explains an empty board caused by missing data.
	Reason string `json:"reason,omitempty"`
}

// Input is everything the score needs about one coin.
type Input struct {
	Symbol     string
	Price      float64
	Change24h  float64
	Volume24h  float64
	Bars5m     []Kline // closed, oldest first
	Bars1h     []Kline // closed, oldest first
	OIChange1h float64
	OIKnown    bool
	Funding    float64
	// BTCChange24h is the market's own 24h change, so a coin can be scored on
	// what it did beyond the market.
	BTCChange24h float64
}
