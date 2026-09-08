package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const maxResearchSourceBytes = 4 * 1024 * 1024

type researchFetcher struct {
	client       *http.Client
	readerClient *http.Client
	readerURL    string
	readerKey    string
}

type researchResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func newResearchFetcher() *researchFetcher {
	return &researchFetcher{
		client:       safeResearchHTTPClient(18 * time.Second),
		readerClient: &http.Client{Timeout: 25 * time.Second},
		readerURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("EASYAGENT_READER_URL")), "/"),
		readerKey:    strings.TrimSpace(os.Getenv("EASYAGENT_READER_API_KEY")),
	}
}

func (fetcher *researchFetcher) Fetch(ctx context.Context, candidate researchSearchResult, query string, maxChars int) researchSource {
	target := canonicalResearchURL(candidate.URL)
	source := researchSource{
		Title: candidate.Title, URL: target, Domain: researchDomain(target),
		Provider: strings.Join(candidate.Providers, ","), Kind: "web_page", Rank: candidate.Rank,
		RetrievedAt: time.Now().UTC().Format(time.RFC3339),
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		source.Error = "来源 URL 无效"
		return source
	}
	if unsafeResearchHost(parsed.Hostname()) {
		source.Error = "拒绝读取本地或私有网络地址"
		return source
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		source.Error = "创建来源请求失败"
		return source
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; EasyAgent/1.0; +https://github.com/lakernote/easy-agent)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain,application/xml,application/pdf;q=0.8,*/*;q=0.1")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	response, err := fetcher.client.Do(request)
	if err != nil {
		source.Error = "读取来源失败: " + compactResearchError(err)
		return source
	}
	defer response.Body.Close()
	source.Status = response.StatusCode
	if response.Request != nil {
		source.URL = canonicalResearchURL(response.Request.URL.String())
		source.Domain = researchDomain(source.URL)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		source.Error = fmt.Sprintf("来源返回 HTTP %d", response.StatusCode)
		return source
	}
	body, err := readBounded(response.Body, maxResearchSourceBytes)
	if err != nil {
		source.Error = "读取来源正文失败: " + compactResearchError(err)
		return source
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	source.ContentType = mediaType
	if (mediaType == "" || mediaType == "text/html" || mediaType == "application/xhtml+xml") && isSearchChallenge(body) {
		source.Error = "来源返回了人机验证页，已尝试后续候选"
		return source
	}

	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		var value any
		if err := json.Unmarshal(body, &value); err != nil {
			source.Error = "JSON 来源格式无效"
			return source
		}
		formatted, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			source.Error = "JSON 来源无法整理"
			return source
		}
		source.Content, source.Truncated = truncateRunes(string(formatted), maxChars)
	case mediaType == "application/pdf":
		source.Kind = "pdf"
		if value, readerErr := fetcher.fetchViaReader(ctx, source, query, maxChars); readerErr == nil {
			return value
		}
		source.Error = "PDF 来源需要配置 EASYAGENT_READER_URL 或 PDF 提取服务，已尝试后续候选"
		return source
	case mediaType == "" || mediaType == "text/html" || mediaType == "application/xhtml+xml":
		document, err := extractResearchHTML(body, contentType)
		if err != nil {
			source.Error = "HTML 来源无法解析"
			return source
		}
		if document.Title != "" {
			source.Title = document.Title
		}
		source.PublishedAt = document.PublishedAt
		source.Content, source.Truncated = selectRelevantPassages(query, document.Paragraphs, maxChars)
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/xml" || strings.HasSuffix(mediaType, "+xml"):
		reader, err := charset.NewReader(bytes.NewReader(body), contentType)
		if err != nil {
			source.Error = "来源字符集无法解码"
			return source
		}
		decoded, err := readBounded(reader, maxResearchSourceBytes)
		if err != nil {
			source.Error = "来源文本无法读取"
			return source
		}
		source.Content, source.Truncated = selectRelevantPassages(query, splitResearchParagraphs(string(decoded)), maxChars)
	default:
		source.Error = fmt.Sprintf("不支持的来源内容类型 %q", contentType)
		return source
	}
	if strings.TrimSpace(source.Content) == "" {
		if value, readerErr := fetcher.fetchViaReader(ctx, source, query, maxChars); readerErr == nil {
			return value
		}
		source.Error = "来源没有提取到可读正文"
	}
	return source
}

