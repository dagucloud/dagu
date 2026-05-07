// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebSearchTool_TavilySearchUsesBearerAuthAndNormalizesResults(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{
					"title": "Dagu",
					"url": "https://dagu.cloud/",
					"content": "A workflow orchestration engine."
				}
			]
		}`))
	}))
	t.Cleanup(server.Close)

	tool := NewWebSearchTool(WebToolsConfig{
		Enabled: true,
		Backend: WebToolsBackendTavily,
		Tavily: &TavilyWebToolsConfig{
			APIKey:  "tvly-test",
			BaseURL: server.URL,
		},
	})
	require.NotNil(t, tool)

	out := tool.Run(ToolContext{}, json.RawMessage(`{"query":"dagu agent","limit":3}`))
	require.False(t, out.IsError, out.Content)
	assert.Equal(t, "Bearer tvly-test", gotAuth)
	assert.Equal(t, "/search", gotPath)
	assert.Equal(t, "dagu agent", gotBody["query"])
	assert.Equal(t, float64(3), gotBody["max_results"])

	var normalized struct {
		Success bool `json:"success"`
		Data    struct {
			Web []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Position    int    `json:"position"`
			} `json:"web"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Content), &normalized))
	require.True(t, normalized.Success)
	require.Len(t, normalized.Data.Web, 1)
	assert.Equal(t, "Dagu", normalized.Data.Web[0].Title)
	assert.Equal(t, "https://dagu.cloud/", normalized.Data.Web[0].URL)
	assert.Equal(t, "A workflow orchestration engine.", normalized.Data.Web[0].Description)
	assert.Equal(t, 1, normalized.Data.Web[0].Position)
}

func TestWebExtractTool_RejectsUnsafeURLsBeforeRequest(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	tool := NewWebExtractTool(WebToolsConfig{
		Enabled: true,
		Backend: WebToolsBackendTavily,
		Tavily: &TavilyWebToolsConfig{
			APIKey:  "tvly-test",
			BaseURL: server.URL,
		},
	})
	require.NotNil(t, tool)

	out := tool.Run(ToolContext{}, json.RawMessage(`{"urls":["http://127.0.0.1:8080/private"]}`))
	require.True(t, out.IsError)
	assert.Contains(t, out.Content, "blocked URL")
	assert.False(t, called)
}

func TestWebExtractTool_TavilyExtractUsesBearerAuthAndNormalizesHermesResults(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{
					"url": "https://example.com/article",
					"title": "Article",
					"raw_content": "Extracted content",
					"metadata": {"sourceURL": "https://example.com/article"}
				}
			],
			"failed_results": [
				{
					"url": "https://example.com/missing",
					"error": "not found"
				}
			]
		}`))
	}))
	t.Cleanup(server.Close)

	tool := NewWebExtractTool(WebToolsConfig{
		Enabled: true,
		Backend: WebToolsBackendTavily,
		Tavily: &TavilyWebToolsConfig{
			APIKey:  "tvly-test",
			BaseURL: server.URL,
		},
	})
	require.NotNil(t, tool)

	out := tool.Run(ToolContext{}, json.RawMessage(`{"urls":["https://example.com/article"]}`))
	require.False(t, out.IsError, out.Content)
	assert.Equal(t, "Bearer tvly-test", gotAuth)
	assert.Equal(t, "/extract", gotPath)
	assert.Equal(t, []any{"https://example.com/article"}, gotBody["urls"])

	var normalized struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
			Error   string `json:"error,omitempty"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Content), &normalized))
	require.Len(t, normalized.Results, 2)
	assert.Equal(t, "https://example.com/article", normalized.Results[0].URL)
	assert.Equal(t, "Article", normalized.Results[0].Title)
	assert.Equal(t, "Extracted content", normalized.Results[0].Content)
	assert.Equal(t, "https://example.com/missing", normalized.Results[1].URL)
	assert.Equal(t, "not found", normalized.Results[1].Error)

	var unexpected map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.Content), &unexpected))
	assert.NotContains(t, unexpected, "success")
	assert.NotContains(t, unexpected, "data")
}

func TestWebSearchTool_FirecrawlSearchUsesBearerAuthAndNormalizesResults(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"web": [
					{
						"title": "Dagu",
						"url": "https://dagu.cloud/",
						"description": "A workflow orchestration engine."
					}
				]
			}
		}`))
	}))
	t.Cleanup(server.Close)

	tool := NewWebSearchTool(WebToolsConfig{
		Enabled: true,
		Backend: WebToolsBackendFirecrawl,
		Firecrawl: &FirecrawlWebToolsConfig{
			APIKey:  "fc-test",
			BaseURL: server.URL,
		},
	})
	require.NotNil(t, tool)

	out := tool.Run(ToolContext{}, json.RawMessage(`{"query":"dagu agent","limit":3}`))
	require.False(t, out.IsError, out.Content)
	assert.Equal(t, "Bearer fc-test", gotAuth)
	assert.Equal(t, "/v2/search", gotPath)
	assert.Equal(t, "dagu agent", gotBody["query"])
	assert.Equal(t, float64(3), gotBody["limit"])
	assert.Equal(t, []any{"web"}, gotBody["sources"])

	var normalized struct {
		Success bool `json:"success"`
		Data    struct {
			Web []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Position    int    `json:"position"`
			} `json:"web"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Content), &normalized))
	require.True(t, normalized.Success)
	require.Len(t, normalized.Data.Web, 1)
	assert.Equal(t, "Dagu", normalized.Data.Web[0].Title)
	assert.Equal(t, "https://dagu.cloud/", normalized.Data.Web[0].URL)
	assert.Equal(t, "A workflow orchestration engine.", normalized.Data.Web[0].Description)
	assert.Equal(t, 1, normalized.Data.Web[0].Position)
}

