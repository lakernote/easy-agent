package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"
)

const maxResearchSearchBytes = 2 * 1024 * 1024

var httpURLPattern = regexp.MustCompile("https?://[^\\s<>()\\\"']+")

type duckDuckGoSearchProvider struct{ client *http.Client }
type bingSearchProvider struct{ client *http.Client }
type searXNGSearchProvider struct {
	client   *http.Client
	endpoint string
}
type braveSearchProvider struct {
	client *http.Client
	key    string
}

func defaultResearchSearchProviders() []researchSearchProvider {
	client := &http.Client{Timeout: 12 * time.Second}
	providers := make([]researchSearchProvider, 0, 4)
	if endpoint := strings.TrimSpace(os.Getenv("EASYAGENT_SEARXNG_URL")); endpoint != "" {
		if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			providers = append(providers, &searXNGSearchProvider{client: client, endpoint: strings.TrimRight(endpoint, "/")})
		}
	}
	key := strings.TrimSpace(os.Getenv("EASYAGENT_BRAVE_SEARCH_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("BRAVE_SEARCH_API_KEY"))
	}
	if key != "" {
		providers = append(providers, &braveSearchProvider{client: client, key: key})
	}
	// 两个零配置 provider 始终作为降级路径。HTML provider 可能受验证码或
	// 页面结构变化影响，因此生产部署仍建议配置 SearXNG 或 Brave Search。
	providers = append(providers,
		&duckDuckGoSearchProvider{client: client},
		&bingSearchProvider{client: client},
	)
	return providers
}

func (provider *duckDuckGoSearchProvider) Name() string { return "duckduckgo_html" }
func (provider *bingSearchProvider) Name() string       { return "bing_html" }
func (provider *searXNGSearchProvider) Name() string    { return "searxng" }
func (provider *braveSearchProvider) Name() string      { return "brave_search" }

func (engine *researchEngine) search(ctx context.Context, arguments researchArguments) ([]researchSearchResult, []researchAttempt, error) {
	limit := min(max(arguments.MaxSources*4, 16), 40)
	queries := planResearchQueries(arguments.Query, arguments.Depth, arguments.Domains)
	type response struct {
		results []researchSearchResult
		attempt researchAttempt
		err     error
		queryID int
	}
	responses := make(chan response, len(queries)*len(engine.providers))
	semaphore := make(chan struct{}, 8)
	var wait sync.WaitGroup
	for queryID, query := range queries {
		for _, provider := range engine.providers {
			wait.Add(1)
			go func(queryID int, query string, provider researchSearchProvider) {
				defer wait.Done()
				select {
				case semaphore <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-semaphore }()
				startedAt := time.Now()
				results, err := provider.Search(ctx, researchSearchRequest{Query: query, Freshness: arguments.Freshness, Limit: limit})
				attempt := researchAttempt{
					Stage: "search", Provider: provider.Name(), Query: query,
					OK: err == nil && len(results) > 0, ResultCount: len(results),
					DurationMS: time.Since(startedAt).Milliseconds(),
				}
				if err != nil {
					attempt.Error = compactResearchError(err)
				} else if len(results) == 0 {
					attempt.Error = "没有返回结果"
				}
				responses <- response{results: results, attempt: attempt, err: err, queryID: queryID}
			}(queryID, query, provider)
		}
	}
	go func() {
		wait.Wait()
		close(responses)
	}()

	all := directURLCandidates(arguments.Query)
	attempts := make([]researchAttempt, 0, len(queries)*len(engine.providers))
	failures := make([]error, 0)
	for item := range responses {
		attempts = append(attempts, item.attempt)
		if item.err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.attempt.Provider, item.err))
			continue
		}
		queryWeight := 1.0 / float64(item.queryID+1)
		for index := range item.results {
			item.results[index].Score += queryWeight * 100 / float64(index+1)
			all = append(all, item.results[index])
		}
	}
	ranked := rankResearchCandidates(all, arguments.Query, arguments.Domains)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	if len(ranked) == 0 {
		if len(failures) == 0 {
			failures = append(failures, errors.New("所有搜索 provider 均未返回候选"))
		}
		return nil, attempts, errors.Join(failures...)
	}
	if len(failures) > 0 {
		return ranked, attempts, errors.Join(failures...)
	}
	return ranked, attempts, nil
}