func (fetcher *researchFetcher) fetchViaReader(ctx context.Context, source researchSource, query string, maxChars int) (researchSource, error) {
	if fetcher.readerURL == "" || fetcher.readerClient == nil {
		return source, errors.New("未配置 reader 服务")
	}
	endpoint := fetcher.readerURL + "/" + source.URL
	if strings.Contains(fetcher.readerURL, "{url}") {
		endpoint = strings.ReplaceAll(fetcher.readerURL, "{url}", url.QueryEscape(source.URL))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return source, err
	}
	request.Header.Set("Accept", "text/plain,text/markdown")
	request.Header.Set("User-Agent", "EasyAgent/1.0")
	request.Header.Set("X-Return-Format", "markdown")
	if fetcher.readerKey != "" {
		request.Header.Set("Authorization", "Bearer "+fetcher.readerKey)
	}
	response, err := fetcher.readerClient.Do(request)
	if err != nil {
		return source, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return source, fmt.Errorf("reader HTTP %d", response.StatusCode)
	}
	body, err := readBounded(response.Body, maxResearchSourceBytes)
	if err != nil {
		return source, err
	}
	content, truncated := selectRelevantPassages(query, splitResearchParagraphs(string(body)), maxChars)
	if strings.TrimSpace(content) == "" {
		return source, errors.New("reader 没有返回正文")
	}
	source.Provider = strings.Join(uniqueStrings(append(strings.Split(source.Provider, ","), "reader_service")), ",")
	source.Kind = "reader_document"
	source.ContentType = "text/markdown"
	source.Content = content
	source.Truncated = truncated
	source.Error = ""
	return source, nil
}

func safeResearchHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// HTTP_PROXY 会绕过 origin DNS 校验；联网研究默认直连并在 DialContext
	// 中解析、校验和固定目标 IP，防止 hostname 私网映射和 DNS rebinding。
	transport.Proxy = nil
	transport.DialContext = safeResearchDialContext(&net.Resolver{}, &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second})
	transport.MaxResponseHeaderBytes = 1 << 20
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.IdleConnTimeout = 45 * time.Second
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("来源跳转次数超过 5 次")
			}
			if request.URL.User != nil || (request.URL.Scheme != "http" && request.URL.Scheme != "https") || unsafeResearchHost(request.URL.Hostname()) {
				return errors.New("拒绝跳转到不安全地址")
			}
			return nil
		},
	}
}

func safeResearchDialContext(resolver researchResolver, dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("解析目标地址: %w", err)
		}
		if unsafeResearchHost(host) {
			return nil, errors.New("拒绝连接本地或私有网络地址")
		}
		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("解析来源域名: %w", err)
		}
		if len(addresses) == 0 {
			return nil, errors.New("来源域名没有可用 IP")
		}
		for _, address := range addresses {
			if unsafeResearchIP(address.IP) {
				return nil, fmt.Errorf("来源域名解析到不安全 IP %s", address.IP)
			}
		}
		failures := make([]error, 0, len(addresses))
		for _, resolved := range addresses {
			if network == "tcp4" && resolved.IP.To4() == nil {
				continue
			}
			if network == "tcp6" && resolved.IP.To4() != nil {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			failures = append(failures, dialErr)
		}
		if len(failures) == 0 {
			failures = append(failures, errors.New("没有与网络类型匹配的公开 IP"))
		}
		return nil, errors.Join(failures...)
	}
}

func unsafeResearchHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".home.arpa") {
		return true
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return unsafeResearchIP(parsed)
	}
	return false
}

func unsafeResearchIP(value net.IP) bool {
	return value == nil || value.IsPrivate() || value.IsLoopback() || value.IsLinkLocalUnicast() ||
		value.IsLinkLocalMulticast() || value.IsInterfaceLocalMulticast() || value.IsUnspecified() ||
		value.IsMulticast()
}

type researchHTMLDocument struct {
	Title       string
	PublishedAt string
	Paragraphs  []string
}

