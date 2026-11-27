// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package toolcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
)

const (
	// tokenMinTTL is the lifetime a listing asks the broker for, as a step
	// that uses a token in process does.
	tokenMinTTL = 5 * time.Minute
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
// token: with HTTP 401 after the token was reported to the source once, or
// with HTTP 403.
type RejectedError struct {
	// Status is the HTTP status, 401 or 403.
	Status int
	// Code is the error the server's WWW-Authenticate challenge names, such
	// as insufficient_scope, or empty.
	Code string
	// Scope is the scope the challenge names, such as a missing scope, or
	// empty.
	Scope string
}

func (e *RejectedError) Error() string {
	msg := fmt.Sprintf("the server rejected the connection's token (HTTP %d", e.Status)
	if e.Code != "" {
		msg += ", " + e.Code
	}
	if e.Scope != "" {
		msg += ", scope " + e.Scope
	}
	return msg + ")"
}

// challengeParam matches one auth-param of a WWW-Authenticate challenge.
var challengeParam = regexp.MustCompile(`([A-Za-z_]+)="([^"]*)"`)

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
// the source once and lists again with the token the source returns. A
// refusal fails with a *RejectedError. A listing of more than 1,000 tools
// or pages, or with a tool over 64 KiB, fails.
func (l *MCPLister) ListTools(ctx context.Context, conn *connection.Connection, endpoint string) ([]connection.Tool, error) {
	token, _, err := l.source.Token(ctx, conn, "", tokenMinTTL)
	if err != nil {
		return nil, err
	}
	tools, err := l.list(ctx, conn, endpoint, token)
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.Status != http.StatusUnauthorized {
		return tools, err
	}
	token, _, err = l.source.Rejected(ctx, conn, "", token, tokenMinTTL)
	if err != nil {
		return nil, err
	}
	return l.list(ctx, conn, endpoint, token)
}

func (l *MCPLister) list(ctx context.Context, conn *connection.Connection, endpoint, token string) ([]connection.Tool, error) {
	bearer := &bearerTransport{base: l.base, conn: conn, token: token}
	transport := &sdk.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport:     bearer,
			CheckRedirect: sameOrigin,
			Timeout:       requestTimeout,
		},
		DisableStandaloneSSE: true,
	}
	session, err := newClient().Connect(ctx, transport, nil)
	if err != nil {
		return nil, bearer.explain(err)
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
			return nil, bearer.explain(err)
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

// newClient returns Dagu's MCP client for a connection. It advertises form
// elicitation, as Spec 107 requires of a client that uses a connection,
// and declines every request at once: only a person accepts one.
func newClient() *sdk.Client {
	return sdk.NewClient(&sdk.Implementation{Name: "dagu", Version: config.Version}, &sdk.ClientOptions{
		ElicitationHandler: func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			return &sdk.ElicitResult{Action: "decline"}, nil
		},
	})
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

// bearerTransport adds the token to each request to a bound host and
// refuses any other request. It remembers the first 401 or 403, which the
// SDK reports only as text.
type bearerTransport struct {
	base  http.RoundTripper
	conn  *connection.Connection
	token string

	mu       sync.Mutex
	rejected *RejectedError
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.conn.BindsURL(req.URL) {
		return nil, fmt.Errorf("refused to send connection %q's token to %s://%s, which is not HTTPS on a bound host", t.conn.Name, req.URL.Scheme, req.URL.Host)
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	resp, err := t.base.RoundTrip(req)
	if err == nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		t.reject(resp)
	}
	return resp, err
}

func (t *bearerTransport) reject(resp *http.Response) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rejected != nil {
		return
	}
	t.rejected = &RejectedError{Status: resp.StatusCode}
	for _, m := range challengeParam.FindAllStringSubmatch(resp.Header.Get("WWW-Authenticate"), -1) {
		switch strings.ToLower(m[1]) {
		case "error":
			t.rejected.Code = m[2]
		case "scope":
			t.rejected.Scope = m[2]
		}
	}
}

// explain returns the remembered refusal in place of err.
func (t *bearerTransport) explain(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rejected != nil {
		return t.rejected
	}
	return err
}
