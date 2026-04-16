package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"llama_shim/internal/llama"
)

const (
	BackendDisabled = "disabled"
	BackendSearXNG  = "searxng"

	defaultTimeout        = 10 * time.Second
	defaultMaxResults     = 10
	defaultFetchBodyBytes = 1 << 20
)

type Config struct {
	Backend   string
	BaseURL   string
	Timeout   time.Duration
	MaxResults int
}

type Provider interface {
	Search(ctx context.Context, request SearchRequest) (SearchResponse, error)
	OpenPage(ctx context.Context, rawURL string) (Page, error)
}

type ReadyChecker interface {
	CheckReady(ctx context.Context) error
}

type SearchRequest struct {
	Query      string
	MaxResults int
}

type SearchResponse struct {
	Results []SearchResult
}

type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

type Page struct {
	Title string
	Text  string
	URL   string
}

func NormalizeConfig(cfg Config) (Config, error) {
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	if cfg.Backend == "" {
		cfg.Backend = BackendDisabled
	}
	switch cfg.Backend {
	case BackendDisabled:
		cfg.BaseURL = ""
		cfg.Timeout = 0
		cfg.MaxResults = 0
		return cfg, nil
	case BackendSearXNG:
	default:
		return Config{}, fmt.Errorf("unsupported web_search backend %q", cfg.Backend)
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return Config{}, errors.New("web_search.base_url must not be empty when web_search.backend is enabled")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = defaultMaxResults
	}
	return cfg, nil
}

func NewProvider(cfg Config) (Provider, error) {
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	switch normalized.Backend {
	case BackendDisabled:
		return nil, nil
	case BackendSearXNG:
		return newSearXNGProvider(normalized), nil
	default:
		return nil, fmt.Errorf("unsupported web_search backend %q", normalized.Backend)
	}
}

type searXNGProvider struct {
	baseURL     string
	client      *http.Client
	fetchClient *http.Client
	maxResults  int
	resolveIP   func(ctx context.Context, host string) ([]net.IPAddr, error)
}

