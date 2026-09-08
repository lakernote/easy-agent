package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
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
	"strconv"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
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
type tavilySearchProvider struct {
	client   *http.Client
	key      string
	endpoint string
}

func defaultResearchSearchProviders() []researchSearchProvider {
	client := &http.Client{Timeout: 12 * time.Second}
	providers := make([]researchSearchProvider, 0, 5)
	tavilyKey := strings.TrimSpace(os.Getenv("EASYAGENT_TAVILY_API_KEY"))
	if tavilyKey == "" {
		tavilyKey = strings.TrimSpace(os.Getenv("TAVILY_API_KEY"))
	}
	if tavilyKey != "" {
		providers = append(providers, &tavilySearchProvider{client: client, key: tavilyKey, endpoint: "https://api.tavily.com/search"})
	}
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
	// 两个零配置 HTML provider 只作为降级路径。配置型 API provider 能给出
	// 足够候选时不会调用它们，避免额外延迟、验证码和页面结构漂移。
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
func (provider *tavilySearchProvider) Name() string     { return "tavily" }

func (engine *researchEngine) search(ctx context.Context, arguments researchArguments) ([]researchSearchResult, []researchAttempt, error) {
	limit := min(max(arguments.MaxSources*4, 16), 40)
	queries := planResearchQueries(arguments.Query, arguments.Depth, arguments.SourceScope, arguments.Domains)
	primaryProviders, fallbackProviders := splitResearchSearchProviders(engine.providers)
	all := directURLCandidates(arguments.Query)
	attempts := make([]researchAttempt, 0, len(queries)*len(engine.providers))
	failures := make([]error, 0)

	if len(primaryProviders) > 0 {
		results, primaryAttempts, primaryFailures := runResearchSearchProviders(ctx, queries, primaryProviders, arguments, limit)
		all = append(all, results...)
		attempts = append(attempts, primaryAttempts...)
		failures = append(failures, primaryFailures...)
	}
	minimumCandidates := min(limit, max(arguments.MaxSources+2, 4))
	primaryRanked := filterResearchCandidates(rankResearchCandidates(all, arguments.Query, arguments.Domains), arguments)
	if len(primaryProviders) == 0 || len(primaryRanked) < minimumCandidates {
		results, fallbackAttempts, fallbackFailures := runResearchSearchProviders(ctx, queries, fallbackProviders, arguments, limit)
		all = append(all, results...)
		attempts = append(attempts, fallbackAttempts...)
		failures = append(failures, fallbackFailures...)
	}

	ranked := filterResearchCandidates(rankResearchCandidates(all, arguments.Query, arguments.Domains), arguments)
	if len(ranked) == 0 && len(arguments.Domains) > 0 {
		sitemapResults, sitemapAttempts, sitemapFailures := discoverDomainSitemapCandidates(ctx, arguments.Domains)
		all = append(all, sitemapResults...)
		attempts = append(attempts, sitemapAttempts...)
		failures = append(failures, sitemapFailures...)
		ranked = filterResearchCandidates(rankResearchCandidates(all, arguments.Query, arguments.Domains), arguments)
	}
	if len(ranked) == 0 && len(arguments.Domains) > 0 {
		all = append(all, domainFallbackCandidates(arguments.Domains)...)
		ranked = filterResearchCandidates(rankResearchCandidates(all, arguments.Query, arguments.Domains), arguments)
	}
	ranked = diversifyResearchCandidates(ranked)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	if len(ranked) == 0 {
		if len(failures) == 0 {
			failures = append(failures, errors.New("所有搜索 provider 均未返回符合来源范围的候选"))
		}
		return nil, attempts, errors.Join(failures...)
	}
	if len(failures) > 0 {
		return ranked, attempts, errors.Join(failures...)
	}
	return ranked, attempts, nil
}

type researchSitemap struct {
	URLs []struct {
		Location string `xml:"loc"`
	} `xml:"url"`
}

func discoverDomainSitemapCandidates(ctx context.Context, domains []string) ([]researchSearchResult, []researchAttempt, []error) {
	type response struct {
		results []researchSearchResult
		attempt researchAttempt
		err     error
	}
	responses := make(chan response, len(domains))
	client := safeResearchHTTPClient(12 * time.Second)
	var wait sync.WaitGroup
	for _, domain := range domains {
		wait.Add(1)
		go func(domain string) {
			defer wait.Done()
			startedAt := time.Now()
			endpoint := "https://" + domain + "/sitemap.xml"
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err == nil {
				setResearchSearchHeaders(request)
			}
			var results []researchSearchResult
			if err == nil {
				var httpResponse *http.Response
				httpResponse, err = client.Do(request)
				if err == nil {
					defer httpResponse.Body.Close()
					if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
						err = fmt.Errorf("sitemap HTTP %d", httpResponse.StatusCode)
					} else {
						var body []byte
						body, err = readBounded(httpResponse.Body, maxResearchSearchBytes)
						if err == nil {
							results, err = parseResearchSitemap(body, domains)
						}
					}
				}
			}
			attempt := researchAttempt{
				Stage: "search", Provider: "domain_sitemap", Query: endpoint,
				OK: err == nil && len(results) > 0, ResultCount: len(results), DurationMS: time.Since(startedAt).Milliseconds(),
			}
			if err != nil {
				attempt.Error = compactResearchError(err)
			} else if len(results) == 0 {
				attempt.Error = "sitemap 没有站内页面"
			}
			responses <- response{results: results, attempt: attempt, err: err}
		}(domain)
	}
	wait.Wait()
	close(responses)
	results := make([]researchSearchResult, 0)
	attempts := make([]researchAttempt, 0, len(domains))
	failures := make([]error, 0)
	for item := range responses {
		results = append(results, item.results...)
		attempts = append(attempts, item.attempt)
		if item.err != nil {
			failures = append(failures, fmt.Errorf("domain_sitemap: %w", item.err))
		}
	}
	return results, attempts, failures
}

