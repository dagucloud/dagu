// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package toolcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
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
	// maxTools bounds the tools kept from one server, so a server that
	// pages without end cannot grow a catalog without bound.
	maxTools = 1000
	// maxRedirects is the number of same-origin redirects a request
	// follows, as net/http does by default.
	maxRedirects = 10
)

// errRejected reports that the server answered a request that carried the
// token with HTTP 401.
var errRejected = errors.New("the server rejected the connection's token (HTTP 401)")

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
// the source once and lists again with the token the source returns.
func (l *MCPLister) ListTools(ctx context.Context, conn *connection.Connection, endpoint string) ([]connection.Tool, error) {
	token, _, err := l.source.Token(ctx, conn, "", tokenMinTTL)
	if err != nil {
		return nil, err
	}
	tools, err := l.list(ctx, conn, endpoint, token)
	if !errors.Is(err, errRejected) {
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
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, bearer.explain(err)
		}
		if len(tools) == maxTools {
			return nil, fmt.Errorf("the server lists more than %d tools", maxTools)
		}
		t, err := catalogTool(tool)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
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
// refuses any other request. It remembers a 401, which the SDK reports
// only as text.
type bearerTransport struct {
	base     http.RoundTripper
	conn     *connection.Connection
	token    string
	rejected atomic.Bool
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.conn.BindsURL(req.URL) {
		return nil, fmt.Errorf("refused to send connection %q's token to %s://%s, which is not HTTPS on a bound host", t.conn.Name, req.URL.Scheme, req.URL.Host)
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		t.rejected.Store(true)
	}
	return resp, err
}

// explain returns errRejected in place of err after a 401.
func (t *bearerTransport) explain(err error) error {
	if t.rejected.Load() {
		return errRejected
	}
	return err
}
