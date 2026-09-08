package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	ResearchProviderTavily    = "tavily"
	ResearchProviderSearXNG   = "searxng"
	ResearchProviderBrave     = "brave"
	ResearchProviderFirecrawl = "firecrawl"
	ResearchProviderReader    = "reader"
	ResearchProviderGitHub    = "github"
)

var ErrResearchProviderConfiguration = errors.New("research provider configuration invalid")

type ResearchProviderConfig struct {
	Endpoint string
	Secret   string
}

// ResearchConfig is resolved once at the beginning of an Agent turn. The
// resulting web_research Tool captures this immutable snapshot, so changing
// settings affects the next turn without mutating an in-flight run.
type ResearchConfig struct {
	Providers map[string]ResearchProviderConfig
}

type ResearchProviderDefinition struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Category             string   `json:"category"`
	Description          string   `json:"description"`
	EndpointLabel        string   `json:"endpointLabel,omitempty"`
	EndpointPlaceholder  string   `json:"endpointPlaceholder,omitempty"`
	SecretLabel          string   `json:"secretLabel,omitempty"`
	RequiresEndpoint     bool     `json:"requiresEndpoint,omitempty"`
	RequiresSecret       bool     `json:"requiresSecret,omitempty"`
	AvailableWithoutAuth bool     `json:"availableWithoutAuth,omitempty"`
	PrimarySearch        bool     `json:"primarySearch,omitempty"`
	EnvironmentVariables []string `json:"environmentVariables,omitempty"`
}

type ResearchProviderTestResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ResultCount int    `json:"resultCount,omitempty"`
	DurationMS  int64  `json:"durationMs"`
	Detail      string `json:"detail"`
}

type ResearchExecutionLayerDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Components  string `json:"components"`
	Description string `json:"description"`
}

var researchProviderDefinitions = []ResearchProviderDefinition{
	{
		ID: ResearchProviderTavily, Name: "Tavily", Category: "主搜索源",
		Description: "稳定的结构化 Search API，适合生产环境默认搜索。",
		SecretLabel: "API Key", RequiresSecret: true, PrimarySearch: true,
		EnvironmentVariables: []string{"EASYAGENT_TAVILY_API_KEY", "TAVILY_API_KEY"},
	},
	{
		ID: ResearchProviderSearXNG, Name: "SearXNG", Category: "主搜索源",
		Description:   "可自托管的聚合搜索服务，适合内网和隐私部署。",
		EndpointLabel: "SearXNG URL", EndpointPlaceholder: "https://search.example.com", RequiresEndpoint: true, PrimarySearch: true,
		EnvironmentVariables: []string{"EASYAGENT_SEARXNG_URL"},
	},
	{
		ID: ResearchProviderBrave, Name: "Brave Search", Category: "主搜索源",
		Description: "独立的 Search API，可作为 Tavily 之外的第二个生产搜索源。",
		SecretLabel: "API Key", RequiresSecret: true, PrimarySearch: true,
		EnvironmentVariables: []string{"EASYAGENT_BRAVE_SEARCH_API_KEY", "BRAVE_SEARCH_API_KEY"},
	},
	{
		ID: ResearchProviderFirecrawl, Name: "Firecrawl", Category: "搜索与正文",
		Description:   "Search API 加动态页面/PDF 正文提取；直连页面不可读时自动作为降级，不向模型暴露额外工具。",
		EndpointLabel: "API Base URL", EndpointPlaceholder: "https://api.firecrawl.dev/v2",
		SecretLabel: "API Key", RequiresSecret: true, PrimarySearch: true,
		EnvironmentVariables: []string{"EASYAGENT_FIRECRAWL_URL", "EASYAGENT_FIRECRAWL_API_KEY", "FIRECRAWL_API_KEY"},
	},
	{
		ID: ResearchProviderReader, Name: "Reader", Category: "正文读取",
		Description:   "为 PDF、JavaScript 页面或静态提取失败页面提供正文降级。",
		EndpointLabel: "Reader URL", EndpointPlaceholder: "https://reader.example.com/{url}", RequiresEndpoint: true,
		SecretLabel:          "Bearer Token",
		EnvironmentVariables: []string{"EASYAGENT_READER_URL", "EASYAGENT_READER_API_KEY"},
	},
	{
		ID: ResearchProviderGitHub, Name: "GitHub", Category: "精确数据",
		Description: "公开仓库可匿名读取；Token 可提高 REST API 限额，不会发给模型。",
		SecretLabel: "Personal Access Token", AvailableWithoutAuth: true,
		EnvironmentVariables: []string{"GITHUB_TOKEN", "GH_TOKEN"},
	},
}

