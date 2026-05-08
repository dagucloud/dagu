// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/llm"
)

const (
	webSearchToolName  = "web_search"
	webExtractToolName = "web_extract"

	// DefaultTavilyBaseURL is the default Tavily API origin.
	DefaultTavilyBaseURL = "https://api.tavily.com"
	// DefaultTavilySearchLimit is the per-call search result count when the model does not request one.
	DefaultTavilySearchLimit = 5
	// MaxTavilySearchLimit is Tavily's maximum supported max_results value for this tool.
	MaxTavilySearchLimit = 20
	// DefaultTavilySearchDepth is the default Tavily search_depth value.
	DefaultTavilySearchDepth = "basic"

	// DefaultFirecrawlBaseURL is the default Firecrawl API origin.
	DefaultFirecrawlBaseURL = "https://api.firecrawl.dev"
	// DefaultFirecrawlSearchLimit is the per-call search result count when the model does not request one.
	DefaultFirecrawlSearchLimit = 5
	// MaxFirecrawlSearchLimit is Firecrawl's maximum supported limit value for this tool.
	MaxFirecrawlSearchLimit = 100

	defaultWebProviderHTTPTimeout = 60 * time.Second
	maxWebToolResponseBytes       = 2 * 1024 * 1024
	maxWebToolOutputBytes         = 100000
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           webSearchToolName,
		Label:          "Web Search",
		Description:    "Search the web with a configured backend",
		DefaultEnabled: true,
		Factory: func(cfg ToolConfig) *AgentTool {
			if cfg.WebTools == nil {
				return nil
			}
			return NewWebSearchTool(*cfg.WebTools)
		},
	})
	RegisterTool(ToolRegistration{
		Name:           webExtractToolName,
		Label:          "Web Extract",
		Description:    "Extract readable content from web pages with a configured backend",
		DefaultEnabled: true,
		Factory: func(cfg ToolConfig) *AgentTool {
			if cfg.WebTools == nil {
				return nil
			}
			return NewWebExtractTool(*cfg.WebTools)
		},
	})
}

// WebSearchToolInput defines the input parameters for web_search.
type WebSearchToolInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// WebExtractToolInput defines the input parameters for web_extract.
type WebExtractToolInput struct {
	URLs []string `json:"urls"`
}

type webToolProvider interface {
	search(ctx context.Context, query string, limit int) (hermesSearchOutput, error)
	extract(ctx context.Context, urls []string) (hermesExtractOutput, error)
	defaultSearchLimit() int
	maxSearchLimit() int
}

type tavilyClient struct {
	baseURL     string
	apiKey      string
	maxResults  int
	searchDepth string
	httpClient  *http.Client
}

type tavilySearchRequest struct {
	Query             string `json:"query"`
	MaxResults        int    `json:"max_results"`
	SearchDepth       string `json:"search_depth,omitempty"`
	IncludeRawContent bool   `json:"include_raw_content"`
	IncludeImages     bool   `json:"include_images"`
}

type tavilySearchResponse struct {
	Results []tavilySearchResult `json:"results"`
}

type tavilySearchResult struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Content    string `json:"content"`
	RawContent string `json:"raw_content"`
}

type tavilyExtractRequest struct {
	URLs          []string `json:"urls"`
	IncludeImages bool     `json:"include_images"`
}

type tavilyExtractResponse struct {
	Results       []tavilyExtractResult       `json:"results"`
	FailedResults []tavilyExtractFailedResult `json:"failed_results"`
}

type tavilyExtractResult struct {
	URL        string         `json:"url"`
	Title      string         `json:"title"`
	Content    string         `json:"content"`
	RawContent string         `json:"raw_content"`
	Metadata   map[string]any `json:"metadata"`
}

type tavilyExtractFailedResult struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

type firecrawlClient struct {
	baseURL    string
	apiKey     string
	maxResults int
	httpClient *http.Client
}

type firecrawlSearchRequest struct {
	Query   string   `json:"query"`
	Limit   int      `json:"limit"`
	Sources []string `json:"sources,omitempty"`
}

type firecrawlSearchResponse struct {
	Success bool                `json:"success"`
	Data    firecrawlSearchData `json:"data"`
	Warning string              `json:"warning"`
}

type firecrawlSearchData struct {
	Web []firecrawlSearchResult `json:"web"`
}

type firecrawlSearchResult struct {
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Description string         `json:"description"`
	Markdown    string         `json:"markdown"`
	Metadata    map[string]any `json:"metadata"`
}

