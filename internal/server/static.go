package server

import (
	"compress/gzip"
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
	if strings.HasPrefix(name, "assets/") {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		response.Header().Set("Cache-Control", "no-cache")
	}
	if acceptsGzip(request) && compressibleStaticAsset(name) && len(data) >= 1024 {
		response.Header().Set("Content-Encoding", "gzip")
		response.Header().Add("Vary", "Accept-Encoding")
		writer := gzip.NewWriter(response)
		_, _ = writer.Write(data)
		_ = writer.Close()
		return
	}
	_, _ = response.Write(data)
}

func acceptsGzip(request *http.Request) bool {
	return strings.Contains(strings.ToLower(request.Header.Get("Accept-Encoding")), "gzip")
}

func compressibleStaticAsset(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".css", ".html", ".js", ".json", ".map", ".svg", ".txt":
		return true
	default:
		return false
	}
}
