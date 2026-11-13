// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpTimeout bounds the connection, the listing and each call of an MCP tool.
// The limit is fixed (Spec 090).
const mcpTimeout = 30 * time.Second

// mcpBinding is the server and the tool name that a catalog tool calls.
type mcpBinding struct {
	endpoint string
	headers  map[string]string
	timeout  time.Duration
	tool     string
}

// headerTransport adds fixed headers to each request for the origin of the
// server. A redirect to another origin gets no header, so a credential never
// leaves the server that the author named.
type headerTransport struct {
	base    http.RoundTripper
	origin  string
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if originOf(req.URL) != t.origin {
		return t.base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	for key, value := range t.headers {
		clone.Header.Set(key, value)
	}
	return t.base.RoundTrip(clone)
}

// originOf returns the scheme and the host of a URL, in lower case.
func originOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// redactEndpoint hides the user info and the query of an endpoint, which may
// carry a secret, before it enters an error message.
func redactEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "<invalid url>"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

// withMCPSession connects to the server, runs fn, and closes the session. The
// fixed limit mcpTimeout covers the whole operation. Each operation uses its
// own session, so a step holds no connection between calls.
func withMCPSession(
	ctx context.Context,
	endpoint string,
	headers map[string]string,
	fn func(context.Context, *mcpsdk.ClientSession) error,
) error {
	ctx, cancel := context.WithTimeout(ctx, mcpTimeout)
	defer cancel()

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("connect to MCP server %s: %w", redactEndpoint(endpoint), err)
	}
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, origin: originOf(parsed), headers: headers}}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "dagu", Version: "v2"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		return fmt.Errorf("connect to MCP server %s: %w", redactEndpoint(endpoint), err)
	}
	defer func() { _ = session.Close() }()
	return fn(ctx, session)
}

// listMCPTools returns every tool that the server lists.
func listMCPTools(
	ctx context.Context,
	endpoint string,
	headers map[string]string,
) ([]*mcpsdk.Tool, error) {
	var tools []*mcpsdk.Tool
	err := withMCPSession(ctx, endpoint, headers, func(ctx context.Context, session *mcpsdk.ClientSession) error {
		params := &mcpsdk.ListToolsParams{}
		for {
			page, err := session.ListTools(ctx, params)
			if err != nil {
				return fmt.Errorf("list tools of MCP server %s: %w", redactEndpoint(endpoint), err)
			}
			tools = append(tools, page.Tools...)
			if page.NextCursor == "" {
				return nil
			}
			params = &mcpsdk.ListToolsParams{Cursor: page.NextCursor}
		}
	})
	return tools, err
}

// call runs the tool on the server. It returns the text of the reply and
// whether the server marked the reply as an error.
func (b *mcpBinding) call(ctx context.Context, args map[string]any) (string, bool, error) {
	var (
		text    string
		isError bool
	)
	err := withMCPSession(ctx, b.endpoint, b.headers, func(ctx context.Context, session *mcpsdk.ClientSession) error {
		result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: b.tool, Arguments: args})
		if err != nil {
			return fmt.Errorf("call tool %q of MCP server %s: %w", b.tool, redactEndpoint(b.endpoint), err)
		}
		text = mcpResultText(result)
		isError = result.IsError
		return nil
	})
	return text, isError, err
}

// mcpResultText joins the text content of a reply with a line end. Content of
// another kind is replaced by a short note.
func mcpResultText(result *mcpsdk.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*mcpsdk.TextContent); ok {
			parts = append(parts, text.Text)
			continue
		}
		parts = append(parts, "[non-text content omitted]")
	}
	return strings.Join(parts, "\n")
}