type firecrawlScrapeRequest struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats"`
	OnlyMainContent bool     `json:"onlyMainContent"`
}

type firecrawlScrapeResponse struct {
	Success bool                `json:"success"`
	Data    firecrawlScrapeData `json:"data"`
	Error   string              `json:"error"`
}

type firecrawlScrapeData struct {
	Markdown string         `json:"markdown"`
	Summary  string         `json:"summary"`
	HTML     string         `json:"html"`
	Metadata map[string]any `json:"metadata"`
}

type hermesSearchOutput struct {
	Success bool             `json:"success"`
	Data    hermesSearchData `json:"data"`
}

type hermesSearchData struct {
	Web []hermesWebResult `json:"web"`
}

type hermesWebResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Position    int    `json:"position"`
}

type hermesExtractOutput struct {
	Results []hermesExtractResult `json:"results"`
}

type hermesExtractResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// NewWebSearchTool creates a provider-backed web_search tool.
func NewWebSearchTool(cfg WebToolsConfig) *AgentTool {
	cfg = ResolveWebToolsConfig(cfg)
	provider, ok := newConfiguredWebToolProvider(cfg)
	if !ok {
		return nil
	}
	defaultLimit := provider.defaultSearchLimit()
	maxLimit := provider.maxSearchLimit()
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        webSearchToolName,
				Description: "Search the public web for current information. Use this when the answer depends on recent or external information.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search query",
						},
						"limit": map[string]any{
							"type":        "integer",
							"minimum":     1,
							"maximum":     maxLimit,
							"description": fmt.Sprintf("Maximum number of results to return (default: %d, max: %d)", defaultLimit, maxLimit),
						},
					},
					"required": []string{"query"},
				},
			},
		},
		Run: func(toolCtx ToolContext, input json.RawMessage) ToolOut {
			return webSearchRun(toolCtx, input, provider)
		},
		Audit: &AuditInfo{
			Action:          "web_search",
			DetailExtractor: ExtractFields("query", "limit"),
		},
	}
}

// NewWebExtractTool creates a provider-backed web_extract tool.
func NewWebExtractTool(cfg WebToolsConfig) *AgentTool {
	cfg = ResolveWebToolsConfig(cfg)
	provider, ok := newConfiguredWebToolProvider(cfg)
	if !ok {
		return nil
	}
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        webExtractToolName,
				Description: "Extract readable text content from public web page URLs.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"urls": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"minItems":    1,
							"maxItems":    10,
							"description": "HTTP or HTTPS URLs to extract",
						},
					},
					"required": []string{"urls"},
				},
			},
		},
		Run: func(toolCtx ToolContext, input json.RawMessage) ToolOut {
			return webExtractRun(toolCtx, input, provider)
		},
		Audit: &AuditInfo{
			Action:          "web_extract",
			DetailExtractor: ExtractFields("urls"),
		},
	}
}

func newConfiguredWebToolProvider(cfg WebToolsConfig) (webToolProvider, bool) {
	cfg = ResolveWebToolsConfig(cfg)
	if !cfg.Enabled {
		return nil, false
	}
	switch cfg.Backend {
	case WebToolsBackendTavily:
		if cfg.Tavily == nil || strings.TrimSpace(cfg.Tavily.APIKey) == "" {
			return nil, false
		}
		return &tavilyClient{
			baseURL:     strings.TrimRight(cfg.Tavily.BaseURL, "/"),
			apiKey:      strings.TrimSpace(cfg.Tavily.APIKey),
			maxResults:  cfg.Tavily.MaxResults,
			searchDepth: cfg.Tavily.SearchDepth,
			httpClient: &http.Client{
				Timeout: defaultWebProviderHTTPTimeout,
			},
		}, true
	case WebToolsBackendFirecrawl:
		if cfg.Firecrawl == nil || strings.TrimSpace(cfg.Firecrawl.APIKey) == "" {
			return nil, false
		}
		return &firecrawlClient{
			baseURL:    strings.TrimRight(cfg.Firecrawl.BaseURL, "/"),
			apiKey:     strings.TrimSpace(cfg.Firecrawl.APIKey),
			maxResults: cfg.Firecrawl.MaxResults,
			httpClient: &http.Client{
				Timeout: defaultWebProviderHTTPTimeout,
			},
		}, true
	default:
		return nil, false
	}
}

