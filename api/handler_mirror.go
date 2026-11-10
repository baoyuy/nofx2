package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"nofx/logger"
	"nofx/mirror"
	"nofx/planning"
	"nofx/store"
	"nofx/trader"
	"nofx/trader/binance"
)

// StartMirror starts the engine that copies AI traders to other accounts.
// Call it after the traders are loaded.
func (s *Server) StartMirror(ctx context.Context) {
	e := mirror.New(s.store.Mirror(), mirror.Deps{
		Leader:   s.mirrorLeader,
		Follower: s.mirrorFollower,
		Busy:     s.mirrorBusy,
	})
	s.mirror, s.mirrorCtx = e, ctx
	trader.SetPositionHook(e.Wake)
	e.Start(ctx)
}

func (s *Server) mirrorLeader(traderID string) (mirror.Account, error) {
	at, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		return nil, err
	}
	return at.GetUnderlyingTrader(), nil
}

// mirrorFollower opens a Binance or Binance paper account.
func (s *Server) mirrorFollower(userID, exchangeID string) (mirror.Account, error) {
	ex, err := s.store.Exchange().GetByID(userID, exchangeID)
	if err != nil {
		return nil, err
	}
	if !ex.Enabled {
		return nil, fmt.Errorf("account %s is disabled", ex.AccountName)
	}
	switch ex.ExchangeType {
	case binance.PaperExchangeType:
		return binance.GetPaperTrader(ex.ID)
	case "binance":
		if ex.Testnet || ex.APIKey == "" || ex.SecretKey == "" {
			return nil, fmt.Errorf("account %s has no API key", ex.AccountName)
		}
		return binance.NewFuturesTrader(string(ex.APIKey), string(ex.SecretKey), userID), nil
	}
	return nil, fmt.Errorf("only Binance and Binance paper accounts can copy")
}

// accountIdentity tells accounts apart: two rows with one API key are one
// account. Paper accounts are their own row.
func accountIdentity(ex *store.Exchange) string {
	if ex.ExchangeType == binance.PaperExchangeType {
		return "paper:" + ex.ID
	}
	return planning.AccountIdentity(ex.ExchangeType, string(ex.APIKey), ex.HyperliquidWalletAddr)
}

// mirrorBusy tells why a link must not trade its account now: the account is
// the AI's own, a running AI trader uses it, or another running link does.
func (s *Server) mirrorBusy(link store.MirrorLink) string {
	ex, err := s.store.Exchange().GetByID(link.UserID, link.ExchangeID)
	if err != nil {
		return "follower account not found"
	}
	id := accountIdentity(ex)
	traders, err := s.store.Trader().List(link.UserID)
	if err != nil {
		return ""
	}
	for _, t := range traders {
		if t.ID != link.LeaderID && !t.IsRunning {
			continue
		}
		tex, err := s.store.Exchange().GetByID(link.UserID, t.ExchangeID)
		if err != nil || accountIdentity(tex) != id {
			continue
		}
		if t.ID == link.LeaderID {
			return "the follower account is the AI trader's own account"
		}
		return fmt.Sprintf("AI trader %s is running on this account", t.Name)
	}
	links, err := s.store.Mirror().Links(link.UserID)
	if err != nil {
		return ""
	}
	for _, o := range links {
		if o.ID == link.ID || !o.Running {
			continue
		}
		oex, err := s.store.Exchange().GetByID(link.UserID, o.ExchangeID)
		if err == nil && accountIdentity(oex) == id {
			return "another copy link is running on this account"
		}
	}
	return ""
}

