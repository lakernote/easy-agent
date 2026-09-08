package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

var (
	weatherDaysPattern = regexp.MustCompile(`(?i)(?:未来|接下来|next\s*)(\d{1,2})\s*(?:天|days?)`)
	githubURLPattern   = regexp.MustCompile(`(?i)github\.com/([a-z0-9_.-]+)/([a-z0-9_.-]+)`)
	repositoryPattern  = regexp.MustCompile(`(?i)\b([a-z0-9_.-]+)/([a-z0-9_.-]+)\b`)
	tickerPattern      = regexp.MustCompile(`\b[A-Z]{1,5}\b`)
	financeTickerValue = regexp.MustCompile(`^[A-Za-z0-9.-]{1,12}$`)
	githubNoisePattern = regexp.MustCompile(`(?i)\bgithub\b|\brepositories?\b|\brepos?\b|\bstars?\b|仓库|项目|多少|数量|有|的`)
	camelBoundary      = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	githubPathSegment  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	githubStarsJSON    = regexp.MustCompile(`"stargazerCount"\s*:\s*([0-9]+)`)
	githubCreatedJSON  = regexp.MustCompile(`"createdAt"\s*:\s*"([^"]+)"`)
)

var weatherIntentMarkers = []string{
	"今天和明天", "今天明天", "未来一周", "未来7天", "未来七天", "接下来一周",
	"降水概率", "降雨概率", "下雨概率", "最高气温", "最低气温", "体感温度", "天气预报",
	"今天", "明天", "后天", "现在", "当前", "实时", "天气", "气温", "温度", "预报",
	"precipitation probability", "chance of rain", "next 7 days", "next week", "weather",
	"forecast", "temperature", "today", "tomorrow", "current",
}

type weatherResearchAdapter struct {
	client       *http.Client
	geocodingURL string
	forecastURL  string
}
type githubResearchAdapter struct {
	client  *http.Client
	token   string
	apiBase string
	webBase string
}
type financeResearchAdapter struct {
	client      *http.Client
	searchURL   string
	chartBase   string
	quoteBase   string
	wikidataURL string
}
type entityResearchAdapter struct {
	client      *http.Client
	wikidataURL string
}

type researchHTTPError struct {
	StatusCode int
	RetryAfter string
	Detail     string
}

func (value *researchHTTPError) Error() string {
	if value == nil {
		return "HTTP 请求失败"
	}
	if value.RetryAfter != "" {
		return fmt.Sprintf("HTTP %d，Retry-After=%s", value.StatusCode, value.RetryAfter)
	}
	if value.Detail != "" {
		return fmt.Sprintf("HTTP %d: %s", value.StatusCode, value.Detail)
	}
	return fmt.Sprintf("HTTP %d", value.StatusCode)
}

func defaultResearchAdapters() []researchAdapter {
	return researchAdapters(ResearchConfigFromEnvironment())
}

func researchAdapters(config ResearchConfig) []researchAdapter {
	client := safeResearchHTTPClient(12 * time.Second)
	token := strings.TrimSpace(config.Provider(ResearchProviderGitHub).Secret)
	return []researchAdapter{
		&weatherResearchAdapter{client: client, geocodingURL: "https://geocoding-api.open-meteo.com/v1/search", forecastURL: "https://api.open-meteo.com/v1/forecast"},
		&githubResearchAdapter{client: client, token: token, apiBase: "https://api.github.com", webBase: "https://github.com"},
		&financeResearchAdapter{client: client, searchURL: "https://query1.finance.yahoo.com/v1/finance/search", chartBase: "https://query1.finance.yahoo.com/v8/finance/chart", quoteBase: "https://finance.yahoo.com/quote", wikidataURL: "https://www.wikidata.org/w/api.php"},
		&entityResearchAdapter{client: client, wikidataURL: "https://www.wikidata.org/w/api.php"},
	}
}

type githubRepositorySnapshot struct {
	FullName        string     `json:"full_name"`
	Name            string     `json:"name"`
	HTMLURL         string     `json:"html_url"`
	Description     string     `json:"description"`
	StargazersCount int        `json:"stargazers_count"`
	ForksCount      int        `json:"forks_count"`
	OpenIssuesCount int        `json:"open_issues_count"`
	WatchersCount   int        `json:"subscribers_count"`
	DefaultBranch   string     `json:"default_branch"`
	Language        string     `json:"language"`
	Archived        bool       `json:"archived"`
	Fork            bool       `json:"fork"`
	Visibility      string     `json:"visibility"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	PushedAt        *time.Time `json:"pushed_at"`
	Owner           struct {
		Login string `json:"login"`
	} `json:"owner"`
	License *struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`
	forksKnown      bool
	openIssuesKnown bool
	createdAtKnown  bool
}

func (adapter *weatherResearchAdapter) Name() string { return "open_meteo" }
func (adapter *githubResearchAdapter) Name() string  { return "github_api" }
func (adapter *financeResearchAdapter) Name() string { return "yahoo_finance" }
func (adapter *entityResearchAdapter) Name() string  { return "wikidata_entities" }

// The model may explicitly select a structured data type. Lexical detection is
// retained only for data_type=auto so older/smaller models still degrade safely.
func adapterTypeApplicable(arguments researchArguments, dataType string, heuristic bool) bool {
	if arguments.DataType == dataType {
		return true
	}
	return (arguments.DataType == "" || arguments.DataType == "auto") && heuristic
}

func (adapter *entityResearchAdapter) Applicable(arguments researchArguments) bool {
	return adapterTypeApplicable(arguments, "entity", entityResearchLookup(arguments.Query) != "")
}

