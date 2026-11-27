// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/mcpclient"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// Details of an auth error of a call through a connection.
const (
	detailReconnectRequired = "reconnect_required"
	detailInsufficientScope = "insufficient_scope"
)

// confirmationWays names how a person lets an action through that the
// server asks to confirm (Spec 107 §Confirmation Requests).
const confirmationWays = "To let the action through, approve it once in Try it and save the approval where the provider's form offers that, " +
	"ask the Workspace admin to change the confirmation rules, or call the API with http.request and a standard connection"

// stepConnection returns the connection that an mcp.call step names, or "".
func stepConnection(step ir.Step) string {
	name, _ := step.ExecutorConfig.Config["connection"].(string)
	return name
}

// connectionCall is what a connection adds to a call: the endpoint that
// may receive its token, the session's authorization, and what the session
// learns on the way.
type connectionCall struct {
	name     string
	endpoint string
	bearer   *mcpclient.Bearer

	mu     sync.Mutex
	tokens []string
	// confirmation is the message of the first confirmation request the
	// server sent; asked reports that one came.
	confirmation string
	asked        bool
}

// connect resolves the step's connection and takes its token, before any
// request is sent.
func (c *caller) connect(ctx context.Context, base http.RoundTripper) (*connectionCall, *callError) {
	name := c.cfg.Connection
	cc := &connectionCall{name: name}
	conns := runtime.GetDAGContext(ctx).Connections
	if conns == nil {
		return cc, authError(name, errors.New("connections are not available to this run"))
	}
	conn, err := conns.Connection(ctx, name)
	if err != nil {
		return cc, authError(name, err)
	}
	if cc.endpoint, err = endpoint(conn, c.cfg); err != nil {
		return cc, authError(name, err)
	}
	cc.bearer = mcpclient.NewBearer(conns, conn, base, cc.learn(c.maskSecrets))
	if _, err := cc.bearer.Token(ctx); err != nil {
		return cc, authError(name, err)
	}
	return cc, nil
}

// endpoint returns the MCP server that cfg calls through conn: its url, or
// the MCP server of its server's preset, or the resource of an mcp
// connection. The token goes to it only over HTTPS on a bound host.
func endpoint(conn *connection.Connection, cfg config) (string, error) {
	if conn.Provider == connection.ProviderMCP && cfg.Server != "" {
		return "", errors.New("with.server is not allowed with an mcp connection, which has one MCP server")
	}
	target := cfg.URL
	switch {
	case target != "":
	case cfg.Server != "":
		p, ok := connection.LookupPreset(conn.Provider, cfg.Server)
		if !ok || p.MCPURL == "" {
			return "", fmt.Errorf("service %q has no MCP server", cfg.Server)
		}
		target = p.MCPURL
	case conn.Provider == connection.ProviderMCP:
		target = conn.Resource
	default:
		return "", errors.New("with.server or with.url is required with this connection")
	}
	u, err := url.Parse(target)
	if err != nil || !conn.BindsURL(u) {
		return "", fmt.Errorf("the token is sent only over HTTPS to the connection's bound hosts, and %s is not one", target)
	}
	return target, nil
}

// learn returns the callback that receives each token the session uses: it
// is masked in the step's logs at once and redacted from its output.
func (cc *connectionCall) learn(mask func(...string)) func(string) {
	return func(token string) {
		if mask != nil {
			mask(token)
		}
		cc.mu.Lock()
		defer cc.mu.Unlock()
		cc.tokens = append(cc.tokens, token)
	}
}

func (cc *connectionCall) seen() []string {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.tokens
}

func (cc *connectionCall) elicited(req *sdk.ElicitRequest) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if !cc.asked {
		cc.asked = true
		cc.confirmation = req.Params.Message
	}
}

// confirmationError returns the error of a call whose server asked for a
// confirmation, or nil when it asked for none.
func (cc *connectionCall) confirmationError(redact redactor) *callError {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if !cc.asked {
		return nil
	}
	msg := "the server asked to confirm the action, which mcp.call declines"
	if cc.confirmation != "" {
		msg = fmt.Sprintf("the server asked to confirm the action (%s), which mcp.call declines", redact.apply(cc.confirmation))
	}
	return &callError{kind: kindConfirmationRequired, message: msg + ". " + confirmationWays, connection: cc.name}
}

// refusal returns the auth error of a call whose token the server refused,
// or nil.
func (cc *connectionCall) refusal(err error, redact redactor) *callError {
	var rejected *mcpclient.RejectedError
	if !errors.As(cc.bearer.Explain(err), &rejected) {
		return nil
	}
	cerr := authError(cc.name, errors.New(redact.apply(rejected.Error())))
	if rejected.Status == http.StatusForbidden && rejected.Code == detailInsufficientScope {
		cerr.detail, cerr.scope = detailInsufficientScope, redact.apply(rejected.Scope)
	}
	return cerr
}

// authError returns the auth error of the connection named name, which
// carries reconnect_required when the connection needs reconnecting.
func authError(name string, err error) *callError {
	msg := err.Error()
	if !strings.Contains(msg, strconv.Quote(name)) {
		msg = fmt.Sprintf("connection %q: %s", name, msg)
	}
	cerr := &callError{kind: kindAuth, message: msg, connection: name}
	if errors.Is(err, broker.ErrReconnectRequired) {
		cerr.detail = detailReconnectRequired
	}
	return cerr
}