func (s *Server) registerMirrorRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/mirror/accounts", "Binance and Binance paper accounts that can copy an AI trader", s.handleMirrorAccounts)
	s.route(protected, "GET", "/mirror/links", "Accounts copying AI traders, with their copied positions (?leader_id=)", s.handleMirrorLinks)
	s.route(protected, "POST", "/mirror/links", "Start copying an AI trader to another Binance account (body: leader_id, exchange_id, size_mode fixed|ratio, fixed_margin, ratio, leverage_mode follow|fixed, leverage, max_leverage, follow_protect, max_margin, max_loss, confirm_real)", s.handleMirrorCreate)
	s.route(protected, "PUT", "/mirror/links/:id", "Change a copy link's sizing, leverage, stops and caps; applies to the next orders", s.handleMirrorUpdate)
	s.route(protected, "POST", "/mirror/links/:id/start", "Start a stopped copy link (body: confirm_real for a real account)", s.handleMirrorStart)
	s.route(protected, "POST", "/mirror/links/:id/stop", "Stop a copy link (body: close_positions closes its copies)", s.handleMirrorStop)
	s.route(protected, "DELETE", "/mirror/links/:id", "Delete a stopped copy link; its positions on the account stay", s.handleMirrorDelete)
	s.route(protected, "GET", "/mirror/links/:id/events", "A copy link's orders, skips and errors, newest first", s.handleMirrorEvents)
}

func (s *Server) mirrorReady(c *gin.Context) bool {
	if s.mirror == nil {
		SafeError(c, http.StatusServiceUnavailable, "Copy trading is starting", nil)
		return false
	}
	return true
}

type mirrorSettings struct {
	SizeMode      string  `json:"size_mode"`
	FixedMargin   float64 `json:"fixed_margin"`
	Ratio         float64 `json:"ratio"`
	LeverageMode  string  `json:"leverage_mode"`
	Leverage      int     `json:"leverage"`
	MaxLeverage   int     `json:"max_leverage"`
	FollowProtect bool    `json:"follow_protect"`
	MaxMargin     float64 `json:"max_margin"`
	MaxLoss       float64 `json:"max_loss"`
}

func (m mirrorSettings) validate() error {
	switch m.SizeMode {
	case "fixed":
		if m.FixedMargin < 1 || m.FixedMargin > 1e6 {
			return errors.New("fixed margin must be 1 to 1,000,000 USDT")
		}
	case "ratio":
		if m.Ratio <= 0 || m.Ratio > 10 {
			return errors.New("ratio must be above 0 and at most 10")
		}
	default:
		return errors.New("size_mode must be fixed or ratio")
	}
	switch m.LeverageMode {
	case "fixed":
		if m.Leverage < 1 || m.Leverage > 125 {
			return errors.New("leverage must be 1 to 125")
		}
	case "follow":
		if m.MaxLeverage < 0 || m.MaxLeverage > 125 {
			return errors.New("max leverage must be 0 (no cap) to 125")
		}
	default:
		return errors.New("leverage_mode must be follow or fixed")
	}
	if m.MaxMargin < 0 || m.MaxLoss < 0 {
		return errors.New("caps must not be negative")
	}
	return nil
}

func (m mirrorSettings) apply(l *store.MirrorLink) {
	l.SizeMode, l.FixedMargin, l.Ratio = m.SizeMode, m.FixedMargin, m.Ratio
	l.LeverageMode, l.Leverage, l.MaxLeverage = m.LeverageMode, m.Leverage, m.MaxLeverage
	l.FollowProtect, l.MaxMargin, l.MaxLoss = m.FollowProtect, m.MaxMargin, m.MaxLoss
}

type mirrorAccount struct {
	ID          string   `json:"id"`
	AccountName string   `json:"account_name"`
	Paper       bool     `json:"paper"`
	UsedBy      []string `json:"used_by"`
}

