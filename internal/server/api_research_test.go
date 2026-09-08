package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lakernote/easy-agent/internal/store"
)

func TestResearchSettingsAPIPersistsAndRedactsSecrets(t *testing.T) {
	for _, name := range []string{
		"EASYAGENT_TAVILY_API_KEY", "TAVILY_API_KEY", "EASYAGENT_SEARXNG_URL",
		"EASYAGENT_BRAVE_SEARCH_API_KEY", "BRAVE_SEARCH_API_KEY", "EASYAGENT_READER_URL",
		"EASYAGENT_READER_API_KEY", "GITHUB_TOKEN", "GH_TOKEN",
	} {
		t.Setenv(name, "")
	}
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())

	const secret = "secret-that-must-never-reach-the-browser"
	request := httptest.NewRequest(http.MethodPut, "/api/v1/research/settings", strings.NewReader(`{
		"providers":[
			{"id":"tavily","secret":"`+secret+`"},
			{"id":"searxng","endpoint":"https://search.example.com"}
		]
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("保存 Research 配置失败: HTTP=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatal("保存响应泄露了 Research 密钥")
	}
	var view researchSettingsView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var tavily, searxng researchProviderView
	for _, provider := range view.Providers {
		switch provider.ID {
		case "tavily":
			tavily = provider
		case "searxng":
			searxng = provider
		}
	}
	if !tavily.SecretConfigured || !tavily.SecretAvailable || tavily.SecretSource != "saved" || !tavily.Ready {
		t.Fatalf("Tavily 脱敏状态不完整: %+v", tavily)
	}
	if searxng.Endpoint != "https://search.example.com" || searxng.EndpointSource != "saved" || !searxng.Ready {
		t.Fatalf("SearXNG 页面配置状态不完整: %+v", searxng)
	}
	stored, err := database.GetResearchSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Providers["tavily"].Secret != secret {
		t.Fatal("密钥没有持久化到服务端设置")
	}

	bootstrapRequest := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	bootstrapResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(bootstrapResponse, bootstrapRequest)
	if bootstrapResponse.Code != http.StatusOK {
		t.Fatalf("读取 Bootstrap 失败: HTTP=%d body=%s", bootstrapResponse.Code, bootstrapResponse.Body.String())
	}
	if strings.Contains(bootstrapResponse.Body.String(), secret) {
		t.Fatal("Bootstrap 响应泄露了 Research 密钥")
	}
}

func TestResearchSettingsAPIRejectsUnknownProviderAndSecretInURL(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())

	for name, body := range map[string]string{
		"unknown provider": `{"providers":[{"id":"invented","secret":"value"}]}`,
		"secret in URL":    `{"providers":[{"id":"searxng","endpoint":"https://user:secret@search.example.com"}]}`,
		"oversized secret": `{"providers":[{"id":"tavily","secret":"` + strings.Repeat("x", 16_385) + `"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/api/v1/research/settings", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			application.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("无效配置应被拒绝: HTTP=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestResearchProviderTestClassifiesIncompleteConfigurationAsBadRequest(t *testing.T) {
	t.Setenv("EASYAGENT_TAVILY_API_KEY", "")
	t.Setenv("TAVILY_API_KEY", "")
	database, err := store.Open(filepath.Join(t.TempDir(), "easyagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	application := newTestApplication(t, database, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	defer application.Shutdown(context.Background())

	request := httptest.NewRequest(http.MethodPost, "/api/v1/research/test", strings.NewReader(`{"providerId":"tavily","providers":[]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "配置不完整") {
		t.Fatalf("Provider 配置错误应返回 400: HTTP=%d body=%s", response.Code, response.Body.String())
	}
}
