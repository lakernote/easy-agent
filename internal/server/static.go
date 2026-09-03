package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func (server *Server) static(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(server.assets, name)
	if err != nil {
		// 前端使用 history API 时，未知路径回退到入口页面。
		data, err = fs.ReadFile(server.assets, "index.html")
		name = "index.html"
	}
	if err != nil {
		http.Error(response, "frontend not built", http.StatusNotFound)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write(data)
}
