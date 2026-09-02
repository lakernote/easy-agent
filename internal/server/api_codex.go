package server

import "net/http"

func (server *Server) getCodex(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, server.detectCodex(request.Context()))
}
