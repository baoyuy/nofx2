package binance

import (
	"encoding/json"
	"fmt"
	"nofx/trader/types"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// PaperExchangeType is the exchange_type of a Binance paper-trading account:
// live Binance USDT-M prices and precision, simulated fills, no API key.
const PaperExchangeType = "binance_paper"

// Simulation parameters (Binance USDT-M defaults for regular users).
const (
	paperTakerFeeRate   = 0.0005 // 0.05% per side
	paperSlippageRate   = 0.0002 // 0.02% adverse slippage on market fills
	paperMaintMarginPct = 0.005  // 0.5% maintenance margin for liquidation
	paperDefaultBalance = 1000.0
	paperMaxHistory     = 500
	paperPriceTTL       = 2 * time.Second
)

type paperPosition struct {
	Symbol     string    `json:"symbol"`
	Side       string    `json:"side"` // "long" | "short"
	Quantity   float64   `json:"quantity"`
	EntryPrice float64   `json:"entry_price"`
	Leverage   int       `json:"leverage"`
	StopLoss   float64   `json:"stop_loss,omitempty"`
	TakeProfit float64   `json:"take_profit,omitempty"`
	OpenedAt   time.Time `json:"opened_at"`
}

type paperOrder struct {
	OrderID    int64     `json:"order_id"`
	Symbol     string    `json:"symbol"`
	Side       string    `json:"side"` // BUY | SELL
	Type       string    `json:"type"`
	AvgPrice   float64   `json:"avg_price"`
	Quantity   float64   `json:"quantity"`
	Commission float64   `json:"commission"`
	Time       time.Time `json:"time"`
}

// PaperCloseEvent is a close the simulator triggered by itself (stop-loss,
// take-profit or liquidation), so the AutoTrader can record it in the DB.
type PaperCloseEvent struct {
	OrderID    string    `json:"order_id"`
	Symbol     string    `json:"symbol"`
	Side       string    `json:"side"` // "long" | "short"
	Quantity   float64   `json:"quantity"`
	EntryPrice float64   `json:"entry_price"`
	ExitPrice  float64   `json:"exit_price"`
	Fee        float64   `json:"fee"`
	Leverage   int       `json:"leverage"`
	Reason     string    `json:"reason"` // stop_loss | take_profit | liquidation
	Time       time.Time `json:"time"`
}

type paperState struct {
	InitialBalance float64                   `json:"initial_balance"`
	WalletBalance  float64                   `json:"wallet_balance"`
	Positions      map[string]*paperPosition `json:"positions"` // key: SYMBOL|side
	Leverage       map[string]int            `json:"leverage"`
	Orders         []paperOrder              `json:"orders"`
	Closed         []types.ClosedPnLRecord   `json:"closed"`
	PendingCloses  []PaperCloseEvent         `json:"pending_closes"`
	ExecOrders     []*paperExecOrder         `json:"exec_orders,omitempty"` // Frontier orders
	NextOrderID    int64                     `json:"next_order_id"`
	CreatedAt      time.Time                 `json:"created_at"`
}

// PaperTrader implements types.Trader against a local virtual account.
type PaperTrader struct {
	market    *FuturesTrader // keyless client: public price/exchangeInfo only
	statePath string

	mu    sync.Mutex
	state paperState

	priceMu    sync.Mutex
	priceCache map[string]paperPrice
	precision  map[string]int
}

type paperPrice struct {
	value float64
	at    time.Time
}

var (
	paperRegistryMu sync.Mutex
	paperRegistry   = map[string]*PaperTrader{}
	unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)
)

// GetPaperTrader returns the single shared simulator for an exchange account,
// so the running AutoTrader, manual closes and balance probes see one state.
func GetPaperTrader(accountID string) (*PaperTrader, error) {
	if accountID == "" {
		return nil, fmt.Errorf("paper trading account id is empty")
	}
	paperRegistryMu.Lock()
	defer paperRegistryMu.Unlock()
	if pt, ok := paperRegistry[accountID]; ok {
		return pt, nil
	}
	dir := paperStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create paper state dir: %w", err)
	}
	pt := &PaperTrader{
		market:     NewExecutionTrader("", "", ""),
		statePath:  filepath.Join(dir, unsafeFileChars.ReplaceAllString(accountID, "_")+".json"),
		priceCache: map[string]paperPrice{},
		precision:  map[string]int{},
	}
	if err := pt.load(); err != nil {
		return nil, err
	}
	// Like a real exchange, protective orders keep working while the bot is stopped.
	go pt.monitorLoop(10 * time.Second)
	paperRegistry[accountID] = pt
	return pt, nil
}