func (s *Server) handleMirrorAccounts(c *gin.Context) {
	userID := c.GetString("user_id")
	exchanges, err := s.store.Exchange().List(userID)
	if err != nil {
		SafeInternalError(c, "List accounts", err)
		return
	}
	traders, err := s.store.Trader().List(userID)
	if err != nil {
		SafeInternalError(c, "List traders", err)
		return
	}
	out := []mirrorAccount{}
	for _, ex := range exchanges {
		if !ex.Enabled || (ex.ExchangeType != "binance" && ex.ExchangeType != binance.PaperExchangeType) {
			continue
		}
		a := mirrorAccount{ID: ex.ID, AccountName: ex.AccountName, Paper: ex.ExchangeType == binance.PaperExchangeType, UsedBy: []string{}}
		for _, t := range traders {
			if t.ExchangeID == ex.ID {
				a.UsedBy = append(a.UsedBy, t.Name)
			}
		}
		out = append(out, a)
	}
	c.JSON(http.StatusOK, gin.H{"accounts": out})
}

type mirrorLinkView struct {
	store.MirrorLink
	AccountName string                 `json:"account_name"`
	Paper       bool                   `json:"paper"`
	Active      bool                   `json:"active"`
	Status      *mirror.Status         `json:"status,omitempty"`
	Positions   []store.MirrorPosition `json:"positions"`
}

func (s *Server) mirrorView(l store.MirrorLink) mirrorLinkView {
	v := mirrorLinkView{MirrorLink: l, Active: s.mirror.Running(l.ID), Positions: []store.MirrorPosition{}}
	if ex, err := s.store.Exchange().GetByID(l.UserID, l.ExchangeID); err == nil {
		v.AccountName, v.Paper = ex.AccountName, ex.ExchangeType == binance.PaperExchangeType
	}
	if st, ok := s.mirror.Status(l.ID); ok {
		v.Status = &st
	}
	if ps, err := s.store.Mirror().Positions(l.ID); err == nil {
		v.Positions = ps
	}
	return v
}

func (s *Server) handleMirrorLinks(c *gin.Context) {
	if !s.mirrorReady(c) {
		return
	}
	links, err := s.store.Mirror().Links(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "List copy links", err)
		return
	}
	leader := c.Query("leader_id")
	out := []mirrorLinkView{}
	for _, l := range links {
		if leader == "" || l.LeaderID == leader {
			out = append(out, s.mirrorView(l))
		}
	}
	c.JSON(http.StatusOK, gin.H{"links": out})
}

// mirrorLinkFor reads the user's link named in the path.
func (s *Server) mirrorLinkFor(c *gin.Context) (store.MirrorLink, bool) {
	l, err := s.store.Mirror().Link(c.GetString("user_id"), c.Param("id"))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		SafeNotFound(c, "Copy link")
		return l, false
	}
	if err != nil {
		SafeInternalError(c, "Read copy link", err)
		return l, false
	}
	return l, true
}

// mirrorRealOK refuses a real account unless the request confirms it.
func (s *Server) mirrorRealOK(c *gin.Context, l store.MirrorLink, confirmed bool) bool {
	ex, err := s.store.Exchange().GetByID(l.UserID, l.ExchangeID)
	if err != nil {
		SafeBadRequest(c, "Follower account not found")
		return false
	}
	if ex.ExchangeType != binance.PaperExchangeType && !confirmed {
		SafeBadRequest(c, "This is a real account: confirm_real is required")
		return false
	}
	return true
}