func newSearXNGProvider(cfg Config) *searXNGProvider {
	p := &searXNGProvider{
		baseURL: cfg.BaseURL,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		resolveIP: net.DefaultResolver.LookupIPAddr,
		maxResults: cfg.MaxResults,
	}
	p.fetchClient = &http.Client{
		Timeout: cfg.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if _, err := p.validateOpenPageURL(req.Context(), req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	return p
}

func (p *searXNGProvider) CheckReady(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/", nil)
	if err != nil {
		return fmt.Errorf("create web search readiness request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return mapHTTPError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return &llama.UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
		}
	}
	return nil
}

func (p *searXNGProvider) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return SearchResponse{}, nil
	}

	values := neturl.Values{}
	values.Set("q", query)
	values.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/search?"+values.Encode(), nil)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("create web search request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return SearchResponse{}, mapHTTPError(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return SearchResponse{}, fmt.Errorf("read web search response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SearchResponse{}, &llama.UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
		}
	}

	var payload struct {
		Results []struct {
			URL         string `json:"url"`
			Title       string `json:"title"`
			Content     string `json:"content"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return SearchResponse{}, &llama.InvalidResponseError{Message: "web search backend returned invalid JSON"}
	}

	limit := request.MaxResults
	if limit <= 0 || limit > p.maxResults {
		limit = p.maxResults
	}
	out := make([]SearchResult, 0, min(limit, len(payload.Results)))
	seen := make(map[string]struct{}, len(payload.Results))
	for _, result := range payload.Results {
		rawURL := strings.TrimSpace(result.URL)
		if rawURL == "" {
			continue
		}
		if _, ok := seen[rawURL]; ok {
			continue
		}
		seen[rawURL] = struct{}{}
		snippet := strings.TrimSpace(result.Content)
		if snippet == "" {
			snippet = strings.TrimSpace(result.Description)
		}
		out = append(out, SearchResult{
			Title:   strings.TrimSpace(result.Title),
			URL:     rawURL,
			Snippet: snippet,
		})
		if len(out) == limit {
			break
		}
	}
	return SearchResponse{Results: out}, nil
}

func (p *searXNGProvider) OpenPage(ctx context.Context, rawURL string) (Page, error) {
	parsed, err := p.validateOpenPageURL(ctx, rawURL)
	if err != nil {
		return Page{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Page{}, fmt.Errorf("create open_page request: %w", err)
	}
	resp, err := p.fetchClient.Do(req)
	if err != nil {
		return Page{}, mapHTTPError(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultFetchBodyBytes))
	if err != nil {
		return Page{}, fmt.Errorf("read open_page response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Page{}, &llama.UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
		}
	}

	pageURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		if _, err := p.validateOpenPageURL(ctx, resp.Request.URL.String()); err != nil {
			return Page{}, err
		}
		pageURL = resp.Request.URL.String()
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if strings.Contains(contentType, "text/plain") {
		return Page{
			Title: "",
			Text:  normalizeWhitespace(string(body)),
			URL:   pageURL,
		}, nil
	}

	title := extractHTMLTitle(string(body))
	return Page{
		Title: title,
		Text:  stripHTMLToText(string(body)),
		URL:   pageURL,
	}, nil
}

func mapHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &llama.TimeoutError{Message: "web search backend timeout"}
	case errors.As(err, &netErr) && netErr.Timeout():
		return &llama.TimeoutError{Message: "web search backend timeout"}
	default:
		return err
	}
}

func (p *searXNGProvider) validateOpenPageURL(ctx context.Context, rawURL string) (*neturl.URL, error) {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, &llama.InvalidResponseError{Message: "web search result URL was invalid"}
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https":
	default:
		return nil, &llama.InvalidResponseError{Message: "web search open_page supports only http and https URLs"}
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return nil, &llama.InvalidResponseError{Message: "web search result URL was missing a host"}
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return nil, &llama.InvalidResponseError{Message: "web search open_page rejected a local host"}
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedOpenPageIP(ip) {
			return nil, &llama.InvalidResponseError{Message: "web search open_page rejected a private IP"}
		}
		return parsed, nil
	}
	resolved, err := p.resolveIP(ctx, host)
	if err != nil {
		return nil, &llama.InvalidResponseError{Message: "web search open_page could not resolve URL host"}
	}
	if len(resolved) == 0 {
		return nil, &llama.InvalidResponseError{Message: "web search open_page could not resolve URL host"}
	}
	for _, addr := range resolved {
		if isBlockedOpenPageIP(addr.IP) {
			return nil, &llama.InvalidResponseError{Message: "web search open_page rejected a private IP"}
		}
	}
	return parsed, nil
}

func isBlockedOpenPageIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

var (
	htmlTitlePattern   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlScriptPattern  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlStylePattern   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagPattern     = regexp.MustCompile(`(?is)<[^>]+>`)
	spaceCollapseRegex = regexp.MustCompile(`[\t \f\v\r\n]+`)
)

func extractHTMLTitle(raw string) string {
	matches := htmlTitlePattern.FindStringSubmatch(raw)
	if len(matches) < 2 {
		return ""
	}
	return normalizeWhitespace(html.UnescapeString(matches[1]))
}

func stripHTMLToText(raw string) string {
	withoutScripts := htmlScriptPattern.ReplaceAllString(raw, " ")
	withoutStyles := htmlStylePattern.ReplaceAllString(withoutScripts, " ")
	withoutTags := htmlTagPattern.ReplaceAllString(withoutStyles, " ")
	return normalizeWhitespace(html.UnescapeString(withoutTags))
}

func normalizeWhitespace(text string) string {
	text = spaceCollapseRegex.ReplaceAllString(strings.TrimSpace(text), " ")
	if utf8Len(text) > 24000 {
		return trimRunes(text, 24000)
	}
	return text
}

func utf8Len(text string) int {
	return len([]rune(text))
}

func trimRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}