var researchExecutionLayerDefinitions = []ResearchExecutionLayerDefinition{
	{ID: "fallback_search", Name: "零配置降级", Components: "DuckDuckGo / Bing HTML", Description: "仅在生产搜索源候选不足时使用"},
	{ID: "structured_data", Name: "内置精确数据", Components: "Open-Meteo · Yahoo Finance · Wikidata", Description: "由模型选择数据类型，Runtime 负责确定性读取"},
}

func ResearchProviderDefinitions() []ResearchProviderDefinition {
	return append([]ResearchProviderDefinition(nil), researchProviderDefinitions...)
}

func ResearchExecutionLayerDefinitions() []ResearchExecutionLayerDefinition {
	return append([]ResearchExecutionLayerDefinition(nil), researchExecutionLayerDefinitions...)
}

func ResearchProviderDefinitionByID(id string) (ResearchProviderDefinition, bool) {
	for _, definition := range researchProviderDefinitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return ResearchProviderDefinition{}, false
}

func ResearchConfigFromEnvironment() ResearchConfig {
	return ResearchConfig{Providers: map[string]ResearchProviderConfig{
		ResearchProviderTavily:  {Secret: firstEnvironmentValue("EASYAGENT_TAVILY_API_KEY", "TAVILY_API_KEY")},
		ResearchProviderSearXNG: {Endpoint: firstEnvironmentValue("EASYAGENT_SEARXNG_URL")},
		ResearchProviderBrave:   {Secret: firstEnvironmentValue("EASYAGENT_BRAVE_SEARCH_API_KEY", "BRAVE_SEARCH_API_KEY")},
		ResearchProviderFirecrawl: {
			Endpoint: firstEnvironmentValue("EASYAGENT_FIRECRAWL_URL"),
			Secret:   firstEnvironmentValue("EASYAGENT_FIRECRAWL_API_KEY", "FIRECRAWL_API_KEY"),
		},
		ResearchProviderReader: {
			Endpoint: firstEnvironmentValue("EASYAGENT_READER_URL"),
			Secret:   firstEnvironmentValue("EASYAGENT_READER_API_KEY"),
		},
		ResearchProviderGitHub: {Secret: firstEnvironmentValue("GITHUB_TOKEN", "GH_TOKEN")},
	}}
}

func MergeResearchConfig(base, overrides ResearchConfig) ResearchConfig {
	result := ResearchConfig{Providers: make(map[string]ResearchProviderConfig, len(base.Providers)+len(overrides.Providers))}
	for id, provider := range base.Providers {
		result.Providers[id] = provider
	}
	for id, override := range overrides.Providers {
		provider := result.Providers[id]
		if value := strings.TrimSpace(override.Endpoint); value != "" {
			provider.Endpoint = value
		}
		if value := strings.TrimSpace(override.Secret); value != "" {
			provider.Secret = value
		}
		result.Providers[id] = provider
	}
	return result
}

func (config ResearchConfig) Provider(id string) ResearchProviderConfig {
	return config.Providers[id]
}

func ResearchProviderReady(config ResearchConfig, id string) bool {
	definition, ok := ResearchProviderDefinitionByID(id)
	if !ok {
		return false
	}
	provider := config.Provider(id)
	if definition.RequiresEndpoint && strings.TrimSpace(provider.Endpoint) == "" {
		return false
	}
	if definition.RequiresSecret && strings.TrimSpace(provider.Secret) == "" {
		return false
	}
	return definition.AvailableWithoutAuth || definition.RequiresEndpoint || definition.RequiresSecret
}

func ValidateResearchConfig(config ResearchConfig) error {
	for _, definition := range researchProviderDefinitions {
		endpoint := strings.TrimSpace(config.Provider(definition.ID).Endpoint)
		if endpoint == "" {
			continue
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("%s URL 必须是不含用户信息的 http/https 地址", definition.Name)
		}
	}
	return nil
}