func planResearchQueries(query, depth string, domains []string) []string {
	result := make([]string, 0, 10)
	for _, domain := range domains {
		result = append(result, "site:"+domain+" "+query)
	}
	result = append(result, query)
	if depth == "normal" || depth == "deep" {
		result = append(result, query+" official source")
	}
	if depth == "deep" {
		result = append(result, query+" official documentation", query+" primary source")
	}
	return uniqueStrings(result)
}

func directURLCandidates(query string) []researchSearchResult {
	values := httpURLPattern.FindAllString(query, 4)
	result := make([]researchSearchResult, 0, len(values))
	for _, value := range values {
		value = strings.TrimRight(value, ".,;:!?，。；：！？")
		if target := canonicalResearchURL(value); target != "" {
			result = append(result, researchSearchResult{Title: target, URL: target, Score: 10_000, Providers: []string{"direct_url"}})
		}
	}
	return result
}

func rankResearchCandidates(values []researchSearchResult, query string, domains []string) []researchSearchResult {
	byURL := make(map[string]researchSearchResult, len(values))
	for _, value := range values {
		value.URL = canonicalResearchURL(value.URL)
		if value.URL == "" {
			continue
		}
		key := strings.ToLower(value.URL)
		value.Score += titleQueryScore(value.Title, query)
		if strings.HasPrefix(value.URL, "https://") {
			value.Score += 2
		}
		for _, domain := range domains {
			host := researchDomain(value.URL)
			if host == domain || strings.HasSuffix(host, "."+domain) {
				value.Score += 250
				break
			}
		}
		existing, ok := byURL[key]
		if !ok {
			value.Providers = uniqueStrings(value.Providers)
			byURL[key] = value
			continue
		}
		existing.Score += value.Score + 35
		existing.Providers = uniqueStrings(append(existing.Providers, value.Providers...))
		if len(value.Title) > len(existing.Title) {
			existing.Title = value.Title
		}
		if len(value.Snippet) > len(existing.Snippet) {
			existing.Snippet = value.Snippet
		}
		byURL[key] = existing
	}
	result := make([]researchSearchResult, 0, len(byURL))
	for _, value := range byURL {
		result = append(result, value)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if math.Abs(result[i].Score-result[j].Score) > 0.001 {
			return result[i].Score > result[j].Score
		}
		return result[i].URL < result[j].URL
	})
	for index := range result {
		result[index].Rank = index + 1
	}
	return result
}

func titleQueryScore(title, query string) float64 {
	title = strings.ToLower(title)
	terms := researchTerms(query)
	matches := 0
	for _, term := range terms {
		if len([]rune(term)) >= 2 && strings.Contains(title, term) {
			matches++
		}
	}
	return float64(matches) * 5
}