func parseResearchSitemap(body []byte, domains []string) ([]researchSearchResult, error) {
	var sitemap researchSitemap
	if err := xml.Unmarshal(body, &sitemap); err != nil {
		return nil, fmt.Errorf("sitemap XML 无效: %w", err)
	}
	result := make([]researchSearchResult, 0, min(len(sitemap.URLs), 5_000))
	for _, item := range sitemap.URLs {
		target := canonicalResearchURL(item.Location)
		if target == "" || !researchDomainAllowed(researchDomain(target), domains) {
			continue
		}
		result = append(result, researchSearchResult{
			Title: target, URL: target, Score: researchVersionPathScore(target), Providers: []string{"domain_sitemap"},
		})
		if len(result) >= 5_000 {
			break
		}
	}
	return result, nil
}

func domainFallbackCandidates(domains []string) []researchSearchResult {
	result := make([]researchSearchResult, 0, len(domains)*3)
	for _, domain := range domains {
		base := "https://" + domain
		for index, path := range []string{"/", "/documentation/", "/docs/"} {
			result = append(result, researchSearchResult{
				Title: domain + path, URL: base + path,
				Score: 300 - float64(index*10), Providers: []string{"domain_fallback"},
			})
		}
	}
	return result
}

func diversifyResearchCandidates(values []researchSearchResult) []researchSearchResult {
	seenDomains := make(map[string]struct{}, len(values))
	primary := make([]researchSearchResult, 0, len(values))
	deferred := make([]researchSearchResult, 0, len(values))
	for _, value := range values {
		domain := researchDomain(value.URL)
		if registrable, err := publicsuffix.EffectiveTLDPlusOne(domain); err == nil {
			domain = registrable
		}
		if _, exists := seenDomains[domain]; exists {
			deferred = append(deferred, value)
			continue
		}
		seenDomains[domain] = struct{}{}
		primary = append(primary, value)
	}
	result := append(primary, deferred...)
	for index := range result {
		result[index].Rank = index + 1
	}
	return result
}

func splitResearchSearchProviders(providers []researchSearchProvider) ([]researchSearchProvider, []researchSearchProvider) {
	primary := make([]researchSearchProvider, 0, len(providers))
	fallback := make([]researchSearchProvider, 0, 2)
	for _, provider := range providers {
		switch provider.Name() {
		case "duckduckgo_html", "bing_html":
			fallback = append(fallback, provider)
		default:
			primary = append(primary, provider)
		}
	}
	return primary, fallback
}

