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
	"testing"
	"time"
)

type stubSearchProvider struct {
	name    string
	results []researchSearchResult
	err     error
	calls   *int
}

func (provider stubSearchProvider) Name() string { return provider.name }
func (provider stubSearchProvider) Search(context.Context, researchSearchRequest) ([]researchSearchResult, error) {
	if provider.calls != nil {
		(*provider.calls)++
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

func (adapter stubResearchAdapter) Name() string           { return adapter.name }
func (adapter stubResearchAdapter) Applicable(string) bool { return adapter.applies }
func (adapter stubResearchAdapter) Research(context.Context, string) ([]researchSource, error) {
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
	queries := planResearchQueries("Kafka 原理", "normal", []string{"kafka.apache.org"})
	if len(queries) < 2 || queries[0] != "site:kafka.apache.org Kafka 原理" {
		t.Fatalf("优先域名查询顺序错误: %#v", queries)
	}
	ranked := rankResearchCandidates([]researchSearchResult{
		{Title: "Kafka overview", URL: "https://example.com/kafka", Score: 100},
		{Title: "Kafka design", URL: "https://kafka.apache.org/documentation/", Score: 10},
	}, "Kafka 原理", []string{"kafka.apache.org"})
	if len(ranked) != 2 || researchDomain(ranked[0].URL) != "kafka.apache.org" {
		t.Fatalf("优先域名没有进入排名: %+v", ranked)
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
	usable, failed := engine.fetchCandidates(context.Background(), candidates, "问题", 2, 4_000)
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
		SourceCount int              `json:"source_count"`
		Sources     []researchSource `json:"sources"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceCount != 2 || len(report.Sources) != 2 || report.Sources[0].ID != "S1" || !strings.Contains(report.Sources[0].Citation, "https://") {
		t.Fatalf("研究证据包错误: %s", output)
	}
}

func TestNormalStructuredFactDoesNotWaitForGeneralSearch(t *testing.T) {
	searchCalls := 0
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
	if searchCalls != 0 || !strings.Contains(output, `"kind": "structured_fact"`) {
		t.Fatalf("normal 结构化事实不应等待通用搜索: calls=%d output=%s", searchCalls, output)
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
	if days := weatherForecastDays("合肥未来10天天气"); days != 10 {
		t.Fatalf("天气天数解析错误: %d", days)
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

func TestWeatherAdapterUsesStructuredProvider(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/geo":
			return http.StatusOK, `{"results":[{"id":1,"name":"合肥","admin1":"安徽","country":"中国","timezone":"Asia/Shanghai","latitude":31.86,"longitude":117.28}]}`
		case "/forecast":
			return http.StatusOK, `{"timezone":"Asia/Shanghai","current":{"time":"2026-09-08T10:00","temperature_2m":28,"apparent_temperature":29,"relative_humidity_2m":55,"weather_code":0,"wind_speed_10m":8},"daily":{"time":["2026-09-08","2026-09-09"],"weather_code":[0,61],"temperature_2m_max":[31,29],"temperature_2m_min":[22,21],"precipitation_probability_max":[10,70],"sunrise":["06:00","06:01"],"sunset":["18:20","18:19"]}}`
		default:
			return http.StatusNotFound, `{}`
		}
	})
	adapter := &weatherResearchAdapter{client: client, geocodingURL: "https://weather.test/geo", forecastURL: "https://weather.test/forecast"}
	sources, err := adapter.Research(context.Background(), "合肥今天和明天天气")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Kind != "weather_forecast" || !strings.Contains(sources[0].Content, "2026-09-09") {
		t.Fatalf("天气结构化来源错误: %+v", sources)
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
	sources, err := adapter.Research(context.Background(), "github.com/lakernote/easy-postman stars")
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
			return http.StatusOK, `<html><head><meta name="octolytics-dimension-repository_nwo" content="lakernote/easy-postman"><meta property="og:description" content="API client - lakernote/easy-postman"></head><body><span id="repo-stars-counter-star" title="713">713</span><span id="repo-network-counter" title="60">60</span><span itemprop="programmingLanguage">Java</span></body></html>`
		default:
			return http.StatusNotFound, `{"error":"unexpected test request"}`
		}
	})
	adapter := &githubResearchAdapter{
		client: client, apiBase: "https://api.github.test", webBase: "https://github.test",
	}
	sources, err := adapter.Research(context.Background(), "EasyPostman 的 GitHub star 多少")
	if err != nil {
		t.Fatal(err)
	}
	if apiSearchCalls != 1 || len(sources) != 1 || sources[0].Provider != "github_web" ||
		!strings.Contains(sources[0].Content, `"stars": 713`) ||
		!strings.Contains(sources[0].Content, `"forks": 60`) {
		t.Fatalf("GitHub 官方网页降级错误: apiSearchCalls=%d sources=%+v", apiSearchCalls, sources)
	}
}

func TestFinanceAdapterResolvesCompanyAndReturnsTimestampedQuote(t *testing.T) {
	client := researchJSONClient(func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/search":
			return http.StatusOK, "{\"quotes\":[{\"symbol\":\"CSCO\",\"shortname\":\"Cisco Systems\",\"longname\":\"Cisco Systems, Inc.\",\"exchange\":\"NMS\",\"quoteType\":\"EQUITY\"}]}"
		case "/chart/CSCO":
			return http.StatusOK, "{\"chart\":{\"result\":[{\"meta\":{\"symbol\":\"CSCO\",\"currency\":\"USD\",\"exchangeName\":\"NMS\",\"instrumentType\":\"EQUITY\",\"exchangeTimezoneName\":\"America/New_York\",\"regularMarketPrice\":70.5,\"previousClose\":69.5,\"regularMarketTime\":1788796800}}],\"error\":null}}"
		default:
			return http.StatusNotFound, "{}"
		}
	})
	adapter := &financeResearchAdapter{
		client: client, searchURL: "https://finance.test/search",
		chartBase: "https://finance.test/chart", quoteBase: "https://finance.example/quote",
	}
	sources, err := adapter.Research(context.Background(), "思科的股票啊")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Kind != "market_quote" ||
		!strings.Contains(sources[0].Content, "\"symbol\": \"CSCO\"") ||
		!strings.Contains(sources[0].Content, "\"market_time_utc\"") {
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
	sources, err := adapter.Research(context.Background(), "思科的股票价格")
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

func TestParseResearchArgumentsRejectsInvalidDomain(t *testing.T) {
	_, err := parseResearchArguments(json.RawMessage(`{"query":"test","domains":["https://example.com/path"]}`))
	if err == nil || !strings.Contains(err.Error(), "无效优先域名") {
		t.Fatalf("带路径的优先域名应被拒绝: %v", err)
	}
}

func TestWebResearchLiveScenarios(t *testing.T) {
	if os.Getenv("EASYAGENT_LIVE_RESEARCH") != "1" {
		t.Skip("set EASYAGENT_LIVE_RESEARCH=1 to run external research checks")
	}
	tests := []struct {
		name     string
		query    string
		depth    string
		wantKind string
	}{
		{name: "weather", query: "合肥未来一周天气", depth: "normal", wantKind: "weather_forecast"},
		{name: "github", query: "EasyPostman 的 GitHub star 多少", depth: "quick", wantKind: "github_repository"},
		{name: "finance", query: "思科的股票价格", depth: "quick", wantKind: "market_quote"},
		{name: "entity", query: "Laker 是谁", depth: "quick"},
		{name: "technical", query: "Kafka 的核心原理", depth: "quick"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, _ := json.Marshal(researchArguments{Query: test.query, Depth: test.depth, Freshness: "day", MaxSources: 3})
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
			if test.wantKind != "" {
				found := false
				for _, source := range report.Sources {
					found = found || source.Kind == test.wantKind
				}
				if !found {
					t.Fatalf("缺少结构化来源 %s: %s", test.wantKind, output)
				}
			}
		})
	}
}
