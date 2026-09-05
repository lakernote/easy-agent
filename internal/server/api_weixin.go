package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

type weixinStateView struct {
	Enabled  bool                `json:"enabled"`
	Accounts []weixinAccountView `json:"accounts"`
}

type weixinAccountView struct {
	ID               string             `json:"id"`
	Label            string             `json:"label"`
	UserID           string             `json:"userId"`
	Enabled          bool               `json:"enabled"`
	Connected        bool               `json:"connected"`
	CurrentSessionID string             `json:"currentSessionId,omitempty"`
	CurrentSession   *weixinSessionView `json:"currentSession,omitempty"`
	DeliveryStatus   string             `json:"deliveryStatus"`
	LastSeenAt       string             `json:"lastSeenAt,omitempty"`
	LastMessageAt    string             `json:"lastMessageAt,omitempty"`
	CreatedAt        time.Time          `json:"createdAt"`
}

type weixinSessionView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Runtime   string `json:"runtime"`
	Progress  string `json:"progress,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

type weixinLoginView struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	QRContent string    `json:"qrContent,omitempty"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (server *Server) getWeixin(response http.ResponseWriter, _ *http.Request) {
	settings, err := server.store.GetWeixinSettings()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	accounts, err := server.store.ListWeixinAccounts()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]weixinAccountView, 0, len(accounts))
	server.weixin.mu.Lock()
	connectedAccounts := make(map[string]bool, len(server.weixin.pollers))
	for id := range server.weixin.pollers {
		connectedAccounts[id] = true
	}
	deliveringAccounts := make(map[string]bool, len(server.weixin.delivery))
	for id := range server.weixin.delivery {
		deliveringAccounts[id] = true
	}
	server.weixin.mu.Unlock()
	for _, account := range accounts {
		var currentSession *weixinSessionView
		var sessionStatus string
		if account.CurrentSessionID != "" {
			if session, loadErr := server.store.LoadSessionWindow(account.CurrentSessionID, 1, 1); loadErr == nil {
				sessionStatus = session.Status
				currentSession = &weixinSessionView{ID: session.ID, Title: session.Title, Status: session.Status, Runtime: session.Runtime, Progress: server.tasks.progress(session.ID), UpdatedAt: optionalTimeJSON(session.UpdatedAt)}
			}
		}
		views = append(views, weixinAccountView{
			ID: account.ID, Label: account.Label, UserID: maskWeixinID(account.UserID), Enabled: account.Enabled,
			Connected: connectedAccounts[account.ID], CurrentSessionID: account.CurrentSessionID, CurrentSession: currentSession,
			DeliveryStatus: weixinDeliveryStatus(account, sessionStatus, deliveringAccounts[account.ID]),
			LastSeenAt:     optionalTimeJSON(account.LastSeenAt), LastMessageAt: optionalTimeJSON(account.LastMessageAt), CreatedAt: account.CreatedAt,
		})
	}
	writeJSON(response, http.StatusOK, weixinStateView{Enabled: settings.Enabled, Accounts: views})
}

func (server *Server) saveWeixinSettings(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(response, request, &input) {
		return
	}
	current, err := server.store.GetWeixinSettings()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	wasEnabled := current.Enabled
	current.Enabled = input.Enabled
	if input.Enabled && !wasEnabled {
		current.IgnoreBefore = now
	} else if !input.Enabled {
		if err := server.store.SuppressPendingWeixinDeliveries(now); err != nil {
			writeError(response, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if _, err := server.store.SaveWeixinSettings(current); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if input.Enabled {
		server.weixin.start()
	} else {
		server.weixin.stopAll()
	}
	server.getWeixin(response, request)
}

func (server *Server) startWeixinLogin(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Label string `json:"label"`
	}
	if !decodeJSON(response, request, &input) {
		return
	}
	login, err := server.weixin.beginLogin(request.Context(), input.Label)
	if err != nil {
		writeError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(response, http.StatusAccepted, loginView(login))
}

func (server *Server) getWeixinLogin(response http.ResponseWriter, request *http.Request) {
	login, ok := server.weixin.getLogin(request.PathValue("id"))
	if !ok {
		writeError(response, http.StatusNotFound, "扫码会话不存在或已清理")
		return
	}
	writeJSON(response, http.StatusOK, loginView(login))
}

func (server *Server) cancelWeixinLogin(response http.ResponseWriter, request *http.Request) {
	if !server.weixin.cancelLogin(request.PathValue("id")) {
		writeError(response, http.StatusNotFound, "扫码会话不存在或已清理")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) verifyWeixinLogin(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(response, request, &input) {
		return
	}
	if err := server.weixin.setVerifyCode(request.PathValue("id"), input.Code); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "扫码会话不存在或已清理")
		} else {
			writeError(response, http.StatusBadRequest, err.Error())
		}
		return
	}
	server.getWeixinLogin(response, request)
}

func (server *Server) updateWeixinAccount(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Label   string `json:"label"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSON(response, request, &input) {
		return
	}
	account, err := server.store.UpdateWeixinAccount(request.PathValue("id"), input.Label, input.Enabled, time.Now())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "微信绑定不存在")
		} else {
			writeError(response, http.StatusBadRequest, err.Error())
		}
		return
	}
	server.weixin.restartAccount(account.ID)
	server.getWeixin(response, request)
}

func (server *Server) deleteWeixinAccount(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	server.weixin.stopAccount(id)
	if err := server.store.DeleteWeixinAccount(id); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) retryWeixinDelivery(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	settings, err := server.store.GetWeixinSettings()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	account, err := server.store.GetWeixinAccount(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, "微信绑定不存在")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if !settings.Enabled || !account.Enabled {
		writeError(response, http.StatusConflict, "请先启用微信远程和这个成员")
		return
	}
	if account.PendingMessageID == 0 || account.PendingMessageID <= account.DeliveredMessageID {
		writeError(response, http.StatusConflict, "当前没有等待回传的结果")
		return
	}
	server.weixin.resumeDelivery(id)
	server.getWeixin(response, request)
}

func weixinDeliveryStatus(account store.WeixinAccount, sessionStatus string, delivering bool) string {
	if account.PendingMessageID == 0 {
		return "idle"
	}
	if account.PendingMessageID <= account.DeliveredMessageID {
		return "delivered"
	}
	if activeSessionStatus(sessionStatus) {
		return "processing"
	}
	if delivering {
		return "sending"
	}
	return "pending"
}

func loginView(login *weixinLogin) weixinLoginView {
	return weixinLoginView{ID: login.ID, Label: login.Label, QRContent: login.Content, Status: login.Status, Message: login.Message, CreatedAt: login.CreatedAt, UpdatedAt: login.UpdatedAt}
}

func maskWeixinID(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= 10 {
		return string(runes)
	}
	return string(runes[:5]) + "…" + string(runes[len(runes)-4:])
}

func optionalTimeJSON(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}