func extractResearchHTML(body []byte, contentType string) (researchHTMLDocument, error) {
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return researchHTMLDocument{}, err
	}
	document, err := xhtml.Parse(reader)
	if err != nil {
		return researchHTMLDocument{}, err
	}
	result := researchHTMLDocument{}
	var contentRoots []*xhtml.Node
	walkHTML(document, func(node *xhtml.Node) bool {
		if node.Type != xhtml.ElementNode {
			return true
		}
		switch node.Data {
		case "title":
			if result.Title == "" {
				result.Title = visibleHTMLText(node)
			}
		case "meta":
			name := strings.ToLower(strings.TrimSpace(firstNonEmpty(htmlAttribute(node, "property"), htmlAttribute(node, "name"), htmlAttribute(node, "itemprop"))))
			content := compactWhitespace(stdhtml.UnescapeString(htmlAttribute(node, "content")))
			if result.Title == "" && (name == "og:title" || name == "twitter:title") {
				result.Title = content
			}
			if result.PublishedAt == "" && containsString([]string{"article:published_time", "date", "datepublished", "publishdate", "pubdate"}, name) {
				result.PublishedAt = content
			}
		case "time":
			if result.PublishedAt == "" {
				result.PublishedAt = compactWhitespace(firstNonEmpty(htmlAttribute(node, "datetime"), visibleHTMLText(node)))
			}
		case "main", "article":
			contentRoots = append(contentRoots, node)
		default:
			if strings.EqualFold(htmlAttribute(node, "role"), "main") {
				contentRoots = append(contentRoots, node)
			}
		}
		return true
	})
	root := document
	bestLength := 0
	for _, candidate := range contentRoots {
		length := len([]rune(visibleHTMLText(candidate)))
		if length > bestLength {
			root = candidate
			bestLength = length
		}
	}
	result.Paragraphs = htmlParagraphs(root)
	if len(result.Paragraphs) == 0 {
		if text := visibleHTMLText(root); text != "" {
			result.Paragraphs = []string{text}
		}
	}
	return result, nil
}

func htmlParagraphs(root *xhtml.Node) []string {
	result := make([]string, 0, 64)
	blockTags := map[string]bool{
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"p": true, "li": true, "blockquote": true, "pre": true, "td": true, "th": true,
	}
	var visit func(*xhtml.Node, bool)
	visit = func(node *xhtml.Node, hidden bool) {
		if node == nil {
			return
		}
		hidden = hidden || hiddenResearchHTMLNode(node)
		if hidden {
			return
		}
		if node.Type == xhtml.ElementNode && blockTags[node.Data] {
			if value := visibleHTMLText(node); len([]rune(value)) >= 20 {
				result = append(result, value)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, hidden)
		}
	}
	visit(root, false)
	return uniqueResearchParagraphs(result)
}

func visibleHTMLText(root *xhtml.Node) string {
	var builder strings.Builder
	var visit func(*xhtml.Node, bool)
	visit = func(node *xhtml.Node, hidden bool) {
		if node == nil {
			return
		}
		hidden = hidden || hiddenResearchHTMLNode(node)
		if hidden {
			return
		}
		if node.Type == xhtml.TextNode {
			builder.WriteString(node.Data)
			builder.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, hidden)
		}
	}
	visit(root, false)
	return compactWhitespace(stdhtml.UnescapeString(builder.String()))
}

func hiddenResearchHTMLNode(node *xhtml.Node) bool {
	if node.Type != xhtml.ElementNode {
		return false
	}
	switch node.Data {
	case "script", "style", "noscript", "svg", "canvas", "nav", "footer", "header", "form", "aside":
		return true
	}
	style := strings.ToLower(htmlAttribute(node, "style"))
	return htmlAttribute(node, "hidden") != "" || strings.Contains(style, "display:none") || strings.Contains(style, "display: none")
}

type scoredResearchPassage struct {
	index int
	score int
	text  string
}