func TestResearchProvider(ctx context.Context, config ResearchConfig, id string) (ResearchProviderTestResult, error) {
	definition, ok := ResearchProviderDefinitionByID(id)
	if !ok {
		return ResearchProviderTestResult{}, fmt.Errorf("%w: 不支持的 Research Provider: %s", ErrResearchProviderConfiguration, id)
	}
	// “测试连接”只验证当前卡片的草稿；另一张尚未填写完成的卡片
	// 不应阻止管理员确认当前 Provider 是否可用。
	selectedConfig := ResearchConfig{Providers: map[string]ResearchProviderConfig{id: config.Provider(id)}}
	if err := ValidateResearchConfig(selectedConfig); err != nil {
		return ResearchProviderTestResult{}, fmt.Errorf("%w: %v", ErrResearchProviderConfiguration, err)
	}
	if !ResearchProviderReady(config, id) {
		return ResearchProviderTestResult{}, fmt.Errorf("%w: %s 配置不完整", ErrResearchProviderConfiguration, definition.Name)
	}
	started := time.Now()
	result := ResearchProviderTestResult{ID: id, Name: definition.Name}
	switch id {
	case ResearchProviderTavily, ResearchProviderSearXNG, ResearchProviderBrave, ResearchProviderFirecrawl:
		var selected researchSearchProvider
		for _, provider := range researchSearchProviders(config) {
			if provider.Name() == id || (id == ResearchProviderBrave && provider.Name() == "brave_search") {
				selected = provider
				break
			}
		}
		if selected == nil {
			return ResearchProviderTestResult{}, fmt.Errorf("%s 没有加载", definition.Name)
		}
		values, err := selected.Search(ctx, researchSearchRequest{Query: "EasyAgent GitHub", Freshness: "any", Limit: 1})
		if err != nil {
			return ResearchProviderTestResult{}, fmt.Errorf("%s 连接失败: %w", definition.Name, err)
		}
		if len(values) == 0 {
			return ResearchProviderTestResult{}, fmt.Errorf("%s 连接成功，但测试查询没有结果", definition.Name)
		}
		result.ResultCount = len(values)
		result.Detail = fmt.Sprintf("真实搜索成功，返回 %d 个候选", len(values))
	case ResearchProviderReader:
		fetcher := newResearchFetcherWithConfig(config)
		source, err := fetcher.fetchViaReader(ctx, researchSource{URL: "https://example.com/", Provider: "provider_test", Kind: "web_page"}, "Example Domain", 800)
		if err != nil {
			return ResearchProviderTestResult{}, fmt.Errorf("Reader 连接失败: %w", err)
		}
		if strings.TrimSpace(source.Content) == "" {
			return ResearchProviderTestResult{}, errors.New("Reader 连接成功，但没有返回正文")
		}
		result.ResultCount = 1
		result.Detail = "真实网页正文读取成功"
	case ResearchProviderGitHub:
		provider := config.Provider(ResearchProviderGitHub)
		var payload struct {
			Resources map[string]struct {
				Limit     int `json:"limit"`
				Remaining int `json:"remaining"`
			} `json:"resources"`
		}
		headers := make(http.Header)
		headers.Set("Accept", "application/vnd.github+json")
		headers.Set("X-GitHub-Api-Version", "2022-11-28")
		if provider.Secret != "" {
			headers.Set("Authorization", "Bearer "+provider.Secret)
		}
		if err := getResearchJSON(ctx, safeResearchHTTPClient(12*time.Second), "https://api.github.com/rate_limit", headers, &payload); err != nil {
			return ResearchProviderTestResult{}, fmt.Errorf("GitHub 连接失败: %w", err)
		}
		core := payload.Resources["core"]
		result.ResultCount = 1
		result.Detail = fmt.Sprintf("GitHub API 可用，当前限额剩余 %d/%d", core.Remaining, core.Limit)
	default:
		return ResearchProviderTestResult{}, fmt.Errorf("暂不支持测试 %s", definition.Name)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

func firecrawlAPIEndpoint(baseURL, operation string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.firecrawl.dev/v2"
	}
	for _, suffix := range []string{"/search", "/scrape"} {
		if strings.HasSuffix(baseURL, suffix) {
			baseURL = strings.TrimSuffix(baseURL, suffix)
			break
		}
	}
	return baseURL + "/" + strings.TrimLeft(operation, "/")
}

func firstEnvironmentValue(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}