func (adapter *entityResearchAdapter) Research(ctx context.Context, arguments researchArguments) ([]researchSource, error) {
	lookup := strings.TrimSpace(arguments.Subject)
	if lookup == "" {
		lookup = entityResearchLookup(arguments.Query)
	}
	if lookup == "" {
		return nil, errors.New("无法从问题中确定待消歧实体")
	}
	language := "en"
	for _, character := range lookup {
		if character >= '\u4e00' && character <= '\u9fff' {
			language = "zh"
			break
		}
	}
	endpoint := adapter.wikidataURL + "?" + url.Values{
		"action": {"wbsearchentities"}, "search": {lookup}, "language": {language},
		"uselang": {language}, "type": {"item"}, "limit": {"8"}, "format": {"json"},
	}.Encode()
	var payload struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
			ConceptURI  string `json:"concepturi"`
			Match       struct {
				Type     string `json:"type"`
				Language string `json:"language"`
				Text     string `json:"text"`
			} `json:"match"`
		} `json:"search"`
	}
	if err := getResearchJSON(ctx, adapter.client, endpoint, nil, &payload); err != nil {
		return nil, fmt.Errorf("实体消歧失败: %w", err)
	}
	if len(payload.Search) == 0 {
		return nil, fmt.Errorf("Wikidata 没有找到实体 %q", lookup)
	}
	candidates := make([]map[string]any, 0, len(payload.Search))
	for _, item := range payload.Search {
		candidate := map[string]any{"id": item.ID, "label": item.Label, "description": item.Description}
		if item.ConceptURI != "" {
			candidate["url"] = item.ConceptURI
		}
		if item.Match.Text != "" {
			candidate["matched_text"] = item.Match.Text
			candidate["match_type"] = item.Match.Type
		}
		candidates = append(candidates, candidate)
	}
	content, _ := json.MarshalIndent(map[string]any{
		"query": lookup, "candidates": candidates,
		"note": "这是实体候选列表，不表示第一个候选必然是用户所指对象；结合其他来源和用户上下文消歧。",
	}, "", "  ")
	return []researchSource{{
		Title: lookup + " entity candidates", URL: endpoint, Domain: "wikidata.org",
		Provider: adapter.Name(), Kind: "entity_candidates", ContentType: "application/json",
		Status: http.StatusOK, RetrievedAt: time.Now().UTC().Format(time.RFC3339), Content: string(content),
	}}, nil
}

func entityResearchLookup(query string) string {
	lower := strings.ToLower(query)
	markers := []string{"是谁", "是什么", "什么意思", "含义", "who is", "what is", "meaning"}
	found := false
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	value := strings.TrimSpace(query)
	if index := strings.IndexAny(value, "?？!！;；\n"); index >= 0 {
		value = value[:index]
	}
	for _, marker := range markers {
		for {
			index := strings.Index(strings.ToLower(value), marker)
			if index < 0 {
				break
			}
			value = value[:index] + " " + value[index+len(marker):]
		}
	}
	value = compactWhitespace(strings.Trim(value, " ,.:，。："))
	if value == "" || len([]rune(value)) > 80 {
		return ""
	}
	return value
}

func (adapter *weatherResearchAdapter) Applicable(arguments researchArguments) bool {
	value := strings.ToLower(arguments.Query)
	for _, marker := range []string{"天气", "气温", "温度", "预报", "weather", "forecast", "temperature"} {
		if strings.Contains(value, marker) {
			return adapterTypeApplicable(arguments, "weather", true)
		}
	}
	return adapterTypeApplicable(arguments, "weather", false)
}

