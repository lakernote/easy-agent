package server

import (
	"compress/gzip"
	"io"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestStaticAssetsUseImmutableCacheAndGzip(t *testing.T) {
	payload := []byte("console.log('easyagent');\n")
	for len(payload) < 2048 {
		payload = append(payload, payload...)
	}
	server := &Server{assets: fstest.MapFS{"assets/index-AbCd1234.js": {Data: payload}}}
	request := httptest.NewRequest("GET", "/assets/index-AbCd1234.js", nil)
	request.Header.Set("Accept-Encoding", "gzip, deflate")
	response := httptest.NewRecorder()

	server.static(response, request)

	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(payload) {
		t.Fatal("gzip response did not round-trip")
	}
}

func TestStaticHistoryFallbackDoesNotUseImmutableCache(t *testing.T) {
	server := &Server{assets: fstest.MapFS{"index.html": {Data: []byte("<main>EasyAgent</main>")}}}
	response := httptest.NewRecorder()

	server.static(response, httptest.NewRequest("GET", "/settings/models", nil))

	if got := response.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Body.String(); got != "<main>EasyAgent</main>" {
		t.Fatalf("body = %q", got)
	}
}
