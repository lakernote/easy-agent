package tools

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubSearchProvider struct {
	name    string
	results []researchSearchResult
	err     error
	calls   *atomic.Int64
}

func (provider stubSearchProvider) Name() string { return provider.name }
func (provider stubSearchProvider) Search(context.Context, researchSearchRequest) ([]researchSearchResult, error) {
	if provider.calls != nil {
		provider.calls.Add(1)
	}
	result := append([]researchSearchResult(nil), provider.results...)
	for index := range result {
		result[index].Providers = []string{provider.name}
	}
	return result, provider.err
}

type stubResearchAdapter struct {
	name    string
	applies bool
	sources []researchSource
	err     error
}

func (adapter stubResearchAdapter) Name() string                      { return adapter.name }
func (adapter stubResearchAdapter) Applicable(researchArguments) bool { return adapter.applies }
func (adapter stubResearchAdapter) Research(context.Context, researchArguments) ([]researchSource, error) {
	return append([]researchSource(nil), adapter.sources...), adapter.err
}

type stubResearchFetcher struct {
	mu      sync.Mutex
	sources map[string]researchSource
	calls   []string
}

func (fetcher *stubResearchFetcher) Fetch(_ context.Context, result researchSearchResult, _ string, _ int) researchSource {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	fetcher.calls = append(fetcher.calls, result.URL)
	source := fetcher.sources[result.URL]
	if source.URL == "" {
		source.URL = result.URL
	}
	if source.Title == "" {
		source.Title = result.Title
	}
	return source
}

type staticResearchResolver struct {
	addresses []net.IPAddr
	err       error
}

type researchRoundTripFunc func(*http.Request) (*http.Response, error)