func (adapter *weatherResearchAdapter) Research(ctx context.Context, arguments researchArguments) ([]researchSource, error) {
	locations := []string{}
	if arguments.Subject != "" {
		locations = append(locations, arguments.Subject)
	}
	locations = append(locations, weatherLocationCandidates(arguments.Query)...)
	locations = uniqueStrings(locations)
	if len(locations) == 0 {
		return nil, errors.New("无法从问题中确定天气地点")
	}
	type weatherPlace struct {
		ID        int64   `json:"id"`
		Name      string  `json:"name"`
		Country   string  `json:"country"`
		Admin1    string  `json:"admin1"`
		Timezone  string  `json:"timezone"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	var place weatherPlace
	resolvedQuery := ""
	for _, location := range locations {
		geoEndpoint := adapter.geocodingURL + "?" + url.Values{
			"name": {location}, "count": {"5"}, "language": {"zh"}, "format": {"json"},
		}.Encode()
		var geoPayload struct {
			Results []weatherPlace `json:"results"`
		}
		if err := getResearchJSON(ctx, adapter.client, geoEndpoint, nil, &geoPayload); err != nil {
			return nil, fmt.Errorf("地点解析失败: %w", err)
		}
		if len(geoPayload.Results) > 0 {
			place = geoPayload.Results[0]
			resolvedQuery = location
			break
		}
	}
	if resolvedQuery == "" {
		return nil, fmt.Errorf("没有找到地点候选 %q", strings.Join(locations, "、"))
	}
	days := arguments.TimeRangeDays
	if days == 0 {
		days = weatherForecastDays(arguments.Query)
	}
	forecastEndpoint := adapter.forecastURL + "?" + url.Values{
		"latitude":      {strconv.FormatFloat(place.Latitude, 'f', 6, 64)},
		"longitude":     {strconv.FormatFloat(place.Longitude, 'f', 6, 64)},
		"current":       {"temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m"},
		"daily":         {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,sunrise,sunset"},
		"forecast_days": {strconv.Itoa(days)},
		"timezone":      {"auto"},
	}.Encode()
	var forecast struct {
		Timezone string `json:"timezone"`
		Current  struct {
			Time        string  `json:"time"`
			Temperature float64 `json:"temperature_2m"`
			Apparent    float64 `json:"apparent_temperature"`
			Humidity    float64 `json:"relative_humidity_2m"`
			WeatherCode int     `json:"weather_code"`
			Wind        float64 `json:"wind_speed_10m"`
		} `json:"current"`
		Daily weatherDaily `json:"daily"`
	}
	if err := getResearchJSON(ctx, adapter.client, forecastEndpoint, nil, &forecast); err != nil {
		return nil, fmt.Errorf("天气数据读取失败: %w", err)
	}
	content := map[string]any{
		"resolved_location": map[string]any{
			"query": resolvedQuery, "name": place.Name, "admin1": place.Admin1, "country": place.Country,
			"latitude": place.Latitude, "longitude": place.Longitude,
		},
		"timezone": forecast.Timezone,
		"current": map[string]any{
			"observed_at": forecast.Current.Time, "condition": weatherText(forecast.Current.WeatherCode),
			"temperature_c": forecast.Current.Temperature, "feels_like_c": forecast.Current.Apparent,
			"humidity_percent": forecast.Current.Humidity, "wind_kmh": forecast.Current.Wind,
		},
		"daily_forecast": buildWeatherForecast(forecast.Daily),
		"note":           "天气预报会变化；回答时保留数据时间和时区。travel_advice 仅依据每日天气代码、降水概率和最高/最低温生成，不包含小时级时段判断。",
	}
	encoded, _ := json.MarshalIndent(content, "", "  ")
	return []researchSource{{
		Title: fmt.Sprintf("%s天气与%d天预报", place.Name, days),
		URL:   forecastEndpoint, Domain: "open-meteo.com", Provider: adapter.Name(), Kind: "weather_forecast",
		ContentType: "application/json", Status: http.StatusOK,
		RetrievedAt: time.Now().UTC().Format(time.RFC3339), Content: string(encoded),
	}}, nil
}

type weatherDaily struct {
	Time                 []string  `json:"time"`
	WeatherCode          []int     `json:"weather_code"`
	TemperatureMax       []float64 `json:"temperature_2m_max"`
	TemperatureMin       []float64 `json:"temperature_2m_min"`
	PrecipitationProbMax []int     `json:"precipitation_probability_max"`
	Sunrise              []string  `json:"sunrise"`
	Sunset               []string  `json:"sunset"`
}

func buildWeatherForecast(daily weatherDaily) []map[string]any {
	count := min(len(daily.Time), len(daily.WeatherCode), len(daily.TemperatureMax), len(daily.TemperatureMin))
	result := make([]map[string]any, 0, count)
	for index := 0; index < count; index++ {
		day := map[string]any{
			"date": daily.Time[index], "condition": weatherText(daily.WeatherCode[index]),
			"temp_max_c": daily.TemperatureMax[index], "temp_min_c": daily.TemperatureMin[index],
		}
		if index < len(daily.PrecipitationProbMax) {
			day["precipitation_probability_percent"] = daily.PrecipitationProbMax[index]
		}
		precipitation := -1
		if index < len(daily.PrecipitationProbMax) {
			precipitation = daily.PrecipitationProbMax[index]
		}
		day["travel_advice"] = weatherTravelAdvice(daily.WeatherCode[index], daily.TemperatureMax[index], daily.TemperatureMin[index], precipitation)
		if index < len(daily.Sunrise) {
			day["sunrise"] = daily.Sunrise[index]
		}
		if index < len(daily.Sunset) {
			day["sunset"] = daily.Sunset[index]
		}
		result = append(result, day)
	}
	return result
}

func weatherTravelAdvice(code int, maximum, minimum float64, precipitation int) string {
	advice := make([]string, 0, 3)
	switch code {
	case 95, 96, 99:
		advice = append(advice, "有雷暴，避免空旷处和高风险户外活动")
	case 71, 73, 75, 77, 85, 86:
		advice = append(advice, "可能有雪，注意路面湿滑")
	case 51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 80, 81, 82:
		advice = append(advice, "预报有降水，外出携带雨具")
	default:
		switch {
		case precipitation >= 60:
			advice = append(advice, "降水概率较高，外出携带雨具")
		case precipitation >= 30:
			advice = append(advice, "有一定降水可能，建议备雨具")
		case precipitation >= 0:
			advice = append(advice, "降水概率较低，按常规出行")
		}
	}
	if maximum >= 35 {
		advice = append(advice, "最高温较高，注意防晒补水")
	}
	if minimum <= 5 {
		advice = append(advice, "最低温较低，注意保暖")
	}
	if len(advice) == 0 {
		advice = append(advice, "按常规出行")
	}
	return strings.Join(uniqueStrings(advice), "；") + "；出发前复核临近预报"
}

func weatherLocation(query string) string {
	candidates := weatherLocationCandidates(query)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

// weatherLocationCandidates keeps location parsing deterministic while tolerating
// small models that append requested fields or answer instructions to the query.
// A prefix before the first weather/time marker is usually the cleanest location;
// the fully cleaned query remains a fallback for forms such as "weather in New York".
func weatherLocationCandidates(query string) []string {
	raw := strings.ToLower(strings.TrimSpace(httpURLPattern.ReplaceAllString(query, " ")))
	if raw == "" {
		return nil
	}
	values := make([]string, 0, 4)
	if index := firstWeatherIntentIndex(raw); index > 0 {
		values = append(values, raw[:index])
	}
	values = append(values, raw)

	candidates := make([]string, 0, len(values)*2)
	for _, value := range values {
		candidate := cleanWeatherLocation(value)
		if candidate == "" {
			continue
		}
		candidates = append(candidates, candidate)
		// Geocoders are generally better at a city token than a conversational
		// phrase. Keep the final whitespace-delimited token as a last fallback.
		fields := strings.Fields(candidate)
		if len(fields) > 1 {
			candidates = append(candidates, fields[len(fields)-1])
		}
	}
	return uniqueStrings(candidates)
}

func firstWeatherIntentIndex(value string) int {
	index := -1
	for _, marker := range weatherIntentMarkers {
		if found := strings.Index(value, marker); found >= 0 && (index < 0 || found < index) {
			index = found
		}
	}
	if match := weatherDaysPattern.FindStringIndex(value); match != nil && (index < 0 || match[0] < index) {
		index = match[0]
	}
	return index
}

func cleanWeatherLocation(value string) string {
	replacer := strings.NewReplacer(
		"未来一周", " ", "未来7天", " ", "未来七天", " ", "接下来一周", " ",
		"今天和明天", " ", "今天明天", " ", "今天", " ", "明天", " ", "后天", " ",
		"现在", " ", "当前", " ", "实时", " ", "天气预报", " ", "天气", " ",
		"最高气温", " ", "最低气温", " ", "体感温度", " ", "气温", " ", "温度", " ", "预报", " ",
		"降水概率", " ", "降雨概率", " ", "下雨概率", " ", "降水", " ", "降雨", " ",
		"风力", " ", "风速", " ", "湿度", " ", "日出", " ", "日落", " ",
		"出行建议", " ", "穿衣建议", " ", "出行", " ", "建议", " ",
		"怎么样", " ", "如何", " ", "多少", " ", "帮我查一下", " ", "查一下", " ",
		"请帮我", " ", "告诉我", " ", "我想知道", " ", "查询", " ", "查看", " ", "请问", " ", "请", " ",
		"weather", " ", "forecast", " ", "temperature", " ", "today", " ", "tomorrow", " ",
		"current", " ", "precipitation probability", " ", "chance of rain", " ",
		"travel advice", " ", "next week", " ", "next 7 days", " ", " in ", " ",
	)
	value = replacer.Replace(value)
	value = weatherDaysPattern.ReplaceAllString(value, " ")
	value = strings.Trim(value, " \t\r\n,.;:!?，。；：！？、的")
	value = compactWhitespace(value)
	for previous := ""; value != previous; {
		previous = value
		for _, prefix := range []string{"what is the", "what's the", "whats the", "tell me the", "tell me", "show me", "please"} {
			if value == prefix {
				value = ""
			} else if strings.HasPrefix(value, prefix+" ") {
				value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
			}
		}
	}
	if len([]rune(value)) == 0 || len([]rune(value)) > 80 {
		return ""
	}
	return value
}

func weatherForecastDays(query string) int {
	if match := weatherDaysPattern.FindStringSubmatch(query); len(match) == 2 {
		if value, err := strconv.Atoi(match[1]); err == nil {
			return min(max(value, 1), 16)
		}
	}
	lower := strings.ToLower(query)
	if strings.Contains(query, "一周") || strings.Contains(query, "七天") || strings.Contains(lower, "week") {
		return 7
	}
	if strings.Contains(query, "今天") && strings.Contains(query, "明天") {
		return 2
	}
	if strings.Contains(query, "后天") {
		return 3
	}
	if strings.Contains(query, "今天") || strings.Contains(query, "明天") ||
		strings.Contains(query, "现在") || strings.Contains(query, "当前") || strings.Contains(query, "实时") ||
		strings.Contains(lower, "today") || strings.Contains(lower, "tomorrow") || strings.Contains(lower, "current") {
		// Include tomorrow even if a small model accidentally shortens “today and
		// tomorrow” to “today”. The extra day is bounded and avoids a second call.
		return 2
	}
	return 7
}

func weatherText(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1, 2:
		return "少云"
	case 3:
		return "阴"
	case 45, 48:
		return "雾"
	case 51, 53, 55, 56, 57:
		return "毛毛雨"
	case 61, 63, 65, 66, 67:
		return "雨"
	case 71, 73, 75, 77:
		return "雪"
	case 80, 81, 82:
		return "阵雨"
	case 85, 86:
		return "阵雪"
	case 95, 96, 99:
		return "雷暴"
	default:
		return fmt.Sprintf("WMO %d", code)
	}
}

func (adapter *githubResearchAdapter) Applicable(arguments researchArguments) bool {
	value := strings.ToLower(arguments.Query)
	heuristic := strings.Contains(value, "github") || strings.Contains(value, "star") ||
		strings.Contains(value, "仓库") || strings.Contains(value, "repository")
	return adapterTypeApplicable(arguments, "repository", heuristic)
}

func (adapter *githubResearchAdapter) Research(ctx context.Context, arguments researchArguments) ([]researchSource, error) {
	lookup := arguments.Query
	if arguments.Subject != "" {
		lookup = arguments.Subject
	}
	owner, repository, keyword := githubRepositoryIdentity(lookup)
	var searchFallback *githubRepositorySnapshot
	if owner == "" || repository == "" {
		if keyword == "" {
			return nil, errors.New("无法确定 GitHub 仓库")
		}
		var search struct {
			Items []githubRepositorySnapshot `json:"items"`
		}
		var searchErr error
		for _, variant := range githubKeywordVariants(keyword) {
			searchEndpoint := strings.TrimRight(adapter.apiBase, "/") + "/search/repositories?" + url.Values{
				"q": {variant + " in:name"}, "per_page": {"5"},
			}.Encode()
			search.Items = nil
			searchErr = getResearchJSON(ctx, adapter.client, searchEndpoint, adapter.githubHeaders(), &search)
			if searchErr == nil && len(search.Items) > 0 {
				break
			}
			if terminalGitHubAPIError(searchErr) {
				break
			}
		}
		if len(search.Items) == 0 {
			var webErr error
			owner, repository, webErr = adapter.searchGitHubWeb(ctx, keyword)
			if webErr != nil {
				if searchErr != nil {
					return nil, fmt.Errorf("GitHub API 搜索失败（%v），网页降级也失败: %w", searchErr, webErr)
				}
				return nil, fmt.Errorf("GitHub 没有找到仓库 %q: %w", keyword, webErr)
			}
		} else {
			selected := selectGitHubRepository(search.Items, keyword)
			owner, repository = selected.Owner.Login, selected.Name
			fallback := selected
			searchFallback = &fallback
		}
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s", strings.TrimRight(adapter.apiBase, "/"), url.PathEscape(owner), url.PathEscape(repository))
	var repo githubRepositorySnapshot
	provider := adapter.Name()
	if apiErr := getResearchJSON(ctx, adapter.client, endpoint, adapter.githubHeaders(), &repo); apiErr != nil {
		webRepo, webErr := adapter.readGitHubRepositoryWeb(ctx, owner, repository)
		switch {
		case webErr == nil:
			repo, provider = webRepo, "github_web"
		case searchFallback != nil:
			repo, provider = *searchFallback, "github_api_search"
		default:
			return nil, fmt.Errorf("GitHub API 读取失败（%v），网页降级也失败: %w", apiErr, webErr)
		}
	}
	if repo.FullName == "" {
		repo.FullName = owner + "/" + repository
	}
	if repo.Name == "" {
		repo.Name = repository
	}
	if repo.HTMLURL == "" {
		repo.HTMLURL = strings.TrimRight(adapter.githubWebBase(), "/") + "/" + owner + "/" + repository
	}
	return []researchSource{githubRepositorySource(repo, provider)}, nil
}

func terminalGitHubAPIError(err error) bool {
	var httpErr *researchHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden || httpErr.StatusCode == http.StatusTooManyRequests
}

func selectGitHubRepository(candidates []githubRepositorySnapshot, keyword string) githubRepositorySnapshot {
	selected := candidates[0]
	selectedMatched := false
	for _, candidate := range candidates {
		matched := strings.EqualFold(candidate.Name, keyword) || strings.EqualFold(candidate.FullName, keyword) || normalizedRepositoryName(candidate.Name) == normalizedRepositoryName(keyword)
		if matched && (!selectedMatched || candidate.StargazersCount > selected.StargazersCount) {
			selected = candidate
			selectedMatched = true
		}
	}
	return selected
}

func githubRepositorySource(repo githubRepositorySnapshot, provider string) researchSource {
	content := map[string]any{
		"full_name": repo.FullName, "description": repo.Description,
		"stars": repo.StargazersCount, "data_path": provider,
	}
	if provider != "github_web" || repo.forksKnown {
		content["forks"] = repo.ForksCount
	}
	if provider == "github_api" {
		content["subscribers"] = repo.WatchersCount
		content["open_issues"] = repo.OpenIssuesCount
		content["default_branch"] = repo.DefaultBranch
		content["archived"] = repo.Archived
		content["fork"] = repo.Fork
		content["visibility"] = repo.Visibility
		content["created_at"] = repo.CreatedAt
		content["updated_at"] = repo.UpdatedAt
		content["pushed_at"] = repo.PushedAt
	} else if provider == "github_api_search" {
		content["forks"] = repo.ForksCount
		content["open_issues"] = repo.OpenIssuesCount
		content["default_branch"] = repo.DefaultBranch
		content["archived"] = repo.Archived
		content["fork"] = repo.Fork
		content["visibility"] = repo.Visibility
		content["created_at"] = repo.CreatedAt
		content["updated_at"] = repo.UpdatedAt
		content["pushed_at"] = repo.PushedAt
		content["unavailable_fields"] = []string{"subscribers"}
	} else if provider == "github_web" {
		unavailable := make([]string, 0, 3)
		if repo.openIssuesKnown {
			content["open_issues"] = repo.OpenIssuesCount
		} else {
			unavailable = append(unavailable, "open_issues")
		}
		if repo.createdAtKnown {
			content["created_at"] = repo.CreatedAt
		} else {
			unavailable = append(unavailable, "created_at")
		}
		unavailable = append(unavailable, "updated_at", "pushed_at")
		content["unavailable_fields"] = unavailable
	}
	if repo.Language != "" {
		content["language"] = repo.Language
	}
	if repo.License != nil {
		content["license"] = repo.License.SPDXID
	}
	encoded, _ := json.MarshalIndent(content, "", "  ")
	source := researchSource{
		Title: repo.FullName, URL: repo.HTMLURL, Domain: "github.com",
		Provider: provider, Kind: "github_repository", ContentType: "application/json",
		Status:      http.StatusOK,
		RetrievedAt: time.Now().UTC().Format(time.RFC3339), Content: string(encoded),
	}
	if !repo.UpdatedAt.IsZero() {
		source.PublishedAt = repo.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return source
}

func (adapter *githubResearchAdapter) githubWebBase() string {
	if value := strings.TrimSpace(adapter.webBase); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "https://github.com"
}

func (adapter *githubResearchAdapter) searchGitHubWeb(ctx context.Context, keyword string) (string, string, error) {
	var lastErr error
	for _, variant := range githubKeywordVariants(keyword) {
		endpoint := adapter.githubWebBase() + "/search?" + url.Values{
			"q": {variant}, "type": {"repositories"},
		}.Encode()
		body, err := getResearchBody(ctx, adapter.client, endpoint, http.Header{
			"Accept": {"text/html,application/xhtml+xml"},
		}, maxResearchSourceBytes)
		if err != nil {
			lastErr = err
			continue
		}
		owner, repository, err := parseGitHubSearchRepository(body, keyword)
		if err == nil {
			return owner, repository, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("网页搜索没有匹配仓库")
	}
	return "", "", lastErr
}

func parseGitHubSearchRepository(body []byte, keyword string) (string, string, error) {
	document, err := xhtml.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", "", fmt.Errorf("GitHub 搜索页 HTML 无效: %w", err)
	}
	target := normalizedRepositoryName(keyword)
	type match struct {
		owner, repository string
		score             int
	}
	best := match{}
	walkHTML(document, func(node *xhtml.Node) bool {
		if node.Type != xhtml.ElementNode || node.Data != "a" {
			return true
		}
		parsed, err := url.Parse(htmlAttribute(node, "href"))
		if err != nil || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return true
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) != 2 || !githubPathSegment.MatchString(parts[0]) || !githubPathSegment.MatchString(parts[1]) {
			return true
		}
		repositoryName := strings.TrimSuffix(parts[1], ".git")
		normalized := normalizedRepositoryName(repositoryName)
		score := 0
		switch {
		case strings.EqualFold(repositoryName, keyword):
			score = 120
		case normalized == target:
			score = 100
		case len(target) >= 4 && (strings.Contains(normalized, target) || strings.Contains(target, normalized)):
			score = 30
		default:
			return true
		}
		if normalizedRepositoryName(htmlNodeText(node)) == target {
			score += 10
		}
		if score > best.score {
			best = match{owner: parts[0], repository: repositoryName, score: score}
		}
		return true
	})
	if best.score == 0 {
		return "", "", fmt.Errorf("GitHub 搜索页没有与 %q 精确匹配的仓库", keyword)
	}
	return best.owner, best.repository, nil
}

func (adapter *githubResearchAdapter) readGitHubRepositoryWeb(ctx context.Context, owner, repository string) (githubRepositorySnapshot, error) {
	endpoint := adapter.githubWebBase() + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repository)
	body, err := getResearchBody(ctx, adapter.client, endpoint, http.Header{
		"Accept": {"text/html,application/xhtml+xml"},
	}, maxResearchSourceBytes)
	if err != nil {
		return githubRepositorySnapshot{}, err
	}
	repo, err := parseGitHubRepositoryPage(body, owner+"/"+repository)
	if err != nil {
		return githubRepositorySnapshot{}, err
	}
	repo.HTMLURL = endpoint
	return repo, nil
}

func parseGitHubRepositoryPage(body []byte, expectedFullName string) (githubRepositorySnapshot, error) {
	document, err := xhtml.Parse(strings.NewReader(string(body)))
	if err != nil {
		return githubRepositorySnapshot{}, fmt.Errorf("GitHub 仓库页 HTML 无效: %w", err)
	}
	repo := githubRepositorySnapshot{}
	starsKnown := false
	forksKnown := false
	issuesKnown := false
	walkHTML(document, func(node *xhtml.Node) bool {
		if node.Type != xhtml.ElementNode {
			return true
		}
		if node.Data == "meta" {
			switch htmlAttribute(node, "name") {
			case "octolytics-dimension-repository_nwo":
				repo.FullName = strings.TrimSpace(htmlAttribute(node, "content"))
			}
			if htmlAttribute(node, "property") == "og:description" {
				repo.Description = strings.TrimSpace(htmlAttribute(node, "content"))
			}
		}
		switch htmlAttribute(node, "id") {
		case "repo-stars-counter-star":
			if count, ok := parseGitHubCounter(htmlAttribute(node, "title")); ok {
				repo.StargazersCount, starsKnown = count, true
			}
		case "repo-network-counter":
			if count, ok := parseGitHubCounter(htmlAttribute(node, "title")); ok {
				repo.ForksCount = count
				forksKnown = true
			}
		case "issues-repo-tab-count":
			if count, ok := parseGitHubCounter(htmlAttribute(node, "title")); ok {
				repo.OpenIssuesCount = count
				issuesKnown = true
			}
		}
		if htmlAttribute(node, "itemprop") == "programmingLanguage" {
			repo.Language = htmlNodeText(node)
		}
		return true
	})
	if repo.FullName == "" {
		return githubRepositorySnapshot{}, errors.New("GitHub 页面缺少仓库身份元数据")
	}
	if expectedFullName != "" && !strings.EqualFold(repo.FullName, expectedFullName) {
		return githubRepositorySnapshot{}, fmt.Errorf("GitHub 页面仓库不匹配: got %s want %s", repo.FullName, expectedFullName)
	}
	parts := strings.SplitN(repo.FullName, "/", 2)
	if len(parts) == 2 {
		repo.Owner.Login, repo.Name = parts[0], parts[1]
	}
	repo.Description = strings.TrimSuffix(repo.Description, " - "+repo.FullName)
	if !starsKnown {
		if match := githubStarsJSON.FindSubmatch(body); len(match) == 2 {
			repo.StargazersCount, _ = strconv.Atoi(string(match[1]))
			starsKnown = true
		}
	}
	if match := githubCreatedJSON.FindSubmatch(body); len(match) == 2 {
		if createdAt, err := time.Parse(time.RFC3339Nano, string(match[1])); err == nil {
			repo.CreatedAt = createdAt
			repo.createdAtKnown = true
		}
	}
	repo.forksKnown = forksKnown
	repo.openIssuesKnown = issuesKnown
	if !starsKnown {
		return githubRepositorySnapshot{}, errors.New("GitHub 页面缺少 star 计数")
	}
	return repo, nil
}

func parseGitHubCounter(value string) (int, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	if value == "" {
		return 0, false
	}
	count, err := strconv.Atoi(value)
	return count, err == nil
}

func (adapter *githubResearchAdapter) githubHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("Accept", "application/vnd.github+json")
	headers.Set("X-GitHub-Api-Version", "2022-11-28")
	if adapter.token != "" {
		headers.Set("Authorization", "Bearer "+adapter.token)
	}
	return headers
}

func githubRepositoryIdentity(query string) (string, string, string) {
	if match := githubURLPattern.FindStringSubmatch(query); len(match) == 3 {
		return match[1], strings.TrimSuffix(match[2], ".git"), ""
	}
	if match := repositoryPattern.FindStringSubmatch(query); len(match) == 3 {
		return match[1], strings.TrimSuffix(match[2], ".git"), ""
	}
	value := compactWhitespace(strings.Trim(githubNoisePattern.ReplaceAllString(query, " "), " ,.;:!?，。；：！？"))
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", "", ""
	}
	best := fields[0]
	for _, field := range fields[1:] {
		if len(field) > len(best) {
			best = field
		}
	}
	return "", "", strings.Trim(best, " ,.;:!?，。；：！？")
}

func githubKeywordVariants(keyword string) []string {
	kebab := strings.ToLower(camelBoundary.ReplaceAllString(keyword, "$1-$2"))
	lower := strings.ToLower(keyword)
	values := []string{keyword, kebab, strings.ReplaceAll(kebab, "-", " ")}
	if !strings.Contains(lower, "-") && len([]rune(lower)) >= 8 {
		for index := 3; index <= len(lower)-3 && index <= 12; index++ {
			values = append(values, lower[:index]+"-"+lower[index:])
		}
	}
	return uniqueStrings(values)
}

func normalizedRepositoryName(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(value)
}

func (adapter *financeResearchAdapter) Applicable(arguments researchArguments) bool {
	value := strings.ToLower(arguments.Query)
	for _, marker := range []string{"股票", "股价", "行情", "市值", "stock price", "share price", "ticker", "market cap"} {
		if strings.Contains(value, marker) {
			return adapterTypeApplicable(arguments, "market", true)
		}
	}
	return adapterTypeApplicable(arguments, "market", false)
}

func (adapter *financeResearchAdapter) Research(ctx context.Context, arguments researchArguments) ([]researchSource, error) {
	query := arguments.Query
	if arguments.Subject != "" {
		query = arguments.Subject
	}
	lookup, ticker := financeIdentity(query)
	if ticker == "" && adapter.wikidataURL != "" {
		ticker, _ = adapter.resolveWikidataTicker(ctx, lookup)
	}
	if ticker == "" {
		searchEndpoint := adapter.searchURL + "?" + url.Values{
			"q": {lookup}, "quotesCount": {"8"}, "newsCount": {"0"},
		}.Encode()
		var search struct {
			Quotes []struct {
				Symbol    string `json:"symbol"`
				ShortName string `json:"shortname"`
				LongName  string `json:"longname"`
				Exchange  string `json:"exchange"`
				QuoteType string `json:"quoteType"`
			} `json:"quotes"`
		}
		if err := getResearchJSON(ctx, adapter.client, searchEndpoint, nil, &search); err != nil {
			return nil, fmt.Errorf("股票代码解析失败: %w", err)
		}
		for _, quote := range search.Quotes {
			if strings.EqualFold(quote.QuoteType, "EQUITY") {
				ticker = quote.Symbol
				break
			}
		}
		if ticker == "" {
			return nil, fmt.Errorf("没有找到 %q 对应的股票代码", lookup)
		}
	}
	endpoint := fmt.Sprintf("%s/%s?range=5d&interval=1d", strings.TrimRight(adapter.chartBase, "/"), url.PathEscape(ticker))
	var chart struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol             string  `json:"symbol"`
					Currency           string  `json:"currency"`
					ExchangeName       string  `json:"exchangeName"`
					FullExchangeName   string  `json:"fullExchangeName"`
					InstrumentType     string  `json:"instrumentType"`
					Timezone           string  `json:"exchangeTimezoneName"`
					RegularMarketPrice float64 `json:"regularMarketPrice"`
					PreviousClose      float64 `json:"previousClose"`
					ChartPreviousClose float64 `json:"chartPreviousClose"`
					RegularMarketTime  int64   `json:"regularMarketTime"`
				} `json:"meta"`
			} `json:"result"`
			Error any `json:"error"`
		} `json:"chart"`
	}
	if err := getResearchJSON(ctx, adapter.client, endpoint, nil, &chart); err != nil {
		return nil, fmt.Errorf("股票行情读取失败: %w", err)
	}
	if len(chart.Chart.Result) == 0 {
		return nil, fmt.Errorf("股票 %s 没有行情数据", ticker)
	}
	meta := chart.Chart.Result[0].Meta
	previousClose := meta.PreviousClose
	if previousClose == 0 {
		previousClose = meta.ChartPreviousClose
	}
	change := meta.RegularMarketPrice - previousClose
	changePercent := 0.0
	if previousClose != 0 {
		changePercent = change / previousClose * 100
	}
	marketTime := time.Unix(meta.RegularMarketTime, 0).UTC()
	content := map[string]any{
		"symbol": meta.Symbol, "instrument_type": meta.InstrumentType,
		"exchange": meta.ExchangeName, "currency": meta.Currency, "exchange_timezone": meta.Timezone,
		"regular_market_price": meta.RegularMarketPrice, "previous_close": previousClose,
		"change": change, "change_percent": changePercent,
		"market_time_utc": marketTime.Format(time.RFC3339),
		"note":            "Yahoo Finance 聚合行情可能延迟；交易决策应再用交易所或券商数据核验。",
	}
	if meta.FullExchangeName != "" {
		content["exchange_name"] = meta.FullExchangeName
	}
	if location, err := time.LoadLocation(meta.Timezone); err == nil && meta.Timezone != "" {
		content["market_time_exchange_local"] = marketTime.In(location).Format(time.RFC3339)
		content["market_time_note"] = "market_time_utc 为 UTC；market_time_exchange_local 为 exchange_timezone 对应的当地时间。时区名称不是交易所名称，交易场所只根据 exchange/exchange_name 表述。"
	}
	encoded, _ := json.MarshalIndent(content, "", "  ")
	pageURL := strings.TrimRight(adapter.quoteBase, "/") + "/" + url.PathEscape(meta.Symbol)
	return []researchSource{{
		Title: meta.Symbol + " market quote", URL: pageURL, Domain: "finance.yahoo.com",
		Provider: adapter.Name(), Kind: "market_quote", ContentType: "application/json",
		Status: http.StatusOK, PublishedAt: marketTime.Format(time.RFC3339),
		RetrievedAt: time.Now().UTC().Format(time.RFC3339), Content: string(encoded),
	}}, nil
}

func (adapter *financeResearchAdapter) resolveWikidataTicker(ctx context.Context, lookup string) (string, error) {
	searchEndpoint := adapter.wikidataURL + "?" + url.Values{
		"action": {"wbsearchentities"}, "search": {lookup}, "language": {"zh"},
		"uselang": {"zh"}, "type": {"item"}, "limit": {"5"}, "format": {"json"},
	}.Encode()
	var search struct {
		Search []struct {
			ID string `json:"id"`
		} `json:"search"`
	}
	if err := getResearchJSON(ctx, adapter.client, searchEndpoint, nil, &search); err != nil {
		return "", err
	}
	for _, candidate := range search.Search {
		// Listed companies usually keep P249 (ticker symbol) as a qualifier of
		// P414 (stock exchange), not as a top-level claim. wbgetclaims also keeps
		// the response small; fetching every claim for a large company can be
		// several megabytes and is unnecessary here.
		if ticker, err := adapter.resolveWikidataClaimTicker(ctx, candidate.ID, "P414", true); err == nil && ticker != "" {
			return ticker, nil
		}
		if ticker, err := adapter.resolveWikidataClaimTicker(ctx, candidate.ID, "P249", false); err == nil && ticker != "" {
			return ticker, nil
		}
	}
	return "", fmt.Errorf("Wikidata 没有 %q 的 ticker claim", lookup)
}

func (adapter *financeResearchAdapter) resolveWikidataClaimTicker(ctx context.Context, entityID, property string, qualifier bool) (string, error) {
	endpoint := adapter.wikidataURL + "?" + url.Values{
		"action": {"wbgetclaims"}, "entity": {entityID}, "property": {property}, "format": {"json"},
	}.Encode()
	type snak struct {
		DataValue struct {
			Value any `json:"value"`
		} `json:"datavalue"`
	}
	var payload struct {
		Claims map[string][]struct {
			MainSnak   snak              `json:"mainsnak"`
			Qualifiers map[string][]snak `json:"qualifiers"`
		} `json:"claims"`
	}
	if err := getResearchJSON(ctx, adapter.client, endpoint, nil, &payload); err != nil {
		return "", err
	}
	for _, claim := range payload.Claims[property] {
		values := []snak{claim.MainSnak}
		if qualifier {
			values = claim.Qualifiers["P249"]
		}
		for _, value := range values {
			ticker, ok := value.DataValue.Value.(string)
			if !ok {
				continue
			}
			ticker = strings.ToUpper(strings.TrimSpace(ticker))
			if financeTickerValue.MatchString(ticker) {
				return ticker, nil
			}
		}
	}
	return "", fmt.Errorf("Wikidata entity %s 没有可用的 %s ticker", entityID, property)
}

func financeIdentity(query string) (string, string) {
	if ticker := tickerPattern.FindString(query); ticker != "" {
		return ticker, ticker
	}
	value := strings.ToLower(query)
	replacer := strings.NewReplacer(
		"股票", " ", "股价", " ", "行情", " ", "市值", " ", "现在", " ", "今天", " ",
		"多少", " ", "价格", " ", "查询", " ", "请问", " ", "的", " ", "啊", " ",
		"stock price", " ", "share price", " ", "ticker", " ", "market cap", " ",
	)
	return compactWhitespace(strings.Trim(replacer.Replace(value), " ,.;:!?，。；：！？")), ""
}

func getResearchJSON(ctx context.Context, client *http.Client, endpoint string, headers http.Header, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "EasyAgent/1.0 (+https://github.com/lakernote/easy-agent)")
	request.Header.Set("Accept", "application/json")
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := readBounded(response.Body, 1_024)
		detail := compactWhitespace(string(body))
		if detail != "" {
			detail, _ = truncateRunes(detail, 240)
		}
		return &researchHTTPError{
			StatusCode: response.StatusCode,
			RetryAfter: strings.TrimSpace(response.Header.Get("Retry-After")),
			Detail:     detail,
		}
	}
	return decodeBoundedJSON(response.Body, maxResearchSearchBytes, target)
}

func getResearchBody(ctx context.Context, client *http.Client, endpoint string, headers http.Header, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "EasyAgent/1.0 (+https://github.com/lakernote/easy-agent)")
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := readBounded(response.Body, 1_024)
		detail := compactWhitespace(string(body))
		if detail != "" {
			detail, _ = truncateRunes(detail, 240)
		}
		return nil, &researchHTTPError{
			StatusCode: response.StatusCode,
			RetryAfter: strings.TrimSpace(response.Header.Get("Retry-After")),
			Detail:     detail,
		}
	}
	return readBounded(response.Body, limit)
}
