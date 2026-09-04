package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
)

const maxSearchResponseBytes = 2 * 1024 * 1024

var (
	searchResultPattern  = regexp.MustCompile(`(?is)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	searchSnippetPattern = regexp.MustCompile(`(?is)<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)
	htmlMainPattern      = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main>`)
	htmlNoisePattern     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<noscript[^>]*>.*?</noscript>`)
	htmlTagPattern       = regexp.MustCompile(`(?s)<[^>]*>`)
)

// webSearchTool 是无需 API Key 的轻量网页发现能力。它只返回通用搜索结果，
// 不识别或增强特定网站；外部平台的精确结构化数据应交给对应 MCP。
func webSearchTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:          "web_search",
			Description:   "发现互联网上的候选来源并返回标题、真实 URL 和摘要。搜索摘要不是原始证据：得到候选后必须继续调用 web_fetch、Shell 或对应 MCP 读取权威来源，核对实体与关键数值后才能回答外部事实。",
			DiscoveryOnly: true,
			Parameters: objectSchema(map[string]any{
				"query": stringSchema("搜索关键词，例如 EasyAgent release notes"),
				"max_results": map[string]any{
					"type": "integer", "description": "返回条数，默认 5，范围 1-10", "minimum": 1, "maximum": 10,
				},
			}, []string{"query"}),
		},
		Run: runWebSearch,
	}
}

type webSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func runWebSearch(ctx context.Context, raw json.RawMessage) (string, error) {
	var arguments struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return "", fmt.Errorf("web_search 参数错误: %w", err)
	}
	arguments.Query = strings.TrimSpace(arguments.Query)
	if arguments.Query == "" {
		return "", errors.New("query 不能为空")
	}
	if arguments.MaxResults == 0 {
		arguments.MaxResults = 5
	}
	if arguments.MaxResults < 1 || arguments.MaxResults > 10 {
		return "", errors.New("max_results 必须在 1 到 10 之间")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(arguments.Query)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EasyAgent/0.1)")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("网页搜索失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSearchResponseBytes))
	if err != nil {
		return "", fmt.Errorf("读取搜索结果失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("网页搜索 HTTP %d: %s", response.StatusCode, compactText(string(body)))
	}

	results := parseDuckDuckGoResults(string(body), arguments.MaxResults)
	if len(results) == 0 {
		output, _ := json.Marshal(map[string]any{"ok": false, "query": arguments.Query, "results": []any{}, "error": "没有搜索到结果，请调整关键词后重试"})
		return string(output), errors.New("没有搜索到结果，请调整关键词后重试")
	}
	output, err := json.MarshalIndent(map[string]any{
		"ok": true, "query": arguments.Query, "stage": "discovery", "evidence_status": "candidates_only",
		"source": "DuckDuckGo", "content_trust": untrustedExternal, "retrieved_at": time.Now().Format(time.RFC3339), "results": results,
		"next": "从候选中确认正确实体，再用 web_fetch、Shell 或对应 MCP 读取至少一个权威原始来源；不要只根据摘要回答精确事实。",
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func parseDuckDuckGoResults(body string, limit int) []webSearchResult {
	links := searchResultPattern.FindAllStringSubmatch(body, limit)
	snippets := searchSnippetPattern.FindAllStringSubmatch(body, limit)
	results := make([]webSearchResult, 0, len(links))
	for index, match := range links {
		if len(match) < 3 {
			continue
		}
		target := searchTarget(match[1])
		if target == "" {
			continue
		}
		result := webSearchResult{Title: cleanHTMLText(match[2]), URL: target}
		if index < len(snippets) && len(snippets[index]) > 1 {
			result.Snippet = cleanHTMLText(snippets[index][1])
		}
		results = append(results, result)
	}
	return results
}

func searchTarget(value string) string {
	value = html.UnescapeString(strings.TrimSpace(value))
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if target := parsed.Query().Get("uddg"); target != "" {
		value = target
		parsed, err = url.Parse(value)
		if err != nil {
			return ""
		}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.String()
}

func cleanHTMLText(value string) string {
	value = htmlNoisePattern.ReplaceAllString(value, " ")
	value = htmlTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func compactText(value string) string {
	value = cleanHTMLText(value)
	if len(value) > 500 {
		return value[:500] + "…"
	}
	return value
}