func (function researchRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func researchJSONClient(handler func(*http.Request) (int, string)) *http.Client {
	return &http.Client{Transport: researchRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := handler(request)
		return &http.Response{
			StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}
}

func (resolver staticResearchResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return resolver.addresses, resolver.err
}

func TestDuckDuckGoFreshnessUsesProviderCodes(t *testing.T) {
	for input, expected := range map[string]string{"day": "d", "week": "w", "month": "m", "year": "y", "any": ""} {
		if actual := duckDuckGoFreshness(input); actual != expected {
			t.Fatalf("freshness %s: got=%q want=%q", input, actual, expected)
		}
	}
}

func TestResearchQueriesAndRankingActuallyPreferDomains(t *testing.T) {
	queries := planResearchQueries("Kafka 原理", "normal", "any", []string{"kafka.apache.org"})
	if len(queries) < 2 || queries[0] != "site:kafka.apache.org Kafka 原理" {
		t.Fatalf("优先域名查询顺序错误: %#v", queries)
	}
	ranked := rankResearchCandidates([]researchSearchResult{
		{Title: "Kafka overview", URL: "https://example.com/kafka", Score: 100},
		{Title: "Kafka design", URL: "https://kafka.apache.org/documentation/", Score: 10},
	}, "Kafka 原理", []string{"kafka.apache.org"})
	if len(ranked) != 1 || researchDomain(ranked[0].URL) != "kafka.apache.org" {
		t.Fatalf("域名硬白名单没有生效: %+v", ranked)
	}
}

func TestResearchCandidatesPreferDomainDiversityBeforeRepeats(t *testing.T) {
	values := diversifyResearchCandidates([]researchSearchResult{
		{URL: "https://docs.example.com/one", Rank: 1},
		{URL: "https://www.example.com/two", Rank: 2},
		{URL: "https://second.test/three", Rank: 3},
		{URL: "https://third.test/four", Rank: 4},
	})
	if len(values) != 4 || researchDomain(values[0].URL) != "docs.example.com" ||
		researchDomain(values[1].URL) != "second.test" || researchDomain(values[2].URL) != "third.test" ||
		researchDomain(values[3].URL) != "www.example.com" {
		t.Fatalf("候选没有优先覆盖不同网站域名: %+v", values)
	}
}

func TestDomainFallbackCandidatesStayInsideHardAllowlist(t *testing.T) {
	values := domainFallbackCandidates([]string{"kafka.apache.org"})
	if len(values) != 3 || values[1].URL != "https://kafka.apache.org/documentation/" {
		t.Fatalf("官网文档降级候选错误: %+v", values)
	}
	for _, value := range values {
		if !researchDomainAllowed(researchDomain(value.URL), []string{"kafka.apache.org"}) ||
			!containsString(value.Providers, "domain_fallback") {
			t.Fatalf("官网降级候选越出白名单或缺少 provider 标记: %+v", value)
		}
	}
}

func TestParseResearchSitemapFiltersDomainsAndPrefersCurrentDocs(t *testing.T) {
	values, err := parseResearchSitemap([]byte(`<?xml version="1.0"?><urlset>
		<url><loc>https://kafka.apache.org/11/implementation/distribution/</loc></url>
		<url><loc>https://kafka.apache.org/43/implementation/distribution/</loc></url>
		<url><loc>https://outside.example/kafka</loc></url>
		</urlset>`), []string{"kafka.apache.org"}, "https://kafka.apache.org/sitemap.xml")
	if err != nil || len(values) != 2 {
		t.Fatalf("sitemap 解析或白名单过滤错误: values=%+v err=%v", values, err)
	}
	ranked := rankResearchCandidates(values, "Kafka replication distribution", []string{"kafka.apache.org"})
	if len(ranked) != 2 || !strings.Contains(ranked[0].URL, "/43/") {
		t.Fatalf("sitemap 没有优先当前文档版本: %+v", ranked)
	}
}

func TestResearchQueriesPreserveExactEntitySpelling(t *testing.T) {
	queries := planResearchQueries("Laker 是谁", "normal", "any", nil)
	if len(queries) < 4 || queries[0] != `"Laker" 是谁` || queries[1] != `"Laker" meaning` ||
		queries[2] != `"Laker" biography` || queries[3] != "Laker 是谁" {
		t.Fatalf("短实体没有优先生成精确匹配查询: %#v", queries)
	}
	domainQueries := planResearchQueries("OpenAI Codex updates", "normal", "official", []string{"openai.com"})
	if len(domainQueries) < 3 || domainQueries[0] != "site:openai.com OpenAI Codex updates" ||
		domainQueries[1] != `site:openai.com "OpenAI" "Codex" updates` {
		t.Fatalf("站点限定查询没有保留精确实体拼写: %#v", domainQueries)
	}
}

func TestModelPlannedSubqueriesAreBudgetedAndPreserveOrder(t *testing.T) {
	arguments := researchArguments{
		Query: "Kafka architecture explained", Depth: "normal", SourceScope: "any",
		Subqueries: []string{"Kafka replication quorum", "Kafka consumer group offsets", "Kafka storage log segments"},
	}
	queries := plannedResearchQueries(arguments)
	if len(queries) != researchQueryBudget("normal") || queries[0] != "Kafka replication quorum" || queries[1] != "Kafka consumer group offsets" {
		t.Fatalf("模型子查询没有优先执行或预算错误: %#v", queries)
	}
	deep := plannedResearchQueries(researchArguments{
		Query: "Kafka architecture", Depth: "deep", Domains: []string{"kafka.apache.org"},
		Subqueries: []string{"Kafka replication", "Kafka consumer offsets"},
	})
	if len(deep) > researchQueryBudget("deep") || deep[0] != "site:kafka.apache.org Kafka replication" {
		t.Fatalf("限定域名的模型子查询错误: %#v", deep)
	}
}

func TestSearchResultMergesDiscoveryQueryProvenance(t *testing.T) {
	values := rankResearchCandidates([]researchSearchResult{
		{Title: "Kafka docs", URL: "https://kafka.apache.org/documentation/", Queries: []string{"Kafka replication"}},
		{Title: "Kafka documentation", URL: "https://kafka.apache.org/documentation/?utm_source=test", Queries: []string{"Kafka consumer offsets"}},
	}, "Kafka architecture", nil)
	if len(values) != 1 || len(values[0].Queries) != 2 {
		t.Fatalf("同一来源没有合并检索溯源: %+v", values)
	}
}

func TestResearchSourcesDeduplicateNearIdenticalVersionedPages(t *testing.T) {
	common := "Apache Kafka documentation explains partition replication leader follower consumer group offsets broker controller producer records topic storage protocol configuration operations security monitoring"
	sources, duplicates := deduplicateResearchSourcesWithStats([]researchSource{
		{Kind: "web_page", URL: "https://kafka.apache.org/43/operations/basic-kafka-operations.html", Content: common + " version 4.3"},
		{Kind: "web_page", URL: "https://kafka.apache.org/42/operations/basic-kafka-operations.html", Content: common + " version 4.2"},
		{Kind: "web_page", URL: "https://kafka.apache.org/documentation/", Content: common + " transactions exactly once streams connect quotas design internals"},
	})
	if len(sources) != 2 || duplicates != 1 {
		t.Fatalf("版本化近重复正文去重错误: sources=%+v duplicates=%d", sources, duplicates)
	}
}

func TestResearchSourcesDeduplicateVersionedDocumentPaths(t *testing.T) {
	sources, duplicates := deduplicateResearchSourcesWithStats([]researchSource{
		{Kind: "web_page", URL: "https://kafka.apache.org/11/operations/basic-kafka-operations/", Content: "Very old and substantially different Kafka operations documentation."},
		{Kind: "web_page", URL: "https://kafka.apache.org/43/operations/basic-kafka-operations/", Content: "Current Kafka operations documentation with enough useful details."},
		{Kind: "web_page", URL: "https://example.com/2026/news/story", Content: "A dated news article must not be mistaken for versioned documentation."},
		{Kind: "web_page", URL: "https://example.com/2025/news/story", Content: "Another dated news article must remain independently available."},
	})
	if len(sources) != 3 || duplicates != 1 || sources[0].URL != "https://kafka.apache.org/43/operations/basic-kafka-operations/" {
		t.Fatalf("版本路径去重错误: sources=%+v duplicates=%d", sources, duplicates)
	}
}

func TestCompactDocumentationVersionsPreferCurrentRelease(t *testing.T) {
	if researchVersionPathScore("https://kafka.apache.org/0110/implementation/distribution/") >= researchVersionPathScore("https://kafka.apache.org/43/implementation/distribution/") {
		t.Fatal("紧凑旧版本 0110 不应排在 Kafka 4.3 之前")
	}
	sources, duplicates := deduplicateResearchSourcesWithStats([]researchSource{
		{Kind: "web_page", URL: "https://kafka.apache.org/0110/implementation/distribution/", Content: "old"},
		{Kind: "web_page", URL: "https://kafka.apache.org/43/implementation/distribution/", Content: "current"},
	})
	if len(sources) != 1 || duplicates != 1 || sources[0].URL != "https://kafka.apache.org/43/implementation/distribution/" {
		t.Fatalf("紧凑文档版本去重没有保留当前版本: sources=%+v duplicates=%d", sources, duplicates)
	}
}

func TestSitemapDiscoveryKeepsQueryProvenance(t *testing.T) {
	endpoint := "https://example.com/sitemap.xml"
	results, err := parseResearchSitemap([]byte(`<urlset><url><loc>https://example.com/docs/</loc></url></urlset>`), []string{"example.com"}, endpoint)
	if err != nil || len(results) != 1 {
		t.Fatalf("sitemap 解析失败: results=%+v err=%v", results, err)
	}
	if len(results[0].Queries) != 1 || results[0].Queries[0] != endpoint {
		t.Fatalf("sitemap 来源缺少发现溯源: %+v", results[0])
	}
}

func TestResearchChallengeDetectionCoversConnectionVerification(t *testing.T) {
	if !isSearchChallenge([]byte("Verifying your connection for security before proceeding")) {
		t.Fatal("常见人机验证页未被识别")
	}
}

func TestResearchTermsIncludeUsefulEnglishWordForms(t *testing.T) {
	terms := researchTerms("partition replication consumers")
	for _, expected := range []string{"partition", "replication", "replica", "consumers", "consumer"} {
		if !containsString(terms, expected) {
			t.Fatalf("缺少英文相关词形 %q: %#v", expected, terms)
		}
	}
}

func TestOfficialScopeKeepsLikelyEntityDomainsAndOfficialRepositories(t *testing.T) {
	values := []researchSearchResult{
		{Title: "Apache Kafka", URL: "https://kafka.apache.org/documentation/"},
		{Title: "Kafka source", URL: "https://github.com/apache/kafka/blob/trunk/README.md"},
		{Title: "Kafka guide", URL: "https://docs.confluent.io/kafka/design/consumer-design.html"},
	}
	ranked := rankResearchCandidates(values, "Apache Kafka official documentation", nil)
	filtered := filterResearchCandidates(ranked, researchArguments{Query: "Apache Kafka official documentation", SourceScope: "official"})
	if len(filtered) != 2 || researchDomain(filtered[0].URL) != "kafka.apache.org" || researchDomain(filtered[1].URL) != "github.com" {
		t.Fatalf("官方来源保守筛选错误: %+v", filtered)
	}
}

func TestOfficialScopeAlsoFiltersStructuredAdapters(t *testing.T) {
	var searchCalls atomic.Int64
	engine := &researchEngine{
		providers: []researchSearchProvider{stubSearchProvider{
			name: "official_search", calls: &searchCalls,
			results: []researchSearchResult{{Title: "Cisco investor relations", URL: "https://investor.cisco.com/stock-information/stock-quote"}},
		}},
		adapters: []researchAdapter{stubResearchAdapter{name: "finance", applies: true, sources: []researchSource{{
			Title: "CSCO market quote", URL: "https://finance.yahoo.com/quote/CSCO", Provider: "yahoo_finance",
			Kind: "market_quote", RetrievedAt: "2026-09-08T00:00:00Z", Content: "third-party quote",
		}}}},
		fetcher: &stubResearchFetcher{sources: map[string]researchSource{
			"https://investor.cisco.com/stock-information/stock-quote": {
				Title: "Cisco investor relations", URL: "https://investor.cisco.com/stock-information/stock-quote",
				Provider: "official_search", Kind: "web_page", RetrievedAt: "2026-09-08T00:00:00Z", Content: "official stock information",
			},
		}},
		now: time.Now,
	}
	output, err := engine.Run(context.Background(), researchArguments{
		Query: "Cisco official stock information", Depth: "normal", Freshness: "day", MaxSources: 2, SourceScope: "official",
	})
	if err != nil {
		t.Fatal(err)
	}
	if searchCalls.Load() == 0 || strings.Contains(output, "finance.yahoo.com") || !strings.Contains(output, "investor.cisco.com") {
		t.Fatalf("official scope 没有约束结构化来源: calls=%d output=%s", searchCalls.Load(), output)
	}
}

func TestOfficialScopeKeepsNormalizedRepositoryName(t *testing.T) {
	values := []researchSearchResult{{Title: "Easy Postman", URL: "https://github.com/lakernote/easy-postman"}}
	ranked := rankResearchCandidates(values, "EasyPostman official GitHub repository", nil)
	filtered := filterResearchCandidates(ranked, researchArguments{Query: "EasyPostman official GitHub repository", SourceScope: "official"})
	if len(filtered) != 1 {
		t.Fatalf("连字符仓库名应匹配精确实体拼写: %+v", filtered)
	}
}

func TestConfiguredSearchProviderAvoidsHTMLFallbackWhenItHasEnoughCandidates(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int64
	results := []researchSearchResult{
		{Title: "one", URL: "https://one.example/doc"},
		{Title: "two", URL: "https://two.example/doc"},
		{Title: "three", URL: "https://three.example/doc"},
		{Title: "four", URL: "https://four.example/doc"},
	}
	engine := &researchEngine{providers: []researchSearchProvider{
		stubSearchProvider{name: "tavily", results: results, calls: &primaryCalls},
		stubSearchProvider{name: "duckduckgo_html", results: results, calls: &fallbackCalls},
	}}
	ranked, _, err := engine.search(context.Background(), researchArguments{Query: "test", Depth: "quick", SourceScope: "any", Freshness: "any", MaxSources: 2})
	if err != nil || len(ranked) < 4 || primaryCalls.Load() == 0 || fallbackCalls.Load() != 0 {
		t.Fatalf("配置型 provider 分层错误: primary=%d fallback=%d results=%d err=%v", primaryCalls.Load(), fallbackCalls.Load(), len(ranked), err)
	}
}

func TestTavilyProviderUsesStrictDomainsAndFreshness(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Tavily 请求方法或认证错误: %s %q", request.Method, request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["time_range"] != "week" || body["include_domains_mode"] != "filter" || body["search_depth"] != "advanced" {
			t.Fatalf("Tavily 参数错误: %#v", body)
		}
		return http.StatusOK, `{"results":[{"title":"Official update","url":"https://openai.com/news/","content":"release notes","score":0.9}]}`
	})
	provider := &tavilySearchProvider{client: client, key: "test-key", endpoint: "https://tavily.test/search"}
	results, err := provider.Search(context.Background(), researchSearchRequest{
		Query: "OpenAI Codex updates", Depth: "deep", Freshness: "week", Limit: 5, Domains: []string{"openai.com"},
	})
	if err != nil || len(results) != 1 || results[0].Providers[0] != "tavily" || results[0].Score <= 0 {
		t.Fatalf("Tavily 结果错误: results=%+v err=%v", results, err)
	}
}

