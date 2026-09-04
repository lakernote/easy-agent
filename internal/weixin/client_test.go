package weixin

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayUsesILinkAuthenticationAndMessageShape(t *testing.T) {
	requests := make(chan *http.Request, 2)
	bodies := make(chan []byte, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		requests <- request.Clone(context.Background())
		bodies <- data
		response.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/getupdates") {
			_, _ = response.Write([]byte(`{"ret":0,"msgs":[],"get_updates_buf":"next"}`))
			return
		}
		_, _ = response.Write([]byte(`{"ret":0}`))
	}))
	defer server.Close()
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test server only
	gateway := NewHTTPGateway(client)
	account := Account{ID: "bot", Token: "secret", BaseURL: server.URL}
	updates, err := gateway.GetUpdates(context.Background(), account, "cursor")
	if err != nil || updates.Buffer != "next" {
		t.Fatalf("getupdates 失败: %+v err=%v", updates, err)
	}
	request := <-requests
	body := <-bodies
	if request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("AuthorizationType") != "ilink_bot_token" || request.Header.Get("iLink-App-Id") != "bot" {
		t.Fatalf("iLink 认证头不完整: %+v", request.Header)
	}
	if _, err := base64.StdEncoding.DecodeString(request.Header.Get("X-WECHAT-UIN")); err != nil {
		t.Fatalf("X-WECHAT-UIN 不是 Base64: %v", err)
	}
	var updateBody map[string]any
	if err := json.Unmarshal(body, &updateBody); err != nil || updateBody["get_updates_buf"] != "cursor" {
		t.Fatalf("getupdates 请求体错误: %s err=%v", body, err)
	}
	if err := gateway.SendText(context.Background(), account, "user", "context", "完成"); err != nil {
		t.Fatal(err)
	}
	request = <-requests
	body = <-bodies
	if !strings.HasSuffix(request.URL.Path, "/sendmessage") || !strings.Contains(string(body), `"context_token":"context"`) || !strings.Contains(string(body), `"text":"完成"`) {
		t.Fatalf("sendmessage 请求错误: %s %s", request.URL.Path, body)
	}
}

func TestResolveURLRejectsNonHTTPS(t *testing.T) {
	if _, err := resolveURL("http://example.com", "ilink/bot/getupdates"); err == nil {
		t.Fatal("微信凭据不得发送到非 HTTPS 地址")
	}
}
