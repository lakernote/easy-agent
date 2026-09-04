package server

import (
	"net/http"
	"strings"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type passwordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (server *Server) login(response http.ResponseWriter, request *http.Request) {
	var input loginRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	if strings.TrimSpace(input.Username) == "" || input.Password == "" {
		writeError(response, http.StatusBadRequest, "请输入用户名和密码")
		return
	}
	valid, err := server.store.Authenticate(input.Username, input.Password)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "登录校验失败")
		return
	}
	if !valid {
		writeError(response, http.StatusUnauthorized, "用户名或密码不正确")
		return
	}
	if !server.createAuthSession(response, request) {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"authenticated": true, "username": strings.TrimSpace(input.Username)})
}

func (server *Server) logout(response http.ResponseWriter, request *http.Request) {
	server.clearAuthSession(response, request)
	response.WriteHeader(http.StatusNoContent)
}

func (server *Server) me(response http.ResponseWriter, request *http.Request) {
	if !server.isAuthenticated(request) {
		writeJSON(response, http.StatusOK, map[string]any{"authenticated": false, "username": ""})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"authenticated": true, "username": "admin"})
}

func (server *Server) changePassword(response http.ResponseWriter, request *http.Request) {
	var input passwordRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	if len(input.NewPassword) < 8 {
		writeError(response, http.StatusBadRequest, "新密码至少需要 8 个字符")
		return
	}
	if err := server.store.ChangePassword("admin", input.CurrentPassword, input.NewPassword); err != nil {
		if err.Error() == "当前密码不正确" {
			writeError(response, http.StatusUnauthorized, err.Error())
			return
		}
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	server.authMu.Lock()
	server.sessions = make(map[string]authSession)
	server.authMu.Unlock()
	server.clearAuthSession(response, request)
	writeJSON(response, http.StatusOK, map[string]any{"authenticated": false, "message": "密码已修改，请重新登录"})
}