func TestFirecrawlProviderUsesSearchV2Contract(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		if request.Method != http.MethodPost || request.URL.String() != "https://firecrawl.test/v2/search" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Firecrawl 请求错误: %s %s %q", request.Method, request.URL, request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		domains, _ := body["includeDomains"].([]any)
		if body["tbs"] != "qdr:w" || body["limit"] != float64(4) || len(domains) != 1 || domains[0] != "kafka.apache.org" {
			t.Fatalf("Firecrawl 查询参数错误: %#v", body)
		}
		return http.StatusOK, `{"success":true,"data":{"web":[{"title":"Kafka Design","description":"Official architecture","url":"https://kafka.apache.org/documentation/"}]}}`
	})
	provider := &firecrawlSearchProvider{client: client, key: "test-key", endpoint: firecrawlAPIEndpoint("https://firecrawl.test/v2", "search")}
	results, err := provider.Search(context.Background(), researchSearchRequest{
		Query: "Kafka architecture", Freshness: "week", Limit: 4, Domains: []string{"kafka.apache.org"},
	})
	if err != nil || len(results) != 1 || results[0].Providers[0] != "firecrawl" {
		t.Fatalf("Firecrawl 搜索结果错误: results=%+v err=%v", results, err)
	}
}

func TestBingParserReadsResultBlocks(t *testing.T) {
	body := `<ol><li class="b_algo"><h2><a href="https://example.com/kafka">Kafka Design</a></h2><div class="b_caption"><p>Official design documentation.</p></div></li></ol>`
	results := parseBingResults(body, 3)
	if len(results) != 1 || results[0].URL != "https://example.com/kafka" || results[0].Snippet != "Official design documentation." {
		t.Fatalf("Bing 解析错误: %+v", results)
	}
}