func runResearchSearchProviders(ctx context.Context, queries []string, providers []researchSearchProvider, arguments researchArguments, limit int) ([]researchSearchResult, []researchAttempt, []error) {
	type response struct {
		results []researchSearchResult
		attempt researchAttempt
		err     error
		queryID int
	}
	responses := make(chan response, len(queries)*len(providers))
	semaphore := make(chan struct{}, 8)
	var wait sync.WaitGroup
	for queryID, query := range queries {
		for _, provider := range providers {
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
				results, err := provider.Search(ctx, researchSearchRequest{
					Query: query, Depth: arguments.Depth, Freshness: arguments.Freshness,
					Limit: limit, Domains: arguments.Domains,
				})
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

	all := make([]researchSearchResult, 0)
	attempts := make([]researchAttempt, 0, len(queries)*len(providers))
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
	return all, attempts, failures
}

func planResearchQueries(query, depth, sourceScope string, domains []string) []string {
	result := make([]string, 0, 12)
	precisionQuery := precisionResearchQuery(query)
	disambiguationQueries := disambiguationResearchQueries(query)
	for _, domain := range domains {
		result = append(result, "site:"+domain+" "+query)
		if precisionQuery != "" {
			result = append(result, "site:"+domain+" "+precisionQuery)
		}
	}
	// Search engines frequently autocorrect short product, project, and person names
	// (for example, Laker -> Lakers). Give exact entity spelling the highest weight
	// when no site constraint already occupies the first query.
	if len(domains) == 0 && precisionQuery != "" {
		result = append(result, precisionQuery)
	}
	if len(domains) == 0 {
		result = append(result, disambiguationQueries...)
	}
	result = append(result, query)
	if sourceScope == "official" || depth == "normal" || depth == "deep" {
		result = append(result, query+" official source")
	}
	if depth == "deep" {
		result = append(result, query+" official documentation", query+" primary source")
	}
	return uniqueStrings(result)
}

var precisionResearchWord = regexp.MustCompile(`[A-Za-z][A-Za-z0-9._-]{2,39}`)

func precisionResearchQuery(query string) string {
	quoted := 0
	result := precisionResearchWord.ReplaceAllStringFunc(query, func(term string) string {
		if quoted >= 3 || strings.Contains(query, `"`+term+`"`) || !looksLikeResearchEntity(term) {
			return term
		}
		quoted++
		return `"` + term + `"`
	})
	if quoted == 0 || result == query {
		return ""
	}
	return result
}

func disambiguationResearchQueries(query string) []string {
	lower := strings.ToLower(query)
	isIdentityQuestion := strings.Contains(query, "是谁") || strings.Contains(query, "是什么") ||
		strings.Contains(query, "含义") || strings.Contains(lower, "who is") ||
		strings.Contains(lower, "what is") || strings.Contains(lower, "meaning")
	if !isIdentityQuestion {
		return nil
	}
	terms := precisionResearchTerms(query)
	if len(terms) != 1 {
		return nil
	}
	entity := `"` + terms[0] + `"`
	return []string{entity + " meaning", entity + " biography"}
}

func precisionResearchTerms(query string) []string {
	result := make([]string, 0, 3)
	for _, term := range precisionResearchWord.FindAllString(query, 6) {
		if looksLikeResearchEntity(term) {
			result = append(result, term)
		}
	}
	return uniqueStrings(result)
}

func looksLikeResearchEntity(term string) bool {
	for index := 0; index < len(term); index++ {
		if term[index] >= 'A' && term[index] <= 'Z' {
			return true
		}
	}
	return strings.ContainsAny(term, ".-_0123456789")
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
		value.Score += researchURLQualityScore(value.URL)
		if strings.HasPrefix(value.URL, "https://") {
			value.Score += 2
		}
		if len(domains) > 0 {
			if !researchDomainAllowed(researchDomain(value.URL), domains) {
				continue
			}
			value.Score += 250
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

func filterResearchCandidates(values []researchSearchResult, arguments researchArguments) []researchSearchResult {
	if arguments.SourceScope != "official" || len(arguments.Domains) > 0 {
		return values
	}
	terms := officialEntityTerms(arguments.Query)
	result := make([]researchSearchResult, 0, len(values))
	for _, value := range values {
		if containsString(value.Providers, "direct_url") || likelyOfficialResearchURL(value.URL, terms) {
			result = append(result, value)
		}
	}
	for index := range result {
		result[index].Rank = index + 1
	}
	return result
}

var officialEntityWord = regexp.MustCompile(`[a-z0-9][a-z0-9._-]+`)

func officialEntityTerms(query string) []string {
	stop := map[string]struct{}{
		"official": {}, "documentation": {}, "documents": {}, "source": {}, "sources": {},
		"current": {}, "latest": {}, "recent": {}, "update": {}, "updates": {}, "today": {},
		"guide": {}, "reference": {}, "implementation": {}, "design": {}, "architecture": {},
		"partition": {}, "replication": {}, "consumer": {}, "group": {}, "offset": {},
		"with": {}, "from": {}, "only": {}, "about": {}, "what": {}, "when": {}, "where": {},
	}
	result := make([]string, 0, 6)
	for _, term := range officialEntityWord.FindAllString(strings.ToLower(query), 12) {
		term = strings.Trim(term, "._-")
		if len(term) < 2 {
			continue
		}
		if _, ignored := stop[term]; !ignored {
			result = append(result, term)
		}
	}
	return uniqueStrings(result)
}

func likelyOfficialResearchURL(rawURL string, terms []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || len(terms) == 0 {
		return false
	}
	hostLabels := strings.FieldsFunc(strings.ToLower(parsed.Hostname()), func(value rune) bool {
		return value == '.' || value == '-'
	})
	for _, term := range terms {
		if containsString(hostLabels, term) {
			return true
		}
	}
	if strings.EqualFold(parsed.Hostname(), "github.com") || strings.EqualFold(parsed.Hostname(), "gitlab.com") {
		pathTerms := strings.FieldsFunc(strings.ToLower(strings.Trim(parsed.Path, "/")), func(value rune) bool {
			return value == '/' || value == '-' || value == '_'
		})
		for _, term := range terms {
			if containsString(pathTerms, term) {
				return true
			}
		}
	}
	return false
}

func researchURLQualityScore(rawURL string) float64 {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.Path)
	score := 0.0
	if strings.HasSuffix(host, ".gov") || strings.Contains(host, ".gov.") || strings.HasSuffix(host, ".edu") || strings.Contains(host, ".edu.") {
		score += 18
	}
	if strings.HasPrefix(host, "docs.") || strings.Contains(path, "/docs/") || strings.Contains(path, "/documentation/") {
		score += 10
	}
	for _, section := range []string{"/apis/", "/design/", "/implementation/", "/reference/"} {
		if strings.Contains(path, section) {
			score += 8
			break
		}
	}
	score += researchVersionPathScore(rawURL)
	if host == "github.com" || host == "gitlab.com" {
		score += 6
	}
	return score
}

func researchVersionPathScore(rawURL string) float64 {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 || !researchTokenIsNumeric(parts[0]) {
		return 0
	}
	version := parts[0]
	if len(version) == 2 {
		major := int(version[0] - '0')
		minor := int(version[1] - '0')
		return float64(major) + float64(minor)/10
	}
	if version[0] == '0' && len(version) >= 3 {
		trimmed := strings.TrimLeft(version, "0")
		value, _ := strconv.Atoi(trimmed)
		return float64(value) / 10
	}
	return 0
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
	score := float64(matches) * 5
	titleTokens := make(map[string]struct{})
	for _, term := range officialEntityWord.FindAllString(title, 32) {
		titleTokens[strings.Trim(term, "._-")] = struct{}{}
	}
	for _, term := range precisionResearchTerms(query) {
		if _, ok := titleTokens[strings.ToLower(term)]; ok {
			score += 15
		}
	}
	return score
}

func (provider *tavilySearchProvider) Search(ctx context.Context, input researchSearchRequest) ([]researchSearchResult, error) {
	depth := "basic"
	switch input.Depth {
	case "quick":
		depth = "fast"
	case "deep":
		depth = "advanced"
	}
	payload := map[string]any{
		"query": input.Query, "search_depth": depth, "max_results": min(max(input.Limit, 1), 20),
		"topic": "general", "include_answer": false, "include_raw_content": false,
		"include_images": false,
	}
	if input.Freshness != "" && input.Freshness != "any" {
		payload["time_range"] = input.Freshness
	}
	if len(input.Domains) > 0 {
		payload["include_domains"] = input.Domains
		payload["include_domains_mode"] = "filter"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(provider.endpoint)
	if endpoint == "" {
		endpoint = "https://api.tavily.com/search"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+provider.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	encoded, err := readBounded(response.Body, maxResearchSearchBytes)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &researchHTTPError{StatusCode: response.StatusCode, RetryAfter: response.Header.Get("Retry-After"), Detail: compactWhitespace(string(encoded))}
	}
	var payloadResponse struct {
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(encoded, &payloadResponse); err != nil {
		return nil, fmt.Errorf("Tavily 返回无效 JSON: %w", err)
	}
	results := make([]researchSearchResult, 0, len(payloadResponse.Results))
	for _, value := range payloadResponse.Results {
		target := canonicalResearchURL(value.URL)
		if target == "" {
			continue
		}
		results = append(results, researchSearchResult{
			Title: compactWhitespace(value.Title), URL: target, Snippet: compactWhitespace(value.Content),
			Score: value.Score * 50, Providers: []string{provider.Name()},
		})
	}
	return results, nil
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
	for _, marker := range []string{"anomaly-modal", "challenge-form", "cf-chl-", "bots use duckduckgo", "unusual traffic", "verify you are human", "verifying your connection", "security verification", "captcha"} {
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
