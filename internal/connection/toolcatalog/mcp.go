// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package toolcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/mcpclient"
)

const (
	// requestTimeout bounds each HTTP request of a listing, including the
	// session's closing DELETE, which the SDK sends without a deadline.
	requestTimeout = 30 * time.Second
	// maxTools and maxPages bound one listing, so a server that pages
	// without end cannot grow a catalog or hold a listing without bound. A
	// listing beyond either fails rather than hiding tools.
	maxTools = 1000
	maxPages = maxTools
	// maxToolBytes bounds one tool's description, input schema, and
	// annotations together.
	maxToolBytes = 64 << 10
	// maxRedirects is the number of same-origin redirects a request
	// follows, as net/http does by default.
	maxRedirects = 10
)

// RejectedError reports that the MCP server refused the connection's
// token.
type RejectedError = mcpclient.RejectedError

var _ Lister = (*MCPLister)(nil)

// MCPLister lists tools over MCP's streamable HTTP transport with tokens
// from a broker source. It sends a token only over HTTPS to the
// connection's bound hosts, follows a redirect only to HTTPS on the same
// host and port, and advertises form elicitation, declining every request.
type MCPLister struct {
	source broker.Source
	base   http.RoundTripper
}

// NewMCPLister returns a lister that takes tokens from source and sends
// requests through base, or http.DefaultTransport when base is nil.
func NewMCPLister(source broker.Source, base http.RoundTripper) *MCPLister {
	if base == nil {
		base = http.DefaultTransport
	}
	return &MCPLister{source: source, base: base}
}

// ListTools lists the tools of endpoint. A 401 to the token reports it to
// the source once and retries with the token the source returns. A refusal
// fails with a *RejectedError. A listing of more than 1,000 tools or pages,
// or with a tool over 64 KiB, fails.
func (l *MCPLister) ListTools(ctx context.Context, conn *connection.Connection, endpoint string) ([]connection.Tool, error) {
	bearer := mcpclient.NewBearer(l.source, conn, l.base, nil)
	if _, err := bearer.Token(ctx); err != nil {
		return nil, err
	}
	transport := &sdk.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport:     bearer,
			CheckRedirect: sameOrigin,
			Timeout:       requestTimeout,
		},
		OAuthHandler:         bearer,
		DisableStandaloneSSE: true,
	}
	session, err := mcpclient.NewClient(nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, bearer.Explain(err)
	}
	defer func() { _ = session.Close() }()

	var tools []connection.Tool
	params := &sdk.ListToolsParams{}
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("the server lists more than %d pages of tools", maxPages)
		}
		res, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, bearer.Explain(err)
		}
		if len(tools)+len(res.Tools) > maxTools {
			return nil, fmt.Errorf("the server lists more than %d tools", maxTools)
		}
		for _, tool := range res.Tools {
			t, err := catalogTool(tool)
			if err != nil {
				return nil, err
			}
			tools = append(tools, t)
		}
		if res.NextCursor == "" {
			return tools, nil
		}
		params = &sdk.ListToolsParams{Cursor: res.NextCursor}
	}
}

func catalogTool(tool *sdk.Tool) (connection.Tool, error) {
	schema, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return connection.Tool{}, fmt.Errorf("tool %q: input schema: %w", tool.Name, err)
	}
	t := connection.Tool{Name: tool.Name, Description: tool.Description, InputSchema: schema}
	if tool.Annotations != nil {
		if t.Annotations, err = json.Marshal(tool.Annotations); err != nil {
			return connection.Tool{}, fmt.Errorf("tool %q: annotations: %w", tool.Name, err)
		}
	}
	if len(t.Description)+len(t.InputSchema)+len(t.Annotations) > maxToolBytes {
		return connection.Tool{}, fmt.Errorf("tool %q is larger than %d KiB", tool.Name, maxToolBytes>>10)
	}
	return t, nil
}

// sameOrigin follows a redirect only to HTTPS on the host and port of the
// first request, as the connection's binding requires.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	first := via[0].URL
	if !strings.EqualFold(req.URL.Scheme, "https") ||
		!strings.EqualFold(strings.TrimSuffix(req.URL.Hostname(), "."), strings.TrimSuffix(first.Hostname(), ".")) ||
		effectivePort(req.URL.Port()) != effectivePort(first.Port()) {
		return fmt.Errorf("refused a redirect to %s://%s: a token follows only to HTTPS on the same host and port", req.URL.Scheme, req.URL.Host)
	}
	return nil
}

func effectivePort(port string) string {
	if port == "" {
		return "443"
	}
	return port
}