func (provider *duckDuckGoSearchProvider) Search(ctx context.Context, input researchSearchRequest) ([]researchSearchResult, error) {
	params := url.Values{"q": {input.Query}}
	if value := duckDuckGoFreshness(input.Freshness); value != "" {
		params.Set("df", value)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://html.duckduckgo.com/html/?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	setResearchSearchHeaders(request)
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maxResearchSearchBytes)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusAccepted || isSearchChallenge(body) {
		return nil, errors.New("搜索 provider 返回了机器人验证页面")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	results := parseDuckDuckGoResults(string(body), input.Limit)
	for index := range results {
		results[index].Providers = []string{provider.Name()}
	}
	return results, nil
}

func duckDuckGoFreshness(value string) string {
	return map[string]string{"day": "d", "week": "w", "month": "m", "year": "y"}[value]
}

func parseDuckDuckGoResults(body string, limit int) []researchSearchResult {
	document, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	results := make([]researchSearchResult, 0, limit)
	walkHTML(document, func(node *xhtml.Node) bool {
		if len(results) >= limit || node.Type != xhtml.ElementNode || !hasHTMLClass(node, "result") {
			return true
		}
		link := findHTMLDescendant(node, func(candidate *xhtml.Node) bool {
			return candidate.Type == xhtml.ElementNode && candidate.Data == "a" && hasHTMLClass(candidate, "result__a")
		})
		if link == nil {
			return true
		}
		target := searchTarget(htmlAttribute(link, "href"))
		if target == "" {
			return true
		}
		snippetNode := findHTMLDescendant(node, func(candidate *xhtml.Node) bool {
			return candidate.Type == xhtml.ElementNode && hasHTMLClass(candidate, "result__snippet")
		})
		result := researchSearchResult{Title: htmlNodeText(link), URL: target}
		if snippetNode != nil {
			result.Snippet = htmlNodeText(snippetNode)
		}
		results = append(results, result)
		return true
	})
	return results
}

func (provider *bingSearchProvider) Search(ctx context.Context, input researchSearchRequest) ([]researchSearchResult, error) {
	query := input.Query
	if suffix := freshnessSearchSuffix(input.Freshness); suffix != "" {
		query += " " + suffix
	}
	params := url.Values{"q": {query}, "count": {fmt.Sprintf("%d", input.Limit)}, "setlang": {"zh-hans"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.bing.com/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	setResearchSearchHeaders(request)
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maxResearchSearchBytes)
	if err != nil {
		return nil, err
	}
	if isSearchChallenge(body) {
		return nil, errors.New("搜索 provider 返回了机器人验证页面")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	results := parseBingResults(string(body), input.Limit)
	for index := range results {
		results[index].Providers = []string{provider.Name()}
	}
	return results, nil
}

func parseBingResults(body string, limit int) []researchSearchResult {
	document, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	results := make([]researchSearchResult, 0, limit)
	walkHTML(document, func(node *xhtml.Node) bool {
		if len(results) >= limit || node.Type != xhtml.ElementNode || node.Data != "li" || !hasHTMLClass(node, "b_algo") {
			return true
		}
		link := findHTMLDescendant(node, func(candidate *xhtml.Node) bool {
			if candidate.Type != xhtml.ElementNode || candidate.Data != "a" {
				return false
			}
			parent := candidate.Parent
			return parent != nil && parent.Type == xhtml.ElementNode && parent.Data == "h2"
		})
		if link == nil {
			return true
		}
		target := canonicalResearchURL(htmlAttribute(link, "href"))
		if target == "" {
			return true
		}
		snippetNode := findHTMLDescendant(node, func(candidate *xhtml.Node) bool {
			return candidate.Type == xhtml.ElementNode && candidate.Data == "p"
		})
		item := researchSearchResult{Title: htmlNodeText(link), URL: target}
		if snippetNode != nil {
			item.Snippet = htmlNodeText(snippetNode)
		}
		results = append(results, item)
		return true
	})
	return results
}

func (provider *searXNGSearchProvider) Search(ctx context.Context, input researchSearchRequest) ([]researchSearchResult, error) {
	endpoint := provider.endpoint
	if !strings.HasSuffix(strings.TrimRight(endpoint, "/"), "/search") {
		endpoint = strings.TrimRight(endpoint, "/") + "/search"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	params := parsed.Query()
	params.Set("q", input.Query)
	params.Set("format", "json")
	params.Set("categories", "general")
	if input.Freshness != "any" {
		params.Set("time_range", input.Freshness)
	}
	parsed.RawQuery = params.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "EasyAgent/1.0")
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := decodeBoundedJSON(response.Body, maxResearchSearchBytes, &payload); err != nil {
		return nil, err
	}
	results := make([]researchSearchResult, 0, min(input.Limit, len(payload.Results)))
	for _, value := range payload.Results {
		target := canonicalResearchURL(value.URL)
		if target == "" {
			continue
		}
		results = append(results, researchSearchResult{Title: compactWhitespace(value.Title), URL: target, Snippet: compactWhitespace(value.Content), Providers: []string{provider.Name()}})
		if len(results) == input.Limit {
			break
		}
	}
	return results, nil
}

func (provider *braveSearchProvider) Search(ctx context.Context, input researchSearchRequest) ([]researchSearchResult, error) {
	params := url.Values{"q": {input.Query}, "count": {fmt.Sprintf("%d", min(input.Limit, 20))}, "safesearch": {"moderate"}}
	if freshness := map[string]string{"day": "pd", "week": "pw", "month": "pm", "year": "py"}[input.Freshness]; freshness != "" {
		params.Set("freshness", freshness)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.search.brave.com/res/v1/web/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Subscription-Token", provider.key)
	request.Header.Set("User-Agent", "EasyAgent/1.0")
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := decodeBoundedJSON(response.Body, maxResearchSearchBytes, &payload); err != nil {
		return nil, err
	}
	results := make([]researchSearchResult, 0, len(payload.Web.Results))
	for _, value := range payload.Web.Results {
		target := canonicalResearchURL(value.URL)
		if target == "" {
			continue
		}
		results = append(results, researchSearchResult{Title: compactWhitespace(value.Title), URL: target, Snippet: compactWhitespace(value.Description), Providers: []string{provider.Name()}})
	}
	return results, nil
}

func searchTarget(value string) string {
	value = stdhtml.UnescapeString(strings.TrimSpace(value))
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if target := parsed.Query().Get("uddg"); target != "" {
		value = target
	}
	return canonicalResearchURL(value)
}

func setResearchSearchHeaders(request *http.Request) {
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EasyAgent/1.0; +https://github.com/lakernote/easy-agent)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
}

func isSearchChallenge(body []byte) bool {
	value := strings.ToLower(string(body))
	for _, marker := range []string{"anomaly-modal", "challenge-form", "bots use duckduckgo", "unusual traffic", "verify you are human", "captcha"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func freshnessSearchSuffix(value string) string {
	return map[string]string{
		"day": "\"past 24 hours\"", "week": "\"past week\"",
		"month": "\"past month\"", "year": "\"past year\"",
	}[value]
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("响应超过 %d 字节限制", limit)
	}
	return body, nil
}

func decodeBoundedJSON(reader io.Reader, limit int64, target any) error {
	body, err := readBounded(reader, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("JSON 响应无效: %w", err)
	}
	return nil
}

func walkHTML(node *xhtml.Node, visit func(*xhtml.Node) bool) {
	if node == nil || !visit(node) {
		return
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, visit)
	}
}

func findHTMLDescendant(node *xhtml.Node, match func(*xhtml.Node) bool) *xhtml.Node {
	var found *xhtml.Node
	walkHTML(node, func(candidate *xhtml.Node) bool {
		if candidate != node && match(candidate) {
			found = candidate
			return false
		}
		return found == nil
	})
	return found
}

func hasHTMLClass(node *xhtml.Node, wanted string) bool {
	for _, value := range strings.Fields(htmlAttribute(node, "class")) {
		if value == wanted {
			return true
		}
	}
	return false
}

func htmlAttribute(node *xhtml.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}
	return ""
}

func htmlNodeText(node *xhtml.Node) string {
	var builder strings.Builder
	walkHTML(node, func(candidate *xhtml.Node) bool {
		if candidate.Type == xhtml.TextNode {
			builder.WriteString(candidate.Data)
			builder.WriteByte(' ')
		}
		return true
	})
	return compactWhitespace(stdhtml.UnescapeString(builder.String()))
}

func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