// ResolveWebToolsConfig applies runtime defaults and trimming for web tool config.
func ResolveWebToolsConfig(cfg WebToolsConfig) WebToolsConfig {
	cfg = *cloneWebToolsConfig(&cfg)
	if cfg.Backend == "" {
		cfg.Backend = WebToolsBackendTavily
	}
	switch cfg.Backend {
	case WebToolsBackendTavily:
		if cfg.Tavily == nil {
			cfg.Tavily = &TavilyWebToolsConfig{}
		}
		cfg.Tavily.APIKey = strings.TrimSpace(cfg.Tavily.APIKey)
		cfg.Tavily.BaseURL = strings.TrimSpace(cfg.Tavily.BaseURL)
		if cfg.Tavily.BaseURL == "" {
			cfg.Tavily.BaseURL = DefaultTavilyBaseURL
		}
		cfg.Tavily.BaseURL = strings.TrimRight(cfg.Tavily.BaseURL, "/")
		if cfg.Tavily.MaxResults <= 0 {
			cfg.Tavily.MaxResults = MaxTavilySearchLimit
		}
		if cfg.Tavily.MaxResults > MaxTavilySearchLimit {
			cfg.Tavily.MaxResults = MaxTavilySearchLimit
		}
		cfg.Tavily.SearchDepth = strings.TrimSpace(cfg.Tavily.SearchDepth)
		if cfg.Tavily.SearchDepth == "" {
			cfg.Tavily.SearchDepth = DefaultTavilySearchDepth
		}
		if !isValidTavilySearchDepth(cfg.Tavily.SearchDepth) {
			cfg.Tavily.SearchDepth = DefaultTavilySearchDepth
		}
	case WebToolsBackendFirecrawl:
		if cfg.Firecrawl == nil {
			cfg.Firecrawl = &FirecrawlWebToolsConfig{}
		}
		cfg.Firecrawl.APIKey = strings.TrimSpace(cfg.Firecrawl.APIKey)
		cfg.Firecrawl.BaseURL = strings.TrimSpace(cfg.Firecrawl.BaseURL)
		if cfg.Firecrawl.BaseURL == "" {
			cfg.Firecrawl.BaseURL = DefaultFirecrawlBaseURL
		}
		cfg.Firecrawl.BaseURL = strings.TrimRight(cfg.Firecrawl.BaseURL, "/")
		if cfg.Firecrawl.MaxResults <= 0 {
			cfg.Firecrawl.MaxResults = MaxFirecrawlSearchLimit
		}
		if cfg.Firecrawl.MaxResults > MaxFirecrawlSearchLimit {
			cfg.Firecrawl.MaxResults = MaxFirecrawlSearchLimit
		}
	}
	return cfg
}

// ValidateWebToolsConfig validates operator-provided web tool config before it is stored.
func ValidateWebToolsConfig(cfg WebToolsConfig) error {
	backend := cfg.Backend
	if backend == "" {
		backend = WebToolsBackendTavily
	}
	if backend != WebToolsBackendTavily && backend != WebToolsBackendFirecrawl {
		return fmt.Errorf("unsupported web tools backend")
	}
	if cfg.Tavily != nil {
		if _, err := ValidateTavilyBaseURL(cfg.Tavily.BaseURL); err != nil {
			return fmt.Errorf("webTools.tavily.baseUrl %w", err)
		}
		if cfg.Tavily.MaxResults != 0 && (cfg.Tavily.MaxResults < 1 || cfg.Tavily.MaxResults > MaxTavilySearchLimit) {
			return fmt.Errorf("webTools.tavily.maxResults must be between 1 and %d", MaxTavilySearchLimit)
		}
		depth := strings.TrimSpace(cfg.Tavily.SearchDepth)
		if depth != "" && !isValidTavilySearchDepth(depth) {
			return fmt.Errorf("webTools.tavily.searchDepth must be advanced, basic, fast, or ultra-fast")
		}
	}
	if cfg.Firecrawl != nil {
		if _, err := ValidateFirecrawlBaseURL(cfg.Firecrawl.BaseURL); err != nil {
			return fmt.Errorf("webTools.firecrawl.baseUrl %w", err)
		}
		if cfg.Firecrawl.MaxResults != 0 && (cfg.Firecrawl.MaxResults < 1 || cfg.Firecrawl.MaxResults > MaxFirecrawlSearchLimit) {
			return fmt.Errorf("webTools.firecrawl.maxResults must be between 1 and %d", MaxFirecrawlSearchLimit)
		}
	}
	return nil
}

// ValidateTavilyBaseURL validates and normalizes an optional Tavily-compatible base URL.
func ValidateTavilyBaseURL(rawURL string) (string, error) {
	return validateWebProviderBaseURL(rawURL)
}