func TestSearchChallengeIsNotReportedAsEmptyResults(t *testing.T) {
	for _, body := range []string{"<form id='challenge-form'></form>", "Verify you are human", "CAPTCHA required"} {
		if !isSearchChallenge([]byte(body)) {
			t.Fatalf("没有识别验证页: %q", body)
		}
	}
	if isSearchChallenge([]byte(strings.Repeat("ordinary article content ", 1_100) + "captcha is discussed only near the end")) {
		t.Fatal("正文后部讨论 CAPTCHA 不应被误判为验证页")
	}
}

func TestFetchCandidatesBackfillsFailedTopResults(t *testing.T) {
	candidates := make([]researchSearchResult, 0, 6)
	sources := make(map[string]researchSource)
	for index := 1; index <= 6; index++ {
		target := "https://example.com/" + string(rune('0'+index))
		candidates = append(candidates, researchSearchResult{Title: target, URL: target})
		sources[target] = researchSource{URL: target, Content: "可用来源正文"}
	}
	for _, index := range []int{1, 2, 3, 4} {
		target := "https://example.com/" + string(rune('0'+index))
		sources[target] = researchSource{URL: target, Error: "读取失败"}
	}
	fetcher := &stubResearchFetcher{sources: sources}
	engine := &researchEngine{fetcher: fetcher}
	usable, failed := engine.fetchCandidates(context.Background(), candidates, "问题", 2, 4_000, nil)
	if len(usable) != 2 || len(failed) != 4 {
		t.Fatalf("候选补位错误: usable=%d failed=%d calls=%v", len(usable), len(failed), fetcher.calls)
	}
	if usable[0].URL != "https://example.com/5" || usable[1].URL != "https://example.com/6" {
		t.Fatalf("没有读取后续候选: %+v", usable)
	}
}