func selectRelevantPassages(query string, paragraphs []string, limit int) (string, bool) {
	paragraphs = uniqueResearchParagraphs(paragraphs)
	if len(paragraphs) == 0 || limit <= 0 {
		return "", false
	}
	terms := researchTerms(query)
	scored := make([]scoredResearchPassage, 0, len(paragraphs))
	totalChars := 0
	for index, paragraph := range paragraphs {
		paragraph = compactWhitespace(paragraph)
		if paragraph == "" {
			continue
		}
		totalChars += len([]rune(paragraph))
		lower := strings.ToLower(paragraph)
		score := 0
		for _, term := range terms {
			count := strings.Count(lower, term)
			if count > 0 {
				score += count * min(len([]rune(term)), 8)
			}
		}
		if index < 3 {
			score += 2
		}
		scored = append(scored, scoredResearchPassage{index: index, score: score, text: paragraph})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].index < scored[j].index
	})
	selected := make([]scoredResearchPassage, 0, len(scored))
	used := 0
	for _, passage := range scored {
		text, shortened := truncateRunes(passage.text, min(limit, 1_200))
		added := len([]rune(text))
		if used > 0 {
			added += 2
		}
		if used+added > limit {
			continue
		}
		selected = append(selected, scoredResearchPassage{index: passage.index, score: passage.score, text: text})
		used += added
		if used >= limit-100 || (shortened && used > limit/2) {
			break
		}
	}
	if len(selected) == 0 {
		value, truncated := truncateRunes(paragraphs[0], limit)
		return value, truncated
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].index < selected[j].index })
	values := make([]string, 0, len(selected))
	for _, passage := range selected {
		values = append(values, passage.text)
	}
	return strings.Join(values, "\n\n"), used < totalChars
}

func researchTerms(query string) []string {
	query = strings.ToLower(query)
	terms := make([]string, 0, 48)
	var token []rune
	flush := func() {
		if len(token) == 0 {
			return
		}
		value := string(token)
		if len(token) >= 2 {
			terms = append(terms, englishResearchTermVariants(value)...)
		}
		if hasHanRune(token) {
			for index := 0; index+1 < len(token) && len(terms) < 64; index++ {
				terms = append(terms, string(token[index:index+2]))
			}
		}
		token = token[:0]
	}
	for _, value := range []rune(query) {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			token = append(token, value)
		} else {
			flush()
		}
	}
	flush()
	return uniqueStrings(terms)
}

func englishResearchTermVariants(value string) []string {
	result := []string{value}
	if hasHanRune([]rune(value)) {
		return result
	}
	// Lightweight variants are enough for passage ranking and avoid pulling a
	// language stemmer into the runtime. Keep stems reasonably long so short
	// query words do not create broad accidental matches.
	for _, suffix := range []string{"ations", "ation", "ments", "ment", "ing", "ies", "es", "s"} {
		if strings.HasSuffix(value, suffix) && len(value)-len(suffix) >= 5 {
			stem := strings.TrimSuffix(value, suffix)
			if suffix == "ations" || suffix == "ation" {
				stem += "a"
			} else if suffix == "ies" {
				stem += "y"
			}
			result = append(result, stem)
			break
		}
	}
	return result
}

func hasHanRune(values []rune) bool {
	for _, value := range values {
		if unicode.Is(unicode.Han, value) {
			return true
		}
	}
	return false
}

func uniqueResearchParagraphs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = compactWhitespace(value)
		key := strings.ToLower(value)
		if len([]rune(value)) < 20 {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func splitResearchParagraphs(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	parts := strings.Split(value, "\n")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = compactWhitespace(part); len([]rune(part)) >= 20 {
			result = append(result, part)
		}
	}
	if len(result) == 0 && strings.TrimSpace(value) != "" {
		result = []string{compactWhitespace(value)}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// 兼容原有单元测试和包内调用；实现已经改为 DOM 解析，不再使用 HTML 正则。
func pageMetadata(value, fallbackTitle string) (string, string) {
	document, err := extractResearchHTML([]byte(value), "text/html; charset=utf-8")
	if err != nil {
		return fallbackTitle, ""
	}
	return firstNonEmpty(document.Title, fallbackTitle), document.PublishedAt
}

func mainOrArticleHTML(value string) string {
	document, err := xhtml.Parse(strings.NewReader(value))
	if err != nil {
		return value
	}
	root := document
	bestLength := 0
	walkHTML(document, func(node *xhtml.Node) bool {
		if node.Type == xhtml.ElementNode && (node.Data == "main" || node.Data == "article" || strings.EqualFold(htmlAttribute(node, "role"), "main")) {
			if length := len([]rune(visibleHTMLText(node))); length > bestLength {
				root = node
				bestLength = length
			}
		}
		return true
	})
	var builder strings.Builder
	if err := xhtml.Render(&builder, root); err != nil {
		return value
	}
	return builder.String()
}

func cleanHTMLText(value string) string {
	document, err := xhtml.Parse(strings.NewReader(value))
	if err != nil {
		return compactWhitespace(stdhtml.UnescapeString(value))
	}
	return visibleHTMLText(document)
}