// paperStateDir keeps simulator files next to the SQLite database.
func paperStateDir() string {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join("data", "data.db")
	}
	return filepath.Join(filepath.Dir(dbPath), "paper")
}

func paperInitialBalance() float64 {
	if v, err := strconv.ParseFloat(os.Getenv("PAPER_INITIAL_BALANCE"), 64); err == nil && v > 0 {
		return v
	}
	return paperDefaultBalance
}

func (p *PaperTrader) load() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := os.ReadFile(p.statePath)
	if os.IsNotExist(err) {
		balance := paperInitialBalance()
		p.state = paperState{
			InitialBalance: balance,
			WalletBalance:  balance,
			NextOrderID:    time.Now().UnixMilli(),
			CreatedAt:      time.Now().UTC(),
		}
	} else if err != nil {
		return fmt.Errorf("read paper state: %w", err)
	} else if err := json.Unmarshal(data, &p.state); err != nil {
		return fmt.Errorf("parse paper state %s: %w", p.statePath, err)
	}
	if p.state.Positions == nil {
		p.state.Positions = map[string]*paperPosition{}
	}
	if p.state.Leverage == nil {
		p.state.Leverage = map[string]int{}
	}
	return p.saveLocked()
}

// saveLocked writes state atomically (temp file + rename). Caller holds p.mu.
func (p *PaperTrader) saveLocked() error {
	if len(p.state.Orders) > paperMaxHistory {
		p.state.Orders = p.state.Orders[len(p.state.Orders)-paperMaxHistory:]
	}
	if len(p.state.Closed) > paperMaxHistory {
		p.state.Closed = p.state.Closed[len(p.state.Closed)-paperMaxHistory:]
	}
	p.trimExecLocked()
	data, err := json.MarshalIndent(&p.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write paper state: %w", err)
	}
	return os.Rename(tmp, p.statePath)
}

func paperKey(symbol, side string) string { return symbol + "|" + side }

// price returns the live Binance price, cached briefly for read-only views.
func (p *PaperTrader) price(symbol string, fresh bool) (float64, error) {
	p.priceMu.Lock()
	cached, ok := p.priceCache[symbol]
	p.priceMu.Unlock()
	if ok && !fresh && time.Since(cached.at) < paperPriceTTL {
		return cached.value, nil
	}
	v, err := p.market.GetMarketPrice(symbol)
	if err != nil {
		return 0, err
	}
	p.priceMu.Lock()
	p.priceCache[symbol] = paperPrice{value: v, at: time.Now()}
	p.priceMu.Unlock()
	return v, nil
}

// marks fetches prices for every symbol with an open position or a resting
// Frontier order.
func (p *PaperTrader) marks() map[string]float64 {
	p.mu.Lock()
	symbols := map[string]bool{}
	for _, pos := range p.state.Positions {
		symbols[pos.Symbol] = true
	}
	for _, o := range p.state.ExecOrders {
		if o.open() {
			symbols[o.Order.Symbol] = true
		}
	}
	p.mu.Unlock()
	out := make(map[string]float64, len(symbols))
	for symbol := range symbols {
		if v, err := p.price(symbol, false); err == nil {
			out[symbol] = v
		}
	}
	return out
}

func (p *PaperTrader) GetMarketPrice(symbol string) (float64, error) {
	return p.price(symbol, true)
}

// FormatQuantity uses Binance's real LOT_SIZE step, cached per symbol.
func (p *PaperTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	p.priceMu.Lock()
	prec, ok := p.precision[symbol]
	p.priceMu.Unlock()
	if !ok {
		var err error
		if prec, err = p.market.GetSymbolPrecision(symbol); err != nil {
			prec = 3
		} else {
			p.priceMu.Lock()
			p.precision[symbol] = prec
			p.priceMu.Unlock()
		}
	}
	return strconv.FormatFloat(quantity, 'f', prec, 64), nil
}

func pnlFor(side string, qty, entry, exit float64) float64 {
	if side == "short" {
		return (entry - exit) * qty
	}
	return (exit - entry) * qty
}

// liquidationPrice approximates an isolated-margin liquidation level.
// Cross margin is treated per position, which is the conservative case.
func liquidationPrice(pos *paperPosition) float64 {
	if pos.Leverage <= 0 {
		return 0
	}
	move := 1/float64(pos.Leverage) - paperMaintMarginPct
	if move <= 0 {
		move = 0.0001
	}
	if pos.Side == "short" {
		return pos.EntryPrice * (1 + move)
	}
	return pos.EntryPrice * (1 - move)
}