func TestResearchEngineReturnsStructuredAndWebEvidenceWithCitations(t *testing.T) {
	structured := researchSource{
		Title: "Open-Meteo", URL: "https://api.open-meteo.com/v1/forecast", Provider: "open_meteo",
		Kind: "weather_forecast", RetrievedAt: "2026-09-08T00:00:00Z", Content: "合肥：晴，28°C",
	}
	webURL := "https://example.com/hefei-weather"
	fetcher := &stubResearchFetcher{sources: map[string]researchSource{
		webURL: {Title: "天气说明", URL: webURL, Provider: "test", Kind: "web_page", RetrievedAt: "2026-09-08T00:00:00Z", Content: "合肥未来一周天气说明"},
	}}
	engine := &researchEngine{
		providers: []researchSearchProvider{stubSearchProvider{name: "test", results: []researchSearchResult{{Title: "天气说明", URL: webURL}}}},
		adapters:  []researchAdapter{stubResearchAdapter{name: "weather", applies: true, sources: []researchSource{structured}}},
		fetcher:   fetcher,
		now:       func() time.Time { return time.Date(2026, 9, 8, 1, 2, 3, 0, time.UTC) },
	}
	output, err := engine.Run(context.Background(), researchArguments{Query: "合肥未来一周天气", Depth: "deep", Freshness: "day", MaxSources: 3})
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		EvidenceStatus         string           `json:"evidence_status"`
		IndependentDomainCount int              `json:"independent_domain_count"`
		SourceCount            int              `json:"source_count"`
		Sources                []researchSource `json:"sources"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceCount != 2 || report.IndependentDomainCount != 2 || report.EvidenceStatus != "multiple_independent_sources_retrieved" ||
		len(report.Sources) != 2 || report.Sources[0].ID != "S1" || !strings.Contains(report.Sources[0].Citation, "https://") {
		t.Fatalf("研究证据包错误: %s", output)
	}
}

func TestEvidenceStatusDistinguishesIndependentWebsites(t *testing.T) {
	sameWebsite := []researchSource{
		{URL: "https://weather.example.co.uk/today"},
		{URL: "https://api.example.co.uk/forecast"},
	}
	if status := evidenceStatus(sameWebsite); status != "multiple_sources_same_domain" {
		t.Fatalf("同站页面不应算独立证据: %s", status)
	}
	independent := append(sameWebsite, researchSource{URL: "https://example.org/weather"})
	if status := evidenceStatus(independent); status != "multiple_independent_sources_retrieved" || independentResearchDomainCount(independent) != 2 {
		t.Fatalf("独立网站识别错误: status=%s domains=%d", status, independentResearchDomainCount(independent))
	}
}

func TestNormalStructuredFactDoesNotWaitForGeneralSearch(t *testing.T) {
	var searchCalls atomic.Int64
	engine := &researchEngine{
		providers: []researchSearchProvider{stubSearchProvider{
			name: "should_not_run", calls: &searchCalls,
			results: []researchSearchResult{{Title: "irrelevant", URL: "https://example.com/irrelevant"}},
		}},
		adapters: []researchAdapter{stubResearchAdapter{name: "structured", applies: true, sources: []researchSource{{
			Title: "exact fact", URL: "https://api.example.com/fact", Provider: "structured", Kind: "structured_fact",
			RetrievedAt: "2026-09-08T00:00:00Z", Content: "verified structured value",
		}}}},
		fetcher: &stubResearchFetcher{sources: map[string]researchSource{}},
		now:     time.Now,
	}
	output, err := engine.Run(context.Background(), researchArguments{Query: "实时事实", Depth: "normal", Freshness: "day", MaxSources: 3})
	if err != nil {
		t.Fatal(err)
	}
	if searchCalls.Load() != 0 || !strings.Contains(output, `"kind": "structured_fact"`) {
		t.Fatalf("normal 结构化事实不应等待通用搜索: calls=%d output=%s", searchCalls.Load(), output)
	}
}

func TestHTMLExtractionDropsNavigationAndSelectsRelevantPassages(t *testing.T) {
	page := `<html><head><meta content="2026-09-08" property="article:published_time"><title>Kafka Architecture</title></head><body><nav>导航内容导航内容导航内容</nav><article><p>这是一段很长但无关的产品介绍内容，用来验证相关性排序不会只保留页面开头。</p><p>Kafka 使用分区日志保存消息，消费者按 offset 读取分区中的记录，这是核心工作原理。</p><footer>页脚内容页脚内容页脚内容</footer></article></body></html>`
	document, err := extractResearchHTML([]byte(page), "text/html; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := selectRelevantPassages("Kafka 分区 offset 原理", document.Paragraphs, 120)
	if document.Title != "Kafka Architecture" || document.PublishedAt != "2026-09-08" ||
		!strings.Contains(content, "offset") || strings.Contains(content, "导航内容") || strings.Contains(content, "页脚内容") {
		t.Fatalf("正文提取或相关性选择错误: doc=%+v content=%q", document, content)
	}
}

func TestSafeResearchDialRejectsResolvedPrivateAddress(t *testing.T) {
	dial := safeResearchDialContext(staticResearchResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}, &net.Dialer{})
	_, err := dial(context.Background(), "tcp", "public.example:443")
	if err == nil || !strings.Contains(err.Error(), "不安全 IP") {
		t.Fatalf("DNS 私网映射没有被拒绝: %v", err)
	}
}

func TestStructuredIntentParsing(t *testing.T) {
	if location := weatherLocation("查询合肥未来一周天气"); location != "合肥" {
		t.Fatalf("天气地点解析错误: %q", location)
	}
	candidates := weatherLocationCandidates("合肥 今天 天气 温度 降雨概率 出行建议")
	if len(candidates) == 0 || candidates[0] != "合肥" {
		t.Fatalf("小模型扩写后的天气地点解析错误: %#v", candidates)
	}
	englishCandidates := weatherLocationCandidates("weather in New York tomorrow")
	if len(englishCandidates) == 0 || englishCandidates[0] != "new york" {
		t.Fatalf("英文天气地点解析错误: %#v", englishCandidates)
	}
	questionCandidates := weatherLocationCandidates("What is the weather in New York today?")
	if len(questionCandidates) == 0 || questionCandidates[0] != "new york" {
		t.Fatalf("英文问句天气地点解析错误: %#v", questionCandidates)
	}
	if days := weatherForecastDays("合肥未来10天天气"); days != 10 {
		t.Fatalf("天气天数解析错误: %d", days)
	}
	if days := weatherForecastDays("合肥今天天气"); days != 2 {
		t.Fatalf("今天查询应弹性覆盖明天: %d", days)
	}
	owner, repo, _ := githubRepositoryIdentity("https://github.com/lakernote/easy-postman 有多少 stars")
	if owner != "lakernote" || repo != "easy-postman" {
		t.Fatalf("GitHub 仓库解析错误: %s/%s", owner, repo)
	}
	lookup, ticker := financeIdentity("CSCO stock price")
	if lookup != "CSCO" || ticker != "CSCO" {
		t.Fatalf("股票代码解析错误: lookup=%q ticker=%q", lookup, ticker)
	}
}

func TestEntityAdapterProvidesAmbiguousCandidatesWithoutSuppressingSearch(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		if request.URL.Query().Get("search") != "Laker" || request.URL.Query().Get("language") != "en" {
			t.Fatalf("实体消歧请求错误: %s", request.URL.String())
		}
		return http.StatusOK, `{"search":[{"id":"Q37007996","label":"Laker","description":"family name","concepturi":"https://www.wikidata.org/entity/Q37007996","match":{"type":"label","language":"en","text":"Laker"}},{"id":"Q121783","label":"Los Angeles Lakers","description":"American professional basketball team","concepturi":"https://www.wikidata.org/entity/Q121783","match":{"type":"alias","language":"en","text":"Lakers"}}]}`
	})
	adapter := &entityResearchAdapter{client: client, wikidataURL: "https://www.wikidata.org/w/api.php"}
	if !adapter.Applicable(researchArguments{Query: "Laker 是谁？请检索并区分含义", DataType: "auto"}) || adapter.Applicable(researchArguments{Query: "Laker 最新新闻", DataType: "auto"}) {
		t.Fatal("实体问题识别错误")
	}
	sources, err := adapter.Research(context.Background(), researchArguments{Query: "Laker 是谁？请检索并区分含义", DataType: "auto"})
	if err != nil || len(sources) != 1 || sources[0].Kind != "entity_candidates" ||
		!strings.Contains(sources[0].Content, "Los Angeles Lakers") || structuredSourcesCanFinish(sources) {
		t.Fatalf("实体候选结果错误: sources=%+v err=%v", sources, err)
	}
}

func TestWeatherAdapterUsesStructuredProvider(t *testing.T) {
	geoQuery := ""
	forecastDays := ""
	client := researchJSONClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/geo":
			geoQuery = request.URL.Query().Get("name")
			return http.StatusOK, `{"results":[{"id":1,"name":"合肥","admin1":"安徽","country":"中国","timezone":"Asia/Shanghai","latitude":31.86,"longitude":117.28}]}`
		case "/forecast":
			forecastDays = request.URL.Query().Get("forecast_days")
			return http.StatusOK, `{"timezone":"Asia/Shanghai","current":{"time":"2026-09-08T10:00","temperature_2m":28,"apparent_temperature":29,"relative_humidity_2m":55,"weather_code":0,"wind_speed_10m":8},"daily":{"time":["2026-09-08","2026-09-09"],"weather_code":[0,61],"temperature_2m_max":[31,29],"temperature_2m_min":[22,21],"precipitation_probability_max":[10,70],"sunrise":["06:00","06:01"],"sunset":["18:20","18:19"]}}`
		default:
			return http.StatusNotFound, `{}`
		}
	})
	adapter := &weatherResearchAdapter{client: client, geocodingURL: "https://weather.test/geo", forecastURL: "https://weather.test/forecast"}
	sources, err := adapter.Research(context.Background(), researchArguments{Query: "合肥 今天 天气 温度 降雨概率 出行建议", DataType: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if geoQuery != "合肥" || forecastDays != "2" || len(sources) != 1 || sources[0].Kind != "weather_forecast" ||
		!strings.Contains(sources[0].Content, "2026-09-09") || !strings.Contains(sources[0].Content, "precipitation_probability_percent") ||
		!strings.Contains(sources[0].Content, "travel_advice") || !strings.Contains(sources[0].Content, "外出携带雨具") {
		t.Fatalf("天气结构化来源错误: %+v", sources)
	}
}

func TestWeatherAdapterFallsBackAcrossLocationCandidates(t *testing.T) {
	geoQueries := make([]string, 0, 2)
	client := researchJSONClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/geo":
			location := request.URL.Query().Get("name")
			geoQueries = append(geoQueries, location)
			if location != "合肥" {
				return http.StatusOK, `{"results":[]}`
			}
			return http.StatusOK, `{"results":[{"id":1,"name":"合肥","admin1":"安徽","country":"中国","timezone":"Asia/Shanghai","latitude":31.86,"longitude":117.28}]}`
		case "/forecast":
			return http.StatusOK, `{"timezone":"Asia/Shanghai","current":{"time":"2026-09-08T10:00","temperature_2m":28,"apparent_temperature":29,"relative_humidity_2m":55,"weather_code":0,"wind_speed_10m":8},"daily":{"time":["2026-09-08","2026-09-09"],"weather_code":[0,61],"temperature_2m_max":[31,29],"temperature_2m_min":[22,21],"precipitation_probability_max":[10,70]}}`
		default:
			return http.StatusNotFound, `{}`
		}
	})
	adapter := &weatherResearchAdapter{client: client, geocodingURL: "https://weather.test/geo", forecastURL: "https://weather.test/forecast"}
	sources, err := adapter.Research(context.Background(), researchArguments{Query: "安徽 合肥 今天 天气 温度和降雨概率", DataType: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(geoQueries) != 2 || geoQueries[0] != "安徽 合肥" || geoQueries[1] != "合肥" || len(sources) != 1 {
		t.Fatalf("天气地点候选降级错误: queries=%#v sources=%+v", geoQueries, sources)
	}
}

func TestGitHubAdapterReturnsExactRepositoryMetrics(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		if request.URL.Path != "/repos/lakernote/easy-postman" {
			return http.StatusNotFound, `{}`
		}
		return http.StatusOK, `{"full_name":"lakernote/easy-postman","html_url":"https://github.com/lakernote/easy-postman","description":"API client","stargazers_count":702,"forks_count":12,"open_issues_count":3,"subscribers_count":7,"default_branch":"main","language":"Java","visibility":"public","created_at":"2024-01-01T00:00:00Z","updated_at":"2026-09-08T00:00:00Z"}`
	})
	adapter := &githubResearchAdapter{client: client, apiBase: "https://api.github.test"}
	sources, err := adapter.Research(context.Background(), researchArguments{Query: "github.com/lakernote/easy-postman stars", DataType: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || !strings.Contains(sources[0].Content, `"stars": 702`) {
		t.Fatalf("GitHub 精确指标错误: %+v", sources)
	}
}

func TestGitHubAdapterFallsBackToOfficialWebPagesWhenAPIRateLimited(t *testing.T) {
	apiSearchCalls := 0
	client := researchJSONClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/search/repositories":
			apiSearchCalls++
			return http.StatusForbidden, `{"message":"API rate limit exceeded"}`
		case "/repos/lakernote/easy-postman":
			return http.StatusForbidden, `{"message":"API rate limit exceeded"}`
		case "/search":
			return http.StatusOK, `<html><body><a href="/other/unrelated">other</a><a href="/lakernote/easy-postman">lakernote/<em>easy-postman</em></a></body></html>`
		case "/lakernote/easy-postman":
			return http.StatusOK, `<html><head><meta name="octolytics-dimension-repository_nwo" content="lakernote/easy-postman"><meta property="og:description" content="API client - lakernote/easy-postman"></head><body><span id="repo-stars-counter-star" title="713">713</span><span id="repo-network-counter" title="60">60</span><span id="issues-repo-tab-count" title="3">3</span><span itemprop="programmingLanguage">Java</span><script>{"createdAt":"2024-01-02T03:04:05Z"}</script></body></html>`
		default:
			return http.StatusNotFound, `{"error":"unexpected test request"}`
		}
	})
	adapter := &githubResearchAdapter{
		client: client, apiBase: "https://api.github.test", webBase: "https://github.test",
	}
	sources, err := adapter.Research(context.Background(), researchArguments{Query: "EasyPostman 的 GitHub star 多少", DataType: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if apiSearchCalls != 1 || len(sources) != 1 || sources[0].Provider != "github_web" ||
		!strings.Contains(sources[0].Content, `"stars": 713`) ||
		!strings.Contains(sources[0].Content, `"forks": 60`) ||
		!strings.Contains(sources[0].Content, `"open_issues": 3`) ||
		!strings.Contains(sources[0].Content, `"updated_at"`) {
		t.Fatalf("GitHub 官方网页降级错误: apiSearchCalls=%d sources=%+v", apiSearchCalls, sources)
	}
}

func TestFinanceAdapterResolvesCompanyAndReturnsTimestampedQuote(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/search":
			return http.StatusOK, "{\"quotes\":[{\"symbol\":\"CSCO\",\"shortname\":\"Cisco Systems\",\"longname\":\"Cisco Systems, Inc.\",\"exchange\":\"NMS\",\"quoteType\":\"EQUITY\"}]}"
		case "/chart/CSCO":
			return http.StatusOK, "{\"chart\":{\"result\":[{\"meta\":{\"symbol\":\"CSCO\",\"currency\":\"USD\",\"exchangeName\":\"NMS\",\"fullExchangeName\":\"NasdaqGS\",\"instrumentType\":\"EQUITY\",\"exchangeTimezoneName\":\"America/New_York\",\"regularMarketPrice\":70.5,\"previousClose\":69.5,\"regularMarketTime\":1788796800}}],\"error\":null}}"
		default:
			return http.StatusNotFound, "{}"
		}
	})
	adapter := &financeResearchAdapter{
		client: client, searchURL: "https://finance.test/search",
		chartBase: "https://finance.test/chart", quoteBase: "https://finance.example/quote",
	}
	sources, err := adapter.Research(context.Background(), researchArguments{Query: "思科的股票啊", DataType: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Kind != "market_quote" ||
		!strings.Contains(sources[0].Content, "\"symbol\": \"CSCO\"") ||
		!strings.Contains(sources[0].Content, "\"market_time_utc\"") ||
		!strings.Contains(sources[0].Content, "\"market_time_exchange_local\"") ||
		!strings.Contains(sources[0].Content, "\"exchange_name\": \"NasdaqGS\"") {
		t.Fatalf("股票结构化来源错误: %+v", sources)
	}
}

func TestFinanceAdapterResolvesTickerFromExchangeQualifier(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		switch {
		case request.URL.Path == "/wikidata" && request.URL.Query().Get("action") == "wbsearchentities":
			return http.StatusOK, `{"search":[{"id":"Q173395"}]}`
		case request.URL.Path == "/wikidata" && request.URL.Query().Get("action") == "wbgetclaims" && request.URL.Query().Get("property") == "P414":
			return http.StatusOK, `{"claims":{"P414":[{"mainsnak":{"datavalue":{"value":{"id":"Q82059"}}},"qualifiers":{"P249":[{"datavalue":{"value":"CSCO"}}]}}]}}`
		case request.URL.Path == "/chart/CSCO":
			return http.StatusOK, `{"chart":{"result":[{"meta":{"symbol":"CSCO","currency":"USD","exchangeName":"NMS","instrumentType":"EQUITY","exchangeTimezoneName":"America/New_York","regularMarketPrice":70.5,"previousClose":69.5,"regularMarketTime":1788796800}}],"error":null}}`
		default:
			return http.StatusNotFound, `{"error":"unexpected test request"}`
		}
	})
	adapter := &financeResearchAdapter{
		client: client, searchURL: "https://finance.test/search", chartBase: "https://finance.test/chart",
		quoteBase: "https://finance.example/quote", wikidataURL: "https://finance.test/wikidata",
	}
	sources, err := adapter.Research(context.Background(), researchArguments{Query: "思科的股票价格", DataType: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || !strings.Contains(sources[0].Content, `"symbol": "CSCO"`) {
		t.Fatalf("Wikidata 交易所 qualifier 没有解析成行情代码: %+v", sources)
	}
}

func TestConfiguredReaderCanExtractPDFFallback(t *testing.T) {
	client := &http.Client{Transport: researchRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.URL.String(), "https://example.com/report.pdf") {
			t.Fatalf("reader 没有收到原始来源 URL: %s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/markdown"}},
			Body:    io.NopCloser(strings.NewReader("Report title\n\nKafka partition replication and broker architecture are described in this primary report.")),
			Request: request,
		}, nil
	})}
	fetcher := &researchFetcher{readerClient: client, readerURL: "https://reader.test"}
	source, err := fetcher.fetchViaReader(context.Background(), researchSource{URL: "https://example.com/report.pdf", Provider: "search", Kind: "pdf"}, "Kafka partition", 500)
	if err != nil || source.Kind != "reader_document" || !strings.Contains(source.Content, "partition replication") {
		t.Fatalf("reader 降级错误: source=%+v err=%v", source, err)
	}
}

func TestConfiguredFirecrawlCanExtractDynamicPageFallback(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		if request.Method != http.MethodPost || request.URL.String() != "https://firecrawl.test/v2/scrape" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Firecrawl scrape 请求错误: %s %s %q", request.Method, request.URL, request.Header.Get("Authorization"))
		}
		return http.StatusOK, `{"success":true,"data":{"markdown":"# Kafka Architecture\n\nKafka partition replication and consumer offsets are described here.","metadata":{"title":"Kafka Architecture","sourceURL":"https://kafka.apache.org/documentation/","statusCode":200}}}`
	})
	fetcher := &researchFetcher{firecrawlClient: client, firecrawlURL: "https://firecrawl.test/v2/scrape", firecrawlKey: "test-key"}
	source, err := fetcher.fetchViaFirecrawl(context.Background(), researchSource{
		URL: "https://kafka.apache.org/documentation/", Provider: "search", Kind: "web_page",
	}, "Kafka replication offsets", 500)
	if err != nil || source.Kind != "reader_document" || !strings.Contains(source.Provider, "firecrawl") || !strings.Contains(source.Content, "consumer offsets") {
		t.Fatalf("Firecrawl 正文降级错误: source=%+v err=%v", source, err)
	}
}

func TestParseResearchArgumentsRejectsInvalidDomain(t *testing.T) {
	_, err := parseResearchArguments(json.RawMessage(`{"query":"test","domains":["https://example.com/path"]}`))
	if err == nil || !strings.Contains(err.Error(), "无效优先域名") {
		t.Fatalf("带路径的优先域名应被拒绝: %v", err)
	}
}

func TestParseResearchArgumentsAllowsOneSourceForExactFact(t *testing.T) {
	arguments, err := parseResearchArguments(json.RawMessage(`{"query":"lakernote/easy-agent stars","depth":"quick","max_sources":1}`))
	if err != nil || arguments.MaxSources != 1 {
		t.Fatalf("单一实时事实应允许一个来源: arguments=%+v err=%v", arguments, err)
	}
}

func TestParseResearchArgumentsKeepsModelRoutingHints(t *testing.T) {
	arguments, err := parseResearchArguments(json.RawMessage(`{"query":"请查询","data_type":"weather","subject":"安徽省合肥市","time_range_days":7}`))
	if err != nil || arguments.DataType != "weather" || arguments.Subject != "安徽省合肥市" || arguments.TimeRangeDays != 7 {
		t.Fatalf("模型路由提示未保留: arguments=%+v err=%v", arguments, err)
	}
}

func TestParseResearchArgumentsNormalizesSubqueries(t *testing.T) {
	arguments, err := parseResearchArguments(json.RawMessage(`{"query":"Kafka architecture","subqueries":[" Kafka replication ","kafka replication","Kafka consumer offsets",""]}`))
	if err != nil || len(arguments.Subqueries) != 2 || arguments.Subqueries[0] != "Kafka replication" {
		t.Fatalf("subqueries 归一化错误: arguments=%+v err=%v", arguments, err)
	}
	_, err = parseResearchArguments(json.RawMessage(`{"query":"test","subqueries":["1","2","3","4","5"]}`))
	if err == nil {
		t.Fatal("超过查询预算的 subqueries 应被拒绝")
	}
}

func TestStructuredAdaptersUseModelDataTypeBeforeLexicalFallback(t *testing.T) {
	weather := &weatherResearchAdapter{}
	github := &githubResearchAdapter{}
	finance := &financeResearchAdapter{}
	entity := &entityResearchAdapter{}
	if !weather.Applicable(researchArguments{Query: "请查询", DataType: "weather", Subject: "合肥"}) {
		t.Fatal("模型明确选择 weather 时应启用天气 adapter")
	}
	for name, adapter := range map[string]researchAdapter{"weather": weather, "github": github, "finance": finance, "entity": entity} {
		if adapter.Applicable(researchArguments{Query: "GitHub 股票天气是什么", DataType: "web"}) {
			t.Fatalf("模型选择 web 时不应由关键词触发 %s adapter", name)
		}
	}
	if !github.Applicable(researchArguments{Query: "请查询", DataType: "repository"}) ||
		!finance.Applicable(researchArguments{Query: "请查询", DataType: "market"}) ||
		!entity.Applicable(researchArguments{Query: "请查询", DataType: "entity", Subject: "Laker"}) {
		t.Fatal("模型显式数据类型没有触发对应 adapter")
	}
}

func TestResearchConfigUsesSavedValuesOverEnvironment(t *testing.T) {
	t.Setenv("EASYAGENT_TAVILY_API_KEY", "environment-tavily")
	t.Setenv("EASYAGENT_SEARXNG_URL", "https://environment.example.com")
	config := MergeResearchConfig(ResearchConfigFromEnvironment(), ResearchConfig{Providers: map[string]ResearchProviderConfig{
		ResearchProviderTavily:  {Secret: "saved-tavily"},
		ResearchProviderSearXNG: {Endpoint: "https://saved.example.com"},
	}})
	if got := config.Provider(ResearchProviderTavily).Secret; got != "saved-tavily" {
		t.Fatalf("页面密钥没有覆盖环境变量: %q", got)
	}
	if got := config.Provider(ResearchProviderSearXNG).Endpoint; got != "https://saved.example.com" {
		t.Fatalf("页面地址没有覆盖环境变量: %q", got)
	}
	if !ResearchProviderReady(config, ResearchProviderTavily) || !ResearchProviderReady(config, ResearchProviderSearXNG) {
		t.Fatal("配置完整的生产搜索源应处于 ready 状态")
	}
}

func TestResearchProviderRegistryIsPublicMetadataOnly(t *testing.T) {
	definitions := ResearchProviderDefinitions()
	if len(definitions) != 6 {
		t.Fatalf("Provider 注册表数量异常: %d", len(definitions))
	}
	seen := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition.ID == "" || definition.Name == "" || definition.Category == "" || seen[definition.ID] {
			t.Fatalf("Provider 注册表元数据无效或重复: %+v", definition)
		}
		seen[definition.ID] = true
	}
	definitions[0].Name = "mutated"
	if ResearchProviderDefinitions()[0].Name == "mutated" {
		t.Fatal("调用方不应能修改 Provider 注册表")
	}
	layers := ResearchExecutionLayerDefinitions()
	if len(layers) != 2 || layers[0].ID == "" || layers[1].Components == "" {
		t.Fatalf("Research 执行层元数据不完整: %+v", layers)
	}
	layers[0].Name = "mutated"
	if ResearchExecutionLayerDefinitions()[0].Name == "mutated" {
		t.Fatal("调用方不应能修改 Research 执行层注册表")
	}
}

func TestResearchConfigValidationRejectsUnsafeEndpointSyntax(t *testing.T) {
	for _, endpoint := range []string{"ftp://search.example.com", "https://user:password@search.example.com", "not-a-url"} {
		err := ValidateResearchConfig(ResearchConfig{Providers: map[string]ResearchProviderConfig{
			ResearchProviderSearXNG: {Endpoint: endpoint},
		}})
		if err == nil {
			t.Fatalf("不安全或无效地址应被拒绝: %s", endpoint)
		}
	}
}

func TestDomainsAreEnforcedAfterRedirectedFetch(t *testing.T) {
	allowed, filtered := filterResearchSourcesByDomains([]researchSource{
		{URL: "https://platform.openai.com/docs"},
		{URL: "https://third-party.example/openai"},
	}, []string{"openai.com"})
	if len(allowed) != 1 || filtered != 1 || researchDomain(allowed[0].URL) != "platform.openai.com" {
		t.Fatalf("抓取后的域名白名单错误: allowed=%+v filtered=%d", allowed, filtered)
	}
	fetcher := &stubResearchFetcher{sources: map[string]researchSource{
		"https://openai.com/redirect": {URL: "https://third-party.example/page", Content: "redirected"},
		"https://openai.com/docs":     {URL: "https://openai.com/docs", Content: "official"},
	}}
	engine := &researchEngine{fetcher: fetcher}
	usable, failed := engine.fetchCandidates(context.Background(), []researchSearchResult{
		{URL: "https://openai.com/redirect"}, {URL: "https://openai.com/docs"},
	}, "OpenAI", 1, 2_000, []string{"openai.com"})
	if len(usable) != 1 || len(failed) != 1 || usable[0].URL != "https://openai.com/docs" {
		t.Fatalf("重定向越域后没有继续补位: usable=%+v failed=%+v", usable, failed)
	}
}

func TestWebResearchLiveScenarios(t *testing.T) {
	if os.Getenv("EASYAGENT_LIVE_RESEARCH") != "1" {
		t.Skip("set EASYAGENT_LIVE_RESEARCH=1 to run external research checks")
	}
	tests := []struct {
		name        string
		query       string
		depth       string
		freshness   string
		sourceScope string
		domains     []string
		wantKind    string
	}{
		{name: "weather", query: "合肥 今天 天气 温度 降雨概率 出行建议", depth: "normal", freshness: "day", wantKind: "weather_forecast"},
		{name: "github", query: "EasyPostman 的 GitHub star 多少", depth: "quick", freshness: "day", wantKind: "github_repository"},
		{name: "finance", query: "思科的股票价格", depth: "quick", freshness: "day", wantKind: "market_quote"},
		{name: "entity", query: "Laker 是谁", depth: "quick", freshness: "any"},
		{name: "technical", query: "Apache Kafka partition replication consumer group offset collaboration official documentation", depth: "normal", freshness: "any", sourceScope: "official", domains: []string{"kafka.apache.org"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, _ := json.Marshal(researchArguments{
				Query: test.query, Depth: test.depth, Freshness: test.freshness, MaxSources: 3,
				SourceScope: test.sourceScope, Domains: test.domains,
			})
			output, err := runWebResearch(context.Background(), raw)
			if err != nil {
				t.Fatalf("live research failed: %v", err)
			}
			var report struct {
				SourceCount int              `json:"source_count"`
				Sources     []researchSource `json:"sources"`
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatal(err)
			}
			if report.SourceCount == 0 {
				t.Fatalf("没有可用来源: %s", output)
			}
			labels := make([]string, 0, len(report.Sources))
			for _, source := range report.Sources {
				labels = append(labels, source.Kind+":"+source.Domain+":"+source.Title)
			}
			t.Logf("sources=%s", strings.Join(labels, " | "))
			if test.name == "technical" {
				combined := ""
				for _, source := range report.Sources {
					combined += "\n" + strings.ToLower(source.Content)
				}
				if !strings.Contains(combined, "replica") || !strings.Contains(combined, "offset") {
					t.Fatalf("Kafka 官方证据没有同时覆盖 replica 与 offset: %s", output)
				}
			}
			if test.wantKind != "" {
				found := false
				for _, source := range report.Sources {
					if source.Kind == test.wantKind {
						found = true
						if test.name == "weather" && (!strings.Contains(source.Content, `"query": "合肥"`) ||
							strings.Count(source.Content, `"date":`) < 2 || !strings.Contains(source.Content, "precipitation_probability_percent")) {
							t.Fatalf("天气结构化来源缺少地点、两天范围或降雨概率: %s", source.Content)
						}
					}
				}
				if !found {
					t.Fatalf("缺少结构化来源 %s: %s", test.wantKind, output)
				}
			}
		})
	}
}