func (s *Server) handleMirrorCreate(c *gin.Context) {
	if !s.mirrorReady(c) {
		return
	}
	var req struct {
		mirrorSettings
		LeaderID    string `json:"leader_id"`
		ExchangeID  string `json:"exchange_id"`
		ConfirmReal bool   `json:"confirm_real"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request")
		return
	}
	if err := req.validate(); err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	userID := c.GetString("user_id")
	t, err := s.store.Trader().GetByID(req.LeaderID)
	if err != nil || t.UserID != userID {
		SafeBadRequest(c, "AI trader not found")
		return
	}
	ex, err := s.store.Exchange().GetByID(userID, req.ExchangeID)
	if err != nil || (ex.ExchangeType != "binance" && ex.ExchangeType != binance.PaperExchangeType) {
		SafeBadRequest(c, "Pick a Binance or Binance paper account")
		return
	}
	l := store.MirrorLink{ID: uuid.NewString(), UserID: userID, LeaderID: req.LeaderID, ExchangeID: req.ExchangeID}
	req.apply(&l)
	if !s.mirrorRealOK(c, l, req.ConfirmReal) {
		return
	}
	if why := s.mirrorBusy(l); why != "" {
		SafeBadRequest(c, why)
		return
	}
	if err := s.store.Mirror().SaveLink(&l); err != nil {
		SafeInternalError(c, "Save copy link", err)
		return
	}
	if err := s.mirror.StartLink(s.mirrorCtx, l); err != nil {
		logger.Warnf("mirror: start %s: %v", l.ID, err)
		_ = s.store.Mirror().DeleteLink(userID, l.ID)
		SafeBadRequest(c, "Could not start: "+err.Error())
		return
	}
	l, _ = s.store.Mirror().Link(userID, l.ID)
	c.JSON(http.StatusOK, gin.H{"link": s.mirrorView(l)})
}

func (s *Server) handleMirrorUpdate(c *gin.Context) {
	if !s.mirrorReady(c) {
		return
	}
	l, ok := s.mirrorLinkFor(c)
	if !ok {
		return
	}
	var req mirrorSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request")
		return
	}
	if err := req.validate(); err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	req.apply(&l)
	if err := s.store.Mirror().SaveLink(&l); err != nil {
		SafeInternalError(c, "Save copy link", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"link": s.mirrorView(l)})
}

func (s *Server) handleMirrorStart(c *gin.Context) {
	if !s.mirrorReady(c) {
		return
	}
	l, ok := s.mirrorLinkFor(c)
	if !ok {
		return
	}
	var req struct {
		ConfirmReal bool `json:"confirm_real"`
	}
	_ = c.ShouldBindJSON(&req)
	if s.mirror.Running(l.ID) {
		c.JSON(http.StatusOK, gin.H{"link": s.mirrorView(l)})
		return
	}
	if !s.mirrorRealOK(c, l, req.ConfirmReal) {
		return
	}
	if err := s.mirror.StartLink(s.mirrorCtx, l); err != nil {
		SafeBadRequest(c, "Could not start: "+err.Error())
		return
	}
	l, _ = s.store.Mirror().Link(l.UserID, l.ID)
	c.JSON(http.StatusOK, gin.H{"link": s.mirrorView(l)})
}

func (s *Server) handleMirrorStop(c *gin.Context) {
	if !s.mirrorReady(c) {
		return
	}
	l, ok := s.mirrorLinkFor(c)
	if !ok {
		return
	}
	var req struct {
		ClosePositions bool `json:"close_positions"`
	}
	_ = c.ShouldBindJSON(&req)
	failed, err := s.mirror.StopLink(l.UserID, l.ID, req.ClosePositions)
	if err != nil {
		SafeInternalError(c, "Stop copy link", err)
		return
	}
	l, _ = s.store.Mirror().Link(l.UserID, l.ID)
	c.JSON(http.StatusOK, gin.H{"link": s.mirrorView(l), "close_failed": failed})
}

func (s *Server) handleMirrorDelete(c *gin.Context) {
	if !s.mirrorReady(c) {
		return
	}
	l, ok := s.mirrorLinkFor(c)
	if !ok {
		return
	}
	if l.Running || s.mirror.Running(l.ID) {
		SafeBadRequest(c, "Stop the copy link first")
		return
	}
	if err := s.store.Mirror().DeleteLink(l.UserID, l.ID); err != nil {
		SafeInternalError(c, "Delete copy link", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleMirrorEvents(c *gin.Context) {
	l, ok := s.mirrorLinkFor(c)
	if !ok {
		return
	}
	ev, err := s.store.Mirror().Events(l.ID, 100)
	if err != nil {
		SafeInternalError(c, "Read copy events", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": ev})
}