func TestWebExtractTool_FirecrawlScrapesEachURLAndNormalizesResults(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var gotPaths []string
	var gotBodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPaths = append(gotPaths, r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotBodies = append(gotBodies, body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"markdown": "Extracted content",
				"metadata": {
					"title": "Article",
					"sourceURL": "https://example.com/article"
				}
			}
		}`))
	}))
	t.Cleanup(server.Close)

	tool := NewWebExtractTool(WebToolsConfig{
		Enabled: true,
		Backend: WebToolsBackendFirecrawl,
		Firecrawl: &FirecrawlWebToolsConfig{
			APIKey:  "fc-test",
			BaseURL: server.URL,
		},
	})
	require.NotNil(t, tool)

	out := tool.Run(ToolContext{}, json.RawMessage(`{"urls":["https://example.com/article"]}`))
	require.False(t, out.IsError, out.Content)
	assert.Equal(t, "Bearer fc-test", gotAuth)
	assert.Equal(t, []string{"/v2/scrape"}, gotPaths)
	require.Len(t, gotBodies, 1)
	assert.Equal(t, "https://example.com/article", gotBodies[0]["url"])
	assert.Equal(t, []any{"markdown"}, gotBodies[0]["formats"])
	assert.Equal(t, true, gotBodies[0]["onlyMainContent"])

	var normalized struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Content), &normalized))
	require.Len(t, normalized.Results, 1)
	assert.Equal(t, "https://example.com/article", normalized.Results[0].URL)
	assert.Equal(t, "Article", normalized.Results[0].Title)
	assert.Equal(t, "Extracted content", normalized.Results[0].Content)
}

func TestCreateTools_IncludesWebToolsOnlyWhenConfigured(t *testing.T) {
	t.Parallel()

	assert.Nil(t, GetToolByName(CreateTools(ToolConfig{}), "web_search"))
	assert.Nil(t, GetToolByName(CreateTools(ToolConfig{}), "web_extract"))

	toolsWithDefaultBackend := CreateTools(ToolConfig{
		WebTools: &WebToolsConfig{
			Enabled: true,
			Tavily: &TavilyWebToolsConfig{
				APIKey: "tvly-test",
			},
		},
	})
	assert.NotNil(t, GetToolByName(toolsWithDefaultBackend, "web_search"))
	assert.NotNil(t, GetToolByName(toolsWithDefaultBackend, "web_extract"))

	tools := CreateTools(ToolConfig{
		WebTools: &WebToolsConfig{
			Enabled: true,
			Backend: WebToolsBackendTavily,
			Tavily: &TavilyWebToolsConfig{
				APIKey: "tvly-test",
			},
		},
	})
	assert.NotNil(t, GetToolByName(tools, "web_search"))
	assert.NotNil(t, GetToolByName(tools, "web_extract"))

	firecrawlTools := CreateTools(ToolConfig{
		WebTools: &WebToolsConfig{
			Enabled: true,
			Backend: WebToolsBackendFirecrawl,
			Firecrawl: &FirecrawlWebToolsConfig{
				APIKey: "fc-test",
			},
		},
	})
	assert.NotNil(t, GetToolByName(firecrawlTools, "web_search"))
	assert.NotNil(t, GetToolByName(firecrawlTools, "web_extract"))
}

func TestResolveWebToolsConfig_CentralizesDefaults(t *testing.T) {
	t.Parallel()

	cfg := ResolveWebToolsConfig(WebToolsConfig{
		Enabled: true,
		Tavily: &TavilyWebToolsConfig{
			APIKey: "  tvly-test  ",
		},
	})

	assert.Equal(t, WebToolsBackendTavily, cfg.Backend)
	require.NotNil(t, cfg.Tavily)
	assert.Equal(t, "tvly-test", cfg.Tavily.APIKey)
	assert.Equal(t, DefaultTavilyBaseURL, cfg.Tavily.BaseURL)
	assert.Equal(t, MaxTavilySearchLimit, cfg.Tavily.MaxResults)
	assert.Equal(t, DefaultTavilySearchDepth, cfg.Tavily.SearchDepth)

	firecrawlCfg := ResolveWebToolsConfig(WebToolsConfig{
		Enabled: true,
		Backend: WebToolsBackendFirecrawl,
		Firecrawl: &FirecrawlWebToolsConfig{
			APIKey: "  fc-test  ",
		},
	})
	assert.Equal(t, WebToolsBackendFirecrawl, firecrawlCfg.Backend)
	require.NotNil(t, firecrawlCfg.Firecrawl)
	assert.Equal(t, "fc-test", firecrawlCfg.Firecrawl.APIKey)
	assert.Equal(t, DefaultFirecrawlBaseURL, firecrawlCfg.Firecrawl.BaseURL)
	assert.Equal(t, MaxFirecrawlSearchLimit, firecrawlCfg.Firecrawl.MaxResults)
}

func TestTruncateForToolOutput_PreservesUTF8(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("a", maxWebToolOutputBytes-1) + "世tail"

	out := truncateForToolOutput(content)

	assert.True(t, utf8.ValidString(out))
	assert.True(t, strings.HasSuffix(out, "\n... [truncated]"))
}