func isValidTavilySearchDepth(depth string) bool {
	switch depth {
	case "advanced", "basic", "fast", "ultra-fast":
		return true
	default:
		return false
	}
}

// ValidateFirecrawlBaseURL validates and normalizes an optional Firecrawl-compatible base URL.
func ValidateFirecrawlBaseURL(rawURL string) (string, error) {
	return validateWebProviderBaseURL(rawURL)
}

func validateWebProviderBaseURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("must use http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("must not include embedded credentials")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("host is required")
	}
	if isBlockedInternalHost(parsed.Hostname()) {
		return "", fmt.Errorf("must not target private or internal hosts")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must not include query or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func webSearchRun(toolCtx ToolContext, input json.RawMessage, provider webToolProvider) ToolOut {
	var args WebSearchToolInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return toolError("Query is required")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = provider.defaultSearchLimit()
	}
	limit = min(max(limit, 1), provider.maxSearchLimit())

	out, err := provider.search(toolCtx.Context, query, limit)
	if err != nil {
		return toolError("Web search failed: %v", err)
	}

	return jsonToolOutput(out)
}

func webExtractRun(toolCtx ToolContext, input json.RawMessage, provider webToolProvider) ToolOut {
	var args WebExtractToolInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}
	if len(args.URLs) == 0 {
		return toolError("At least one URL is required")
	}
	if len(args.URLs) > 10 {
		return toolError("Too many URLs: maximum is 10")
	}
	urls := make([]string, 0, len(args.URLs))
	for _, rawURL := range args.URLs {
		cleanURL, err := validateExtractURL(rawURL)
		if err != nil {
			return toolError("blocked URL %q: %v", rawURL, err)
		}
		urls = append(urls, cleanURL)
	}

	out, err := provider.extract(toolCtx.Context, urls)
	if err != nil {
		return toolError("Web extract failed: %v", err)
	}

	return jsonToolOutput(out)
}

func (c *tavilyClient) post(ctx context.Context, path string, payload any, out any) error {
	return postWebProviderJSON(ctx, c.httpClient, "tavily", c.baseURL, path, c.apiKey, payload, out)
}

func (c *tavilyClient) defaultSearchLimit() int {
	return min(DefaultTavilySearchLimit, c.maxSearchLimit())
}

func (c *tavilyClient) maxSearchLimit() int {
	if c.maxResults <= 0 {
		return MaxTavilySearchLimit
	}
	return c.maxResults
}

func (c *tavilyClient) search(ctx context.Context, query string, limit int) (hermesSearchOutput, error) {
	var resp tavilySearchResponse
	if err := c.post(ctx, "/search", tavilySearchRequest{
		Query:             query,
		MaxResults:        limit,
		SearchDepth:       c.searchDepth,
		IncludeRawContent: false,
		IncludeImages:     false,
	}, &resp); err != nil {
		return hermesSearchOutput{}, err
	}
	return normalizeTavilySearch(resp), nil
}

func (c *tavilyClient) extract(ctx context.Context, urls []string) (hermesExtractOutput, error) {
	var resp tavilyExtractResponse
	if err := c.post(ctx, "/extract", tavilyExtractRequest{
		URLs:          urls,
		IncludeImages: false,
	}, &resp); err != nil {
		return hermesExtractOutput{}, err
	}
	return normalizeTavilyExtract(resp), nil
}

func (c *firecrawlClient) post(ctx context.Context, path string, payload any, out any) error {
	return postWebProviderJSON(ctx, c.httpClient, "firecrawl", c.baseURL, path, c.apiKey, payload, out)
}

func (c *firecrawlClient) defaultSearchLimit() int {
	return min(DefaultFirecrawlSearchLimit, c.maxSearchLimit())
}

func (c *firecrawlClient) maxSearchLimit() int {
	if c.maxResults <= 0 {
		return MaxFirecrawlSearchLimit
	}
	return c.maxResults
}

func (c *firecrawlClient) search(ctx context.Context, query string, limit int) (hermesSearchOutput, error) {
	var resp firecrawlSearchResponse
	if err := c.post(ctx, "/v2/search", firecrawlSearchRequest{
		Query:   query,
		Limit:   limit,
		Sources: []string{"web"},
	}, &resp); err != nil {
		return hermesSearchOutput{}, err
	}
	return normalizeFirecrawlSearch(resp), nil
}

