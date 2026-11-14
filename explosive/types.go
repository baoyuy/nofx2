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
	// TakerBuy is the base volume bought by takers (aggressors).
	TakerBuy float64
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
	// Beijing calendar-day view: the gain since 00:00, how many hours the move
	// has run, and the volume slope over the last three hours.
	DayGain     float64 `json:"day_gain"`
	ElapsedH    float64 `json:"elapsed_hours"`
	VolumeSlope float64 `json:"volume_slope"`
	// PredictedRemaining is the model's estimate of the rest of the day's
	// move, in percent.
	PredictedRemaining float64 `json:"predicted_remaining"`
	// PredictedFinal is where the coin is expected to end the day, as a total
	// gain from today's open, percent. This is what the ambush board ranks on:
	// a coin at +10% heading for +90% outranks one at +60% heading for +65%.
	PredictedFinal float64 `json:"predicted_final"`
	// Ran is today's gain already realised; Ambush marks a coin still early
	// enough to enter (the board's first section).
	Ran    bool `json:"ran"`
	Ambush bool `json:"ambush"`
	// VolSpike1h is the last closed hour's volume against its ~6-day median.
	// RadarScore combines it with volatility; Radar flags an ignition.
	VolSpike1h float64 `json:"vol_spike_1h"`
	RadarScore float64 `json:"radar_score"`
	Radar      bool    `json:"radar"`
	// Tradability is a coarse liquidity label: good | fair | thin.
	Tradability string `json:"tradability"`
	// Multi-timeframe changes, percent, from 1h closes.
	Change1h float64 `json:"change_1h"`
	Change4h float64 `json:"change_4h"`
	// EMASlope is the change of EMA20 over the last three 1h bars, percent.
	EMASlope float64 `json:"ema_slope"`
	// TakerBuyRatio is aggressive buying over volume in the last hour, 0-1.
	TakerBuyRatio float64 `json:"taker_buy_ratio"`
	// RiseEfficiency is net move over the path travelled in the last hour.
	RiseEfficiency float64 `json:"rise_efficiency"`
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
	// Rows is the ambush list, ordered by PredictedFinal.
	Rows []Row `json:"rows"`
	// Blasted are coins that already ran today: shown, but not ranked as
	// ambushes.
	Blasted []Row `json:"blasted"`
	// Radar lists ignition candidates: volume spike + volatility + OI rising,
	// still early in the day. Measured hit rate is low (about 8% reach +50%),
	// so it is a watch list, not a prediction.
	Radar []Row `json:"radar"`
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
	// Day fields, Beijing calendar day. HasDay is false when the day's open
	// could not be found (short history), and the day model is then skipped.
	HasDay      bool
	DayGain     float64
	ElapsedH    float64
	VolumeSlope float64
	DayHighDist float64
}
