package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"nofx/events"
	"nofx/kernel"
	"nofx/store"
)

// Only public prediction events are shared. Associations are built afresh
// after checking ownership and runtime state on every request.
var eventFeed = events.New()

func (s *Server) registerEventRoutes(protected *gin.RouterGroup) {
	protected.GET("/events", s.handleEvents)
	protected.GET("/events/trade-tags", s.handleEventTradeTags)
	protected.GET("/events/cards", s.handleEventCards)
	protected.GET("/events/card-stats", s.handleEventCardStats)
	protected.POST("/events/labels", s.handleEventLabel)
	protected.GET("/events/briefing", s.handleEventBriefing)
	protected.POST("/events/briefing", s.handleEventBriefing)
}

type eventRuntime interface {
	IsRunning() bool
	GetCandidateCoins() ([]kernel.CandidateCoin, error)
}

func eventWatches(records []*store.Trader, runtime func(string) eventRuntime, strategyName func(string) string) ([]events.Watch, int, []string) {
	watches := []events.Watch{}
	warnings := []string{}
	running := 0
	for _, record := range records {
		live := runtime(record.ID)
		if live == nil || !live.IsRunning() {
			continue
		}
		running++
		coins, err := live.GetCandidateCoins()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Could not resolve strategy coins for %s", record.Name))
			continue
		}
		// A stop during resolution must not leave a stopped trader in the scope.
		if !live.IsRunning() {
			running--
			continue
		}
		ref := events.TraderRef{TraderID: record.ID, TraderName: record.Name, StrategyID: record.StrategyID, StrategyName: strategyName(record.StrategyID)}
		for _, coin := range coins {
			if events.Coin(coin.Symbol) == "" {
				warnings = append(warnings, fmt.Sprintf("Unsupported event symbol %s for %s", coin.Symbol, record.Name))
				continue
			}
			watches = append(watches, events.Watch{Symbol: coin.Symbol, Traders: []events.TraderRef{ref}})
		}
	}
	return events.Watches(watches), running, warnings
}

func eventHistoryOptions(c *gin.Context) (events.HistoryOptions, error) {
	opts := events.HistoryOptions{Window: time.Hour, ThresholdPP: 5, EndingWithin: 24 * time.Hour}
	if raw := c.Query("window_minutes"); raw != "" {
		switch raw {
		case "15":
			opts.Window = 15 * time.Minute
		case "60":
			opts.Window = time.Hour
		case "360":
			opts.Window = 6 * time.Hour
		default:
			return opts, fmt.Errorf("window_minutes must be 15, 60 or 360")
		}
	}
	if raw := c.Query("threshold_pp"); raw != "" {
		switch raw {
		case "1":
			opts.ThresholdPP = 1
		case "5":
			opts.ThresholdPP = 5
		case "10":
			opts.ThresholdPP = 10
		default:
			return opts, fmt.Errorf("threshold_pp must be 1, 5 or 10")
		}
	}
	if raw := c.Query("ending_hours"); raw != "" {
		switch raw {
		case "6":
			opts.EndingWithin = 6 * time.Hour
		case "24":
			opts.EndingWithin = 24 * time.Hour
		case "72":
			opts.EndingWithin = 72 * time.Hour
		default:
			return opts, fmt.Errorf("ending_hours must be 6, 24 or 72")
		}
	}
	return opts, nil
}

func (s *Server) handleEvents(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	opts, err := eventHistoryOptions(c)
	if err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	board, err := s.buildEventBoard(c.Request.Context(), userID, opts)
	if err != nil {
		SafeInternalError(c, "Load event scope", err)
		return
	}
	if id := c.Query("label_model_id"); id != "" {
		model, err := s.eventLabelModel(userID, id)
		if err != nil {
			SafeBadRequest(c, "Enabled model not found")
			return
		}
		if err := events.AttachLabels(s.store.EventLabels(), userID, model, &board); err != nil {
			board.Warnings = append(board.Warnings, "Event labels unavailable")
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, board)
}

// Both endpoints resolve ownership, runtime strategy scope and history on the
// server; client event content is never accepted.
func (s *Server) buildEventBoard(parent context.Context, userID string, opts events.HistoryOptions) (events.Board, error) {
	records, err := s.store.Trader().List(userID)
	if err != nil {
		return events.Board{}, err
	}
	strategyNames := map[string]string{}
	watches, running, warnings := eventWatches(records, func(id string) eventRuntime {
		live, err := s.traderManager.GetTrader(id)
		if err != nil {
			return nil
		}
		return live
	}, func(id string) string {
		if name, ok := strategyNames[id]; ok {
			return name
		}
		name := ""
		if id != "" {
			if strategy, err := s.store.Strategy().Get(userID, id); err == nil {
				name = strategy.Name
			}
		}
		strategyNames[id] = name
		return name
	})
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	board := eventFeed.Build(ctx, watches, running, warnings)
	// Running status may change while a provider request is in flight. Recheck
	// before publishing associations; never return stopped traders' cached scope.
	active := map[string]bool{}
	for _, record := range records {
		if live, err := s.traderManager.GetTrader(record.ID); err == nil && live.IsRunning() {
			active[record.ID] = true
		}
	}
	board.RunningTraders = len(active)
	filtered := make([]events.Watch, 0, len(board.Watches))
	for _, watch := range board.Watches {
		refs := []events.TraderRef{}
		for _, ref := range watch.Traders {
			if active[ref.TraderID] {
				refs = append(refs, ref)
			}
		}
		if len(refs) > 0 {
			watch.Traders = refs
			filtered = append(filtered, watch)
		}
	}
	board.Watches = filtered
	scoped := make([]events.Event, 0, len(board.Events))
	for _, event := range board.Events {
		refs := []events.TraderRef{}
		for _, ref := range event.Traders {
			if active[ref.TraderID] {
				refs = append(refs, ref)
			}
		}
		coins := []string{}
		for _, coin := range event.Coins {
			for _, watch := range filtered {
				if watch.Coin == coin {
					coins = append(coins, coin)
					break
				}
			}
		}
		if len(refs) > 0 && len(coins) > 0 {
			event.Traders = refs
			event.Coins = coins
			scoped = append(scoped, event)
		}
	}
	board.Events = scoped
	if err := events.AnnotateHistory(s.store.EventHistory(), userID, &board, opts); err != nil {
		board.Warnings = append(board.Warnings, "Event history unavailable; local changes cannot be verified")
	}
	return board, nil
}
