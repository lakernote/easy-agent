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
	htmlTagPattern       = regexp.MustCompile(`(?s)<[^>]*>`)
)

// webSearchTool 是无需 API Key 的轻量网页发现能力。它只返回搜索结果摘要；
// 遇到 GitHub 仓库链接时再读取官方 API，把实时 star 等元数据放进同一结果，
// 让小模型不必自己猜 owner/repository 或拼接脆弱的 curl 命令。
func webSearchTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "web_search",
			Description: "搜索互联网并返回标题、真实 URL 和摘要，用于最新资料、未知实体和不完整名称。GitHub 仓库结果会附带官方 API 的实时 star、fork、语言和更新时间；不要猜测网址或数值。",
			Parameters: objectSchema(map[string]any{
				"query": stringSchema("搜索关键词，例如 easypostman github"),
				"max_results": map[string]any{
					"type": "integer", "description": "返回条数，默认 5，范围 1-10", "minimum": 1, "maximum": 10,
				},
			}, []string{"query"}),
		},
		Run: runWebSearch,
	}
}

type webSearchResult struct {
	Title   string          `json:"title"`
	URL     string          `json:"url"`
	Snippet string          `json:"snippet,omitempty"`
	GitHub  *githubMetadata `json:"github,omitempty"`
}

type githubMetadata struct {
	FullName   string `json:"full_name"`
	Stars      int    `json:"stars"`
	Forks      int    `json:"forks"`
	OpenIssues int    `json:"open_issues"`
	Language   string `json:"language,omitempty"`
	UpdatedAt  string `json:"updated_at"`
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
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EasyAgent/0.1; +https://github.com/lakernote/easy-agent)")
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
	for index := range results {
		results[index].GitHub = readGitHubMetadata(ctx, client, results[index].URL)
	}

	output, err := json.MarshalIndent(map[string]any{
		"ok": true, "query": arguments.Query, "source": "DuckDuckGo", "retrieved_at": time.Now().Format(time.RFC3339), "results": results,
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

func readGitHubMetadata(ctx context.Context, client *http.Client, target string) *githubMetadata {
	parsed, err := url.Parse(target)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return nil
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	repository := strings.TrimSuffix(parts[1], ".git")
	var response struct {
		FullName        string `json:"full_name"`
		StargazersCount int    `json:"stargazers_count"`
		ForksCount      int    `json:"forks_count"`
		OpenIssuesCount int    `json:"open_issues_count"`
		Language        string `json:"language"`
		UpdatedAt       string `json:"updated_at"`
	}
	endpoint := "https://api.github.com/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(repository)
	if err := getJSON(ctx, client, endpoint, &response); err != nil || response.FullName == "" {
		return nil
	}
	return &githubMetadata{
		FullName: response.FullName, Stars: response.StargazersCount, Forks: response.ForksCount,
		OpenIssues: response.OpenIssuesCount, Language: response.Language, UpdatedAt: response.UpdatedAt,
	}
}