func (c *firecrawlClient) extract(ctx context.Context, urls []string) (hermesExtractOutput, error) {
	results := make([]hermesExtractResult, 0, len(urls))
	for _, itemURL := range urls {
		var resp firecrawlScrapeResponse
		err := c.post(ctx, "/v2/scrape", firecrawlScrapeRequest{
			URL:             itemURL,
			Formats:         []string{"markdown"},
			OnlyMainContent: true,
		}, &resp)
		if err != nil {
			results = append(results, hermesExtractResult{
				URL:   itemURL,
				Error: err.Error(),
			})
			continue
		}
		if !resp.Success {
			results = append(results, hermesExtractResult{
				URL:   itemURL,
				Error: cmpNonEmpty(resp.Error, "firecrawl scrape failed"),
			})
			continue
		}
		results = append(results, hermesExtractResult{
			URL:     cmpNonEmpty(metadataString(resp.Data.Metadata, "sourceURL"), itemURL),
			Title:   metadataString(resp.Data.Metadata, "title"),
			Content: cmpNonEmpty(resp.Data.Markdown, cmpNonEmpty(resp.Data.Summary, resp.Data.HTML)),
		})
	}
	return hermesExtractOutput{Results: results}, nil
}

func postWebProviderJSON(ctx context.Context, client *http.Client, providerName, baseURL, path, apiKey string, payload any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	limited := io.LimitReader(resp.Body, maxWebToolResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(raw) > maxWebToolResponseBytes {
		return fmt.Errorf("response exceeds maximum size")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s returned %s: %s", providerName, resp.Status, truncateForToolOutput(string(raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func normalizeTavilySearch(resp tavilySearchResponse) hermesSearchOutput {
	results := make([]hermesWebResult, 0, len(resp.Results))
	for i, item := range resp.Results {
		results = append(results, hermesWebResult{
			Title:       item.Title,
			URL:         item.URL,
			Description: cmpNonEmpty(item.Content, item.RawContent),
			Position:    i + 1,
		})
	}
	return hermesSearchOutput{
		Success: true,
		Data:    hermesSearchData{Web: results},
	}
}

func normalizeTavilyExtract(resp tavilyExtractResponse) hermesExtractOutput {
	results := make([]hermesExtractResult, 0, len(resp.Results)+len(resp.FailedResults))
	for _, item := range resp.Results {
		content := cmpNonEmpty(item.Content, item.RawContent)
		results = append(results, hermesExtractResult{
			URL:     item.URL,
			Title:   item.Title,
			Content: content,
		})
	}
	for _, item := range resp.FailedResults {
		results = append(results, hermesExtractResult{
			URL:     item.URL,
			Title:   "",
			Content: "",
			Error:   item.Error,
		})
	}
	return hermesExtractOutput{Results: results}
}

func normalizeFirecrawlSearch(resp firecrawlSearchResponse) hermesSearchOutput {
	results := make([]hermesWebResult, 0, len(resp.Data.Web))
	for i, item := range resp.Data.Web {
		description := cmpNonEmpty(item.Description, item.Markdown)
		if description == "" {
			description = metadataString(item.Metadata, "description")
		}
		results = append(results, hermesWebResult{
			Title:       item.Title,
			URL:         item.URL,
			Description: description,
			Position:    i + 1,
		})
	}
	return hermesSearchOutput{
		Success: true,
		Data:    hermesSearchData{Web: results},
	}
}

func jsonToolOutput(value any) ToolOut {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return toolError("Failed to encode web tool output: %v", err)
	}
	return ToolOut{Content: truncateForToolOutput(string(raw))}
}

func truncateForToolOutput(content string) string {
	if len(content) <= maxWebToolOutputBytes {
		return content
	}
	cutoff := maxWebToolOutputBytes
	for cutoff > 0 && !utf8.RuneStart(content[cutoff]) {
		cutoff--
	}
	return content[:cutoff] + "\n... [truncated]"
}

func validateExtractURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("empty URL")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("only http and https URLs are allowed")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("embedded credentials are not allowed")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	if isBlockedInternalHost(host) {
		return "", fmt.Errorf("private or internal hosts are not allowed")
	}
	if hasSensitiveQueryParam(parsed.Query()) {
		return "", fmt.Errorf("URLs with sensitive query parameters are not allowed")
	}
	return parsed.String(), nil
}

func isBlockedInternalHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	if !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.IsLoopback() ||
			addr.IsPrivate() ||
			addr.IsUnspecified() ||
			addr.IsLinkLocalUnicast() ||
			addr.IsLinkLocalMulticast() ||
			addr.IsMulticast()
	}
	return false
}

func hasSensitiveQueryParam(values url.Values) bool {
	for name := range values {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "key") ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") {
			return true
		}
	}
	return false
}

func cmpNonEmpty(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}
