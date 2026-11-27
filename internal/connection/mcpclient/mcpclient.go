// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mcpclient sets up Dagu's MCP clients that use a connection. Each
// request carries the connection's token, only over HTTPS to a bound host,
// and a refused token is reported to the token source once per session
// (Spec 107 §Using a Connection):
//
//	request -> bound host? -- no --> refused, nothing sent
//	             | yes
//	             v
//	           Bearer token -> 401 -> Rejected, once -> retry
//	                        -> 403 -> *RejectedError, no refresh
package mcpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
)

const (
	// redactedText replaces a secret in an error's text.
	redactedText = "[redacted]"
	// maxRefusalBody bounds the part of a refusal's body that a
	// RejectedError keeps.
	maxRefusalBody = 4 << 10
)

// challengeParam matches one auth-param of a WWW-Authenticate challenge,
// whose value is a quoted string or a token (RFC 7235).
var challengeParam = regexp.MustCompile(`([A-Za-z_]+)=(?:"([^"]*)"|([^\s,"]+))`)

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
	// Description is the challenge's error_description, or empty.
	Description string
	// Err is the failure the MCP client reported, which carries the
	// provider's message, with the token redacted.
	Err error
	// Body is the start of the response body, at most 4 KiB, with the
	// token redacted. It holds the provider's error when that is no
	// JSON-RPC error, such as a Google API's JSON error.
	Body string
}

func (e *RejectedError) Error() string {
	msg := fmt.Sprintf("the server rejected the connection's token (HTTP %d", e.Status)
	if e.Code != "" {
		msg += ", " + e.Code
	}
	if e.Scope != "" {
		msg += ", scope " + e.Scope
	}
	msg += ")"
	if e.Description != "" {
		msg += ": " + e.Description
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *RejectedError) Unwrap() error { return e.Err }

var (
	_ http.RoundTripper = (*Bearer)(nil)
	_ auth.OAuthHandler = (*Bearer)(nil)
)

// Bearer authorizes the requests of one MCP session with a connection's
// token. As the session's HTTP transport, it sends the token only over
// HTTPS to the connection's bound hosts and refuses any other request. As
// the session's OAuth handler, it reports a token refused with HTTP 401 to
// the source once, so the request is retried with the token the source
// returns. A Bearer serves one call, such as one mcp.call or one listing:
// it allows one report in its lifetime.
type Bearer struct {
	source  broker.Source
	conn    *connection.Connection
	base    http.RoundTripper
	onToken func(string)

	mu    sync.Mutex
	token string
	// renewAt is when token runs low, or zero for a token without expiry.
	renewAt  time.Time
	reported bool
	// rejected is the latest refusal and refusedToken the token it refused;
	// a report that gets a new token clears both.
	rejected     *RejectedError
	refusedToken string
}

// NewBearer returns a Bearer for conn that takes tokens from source, asking
// for broker.StepMinTTL, and sends requests through base, or
// http.DefaultTransport when base is nil. onToken, when set, receives each
// token the source hands out before a request carries it.
func NewBearer(source broker.Source, conn *connection.Connection, base http.RoundTripper, onToken func(string)) *Bearer {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Bearer{source: source, conn: conn, base: base, onToken: onToken}
}

// Token returns the token that requests carry, asking the source again once
// it runs low.
func (b *Bearer) Token(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.current(ctx)
}

func (b *Bearer) current(ctx context.Context) (string, error) {
	if b.token != "" && (b.renewAt.IsZero() || time.Now().Before(b.renewAt)) {
		return b.token, nil
	}
	token, expiresIn, err := b.source.Token(ctx, b.conn, "", broker.StepMinTTL)
	if err != nil {
		return "", err
	}
	b.keep(token, expiresIn)
	return token, nil
}

// keep makes token the one requests carry. It runs low when less than the
// lifetime the source keeps remains: StepMinTTL, or half of a shorter
// lifetime.
func (b *Bearer) keep(token string, expiresIn time.Duration) {
	b.token = token
	b.renewAt = time.Time{}
	if expiresIn > 0 {
		b.renewAt = time.Now().Add(expiresIn - min(broker.StepMinTTL, expiresIn/2))
	}
	if b.onToken != nil {
		b.onToken(token)
	}
}

func (b *Bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	if !b.conn.BindsURL(req.URL) {
		return nil, fmt.Errorf("refused to send connection %q's token to %s://%s, which is not HTTPS on a bound host", b.conn.Name, req.URL.Scheme, req.URL.Host)
	}
	token, err := b.Token(req.Context())
	if err != nil {
		return nil, err
	}
	resp, err := b.send(req, token)
	if err == nil && req.Method == http.MethodGet && resp.StatusCode == http.StatusUnauthorized {
		return b.resume(req, resp)
	}
	return resp, err
}

// resume reports the token that the GET resuming a stream had refused,
// which go-sdk never passes to Authorize, and sends the GET again, with its
// Last-Event-ID, carrying the token the source returns. After the one
// report, it leaves the refusal to the session.
func (b *Bearer) resume(req *http.Request, resp *http.Response) (*http.Response, error) {
	b.mu.Lock()
	if b.reported {
		b.mu.Unlock()
		return resp, nil
	}
	_ = resp.Body.Close()
	b.reported = true
	token, expiresIn, err := b.source.Rejected(req.Context(), b.conn, "", b.refusedToken, broker.StepMinTTL)
	if err == nil {
		b.rejected, b.refusedToken = nil, ""
		b.keep(token, expiresIn)
	}
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return b.send(req, token)
}

// send sends req with token, remembering a refusal.
func (b *Bearer) send(req *http.Request, token string) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := b.base.RoundTrip(req)
	// The DELETE that closes a session answers for no operation of the
	// call, so its refusal never stands for the call's failure.
	if err == nil && req.Method != http.MethodDelete &&
		(resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		b.reject(resp, token)
	}
	return resp, err
}

// TokenSource returns no token source: RoundTrip adds the token, after the
// bound host check.
func (b *Bearer) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return nil, nil
}

// Authorize reports the token that a 401 refused to the source, once per
// Bearer, and keeps the token the source returns, so the session retries
// the request with it. The source refreshes only while the refused token
// is still the stored one. A 403, or a 401 after the report, fails with a
// *RejectedError, without a refresh.
func (b *Bearer) Authorize(ctx context.Context, _ *http.Request, resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rejected == nil {
		b.rejected = &RejectedError{Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusUnauthorized || b.reported {
		rejected := *b.rejected
		rejected.Err = errors.New(b.redact(providerMessage(resp)))
		return &rejected
	}
	b.reported = true
	token, expiresIn, err := b.source.Rejected(ctx, b.conn, "", b.refusedToken, broker.StepMinTTL)
	if err != nil {
		return err
	}
	b.rejected, b.refusedToken = nil, ""
	b.keep(token, expiresIn)
	return nil
}

// Explain returns the refusal that caused err, when a request of the
// session was refused, in place of err.
func (b *Bearer) Explain(err error) error {
	var rejected *RejectedError
	if err == nil || errors.As(err, &rejected) {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rejected == nil {
		return err
	}
	explained := *b.rejected
	explained.Err = err
	if msg := err.Error(); b.refusedToken != "" && strings.Contains(msg, b.refusedToken) {
		explained.Err = errors.New(b.redact(msg))
	}
	return &explained
}

// reject remembers the refusal resp, with the start of its body, which it
// leaves for the SDK to read whole.
func (b *Bearer) reject(resp *http.Response, token string) {
	// A read error recurs when the SDK reads the rest of the body.
	head, _ := io.ReadAll(io.LimitReader(resp.Body, maxRefusalBody))
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(head), resp.Body), Closer: resp.Body}
	rejected := &RejectedError{
		Status: resp.StatusCode,
		Body:   strings.ReplaceAll(string(head), token, redactedText),
	}
	for _, m := range challengeParam.FindAllStringSubmatch(resp.Header.Get("WWW-Authenticate"), -1) {
		value := m[2] + m[3]
		switch strings.ToLower(m[1]) {
		case "error":
			rejected.Code = value
		case "scope":
			rejected.Scope = value
		case "error_description":
			rejected.Description = strings.ReplaceAll(value, token, redactedText)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rejected, b.refusedToken = rejected, token
}

type readCloser struct {
	io.Reader
	io.Closer
}

func (b *Bearer) redact(text string) string {
	if b.refusedToken == "" {
		return text
	}
	return strings.ReplaceAll(text, b.refusedToken, redactedText)
}

// providerMessage returns the message of the JSON-RPC error that resp
// carries, or its status text, as the MCP client reports a refusal.
func providerMessage(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRefusalBody))
	if msg, err := jsonrpc.DecodeMessage(body); err == nil {
		if r, ok := msg.(*jsonrpc.Response); ok && r.Error != nil {
			return r.Error.Error()
		}
	}
	return http.StatusText(resp.StatusCode)
}

// NewClient returns Dagu's MCP client for a session that uses a connection.
// It advertises form elicitation, as Spec 107 requires of such a client,
// and declines every request at once: only a person accepts one. onElicit,
// when set, receives each request before it is declined.
func NewClient(onElicit func(*sdk.ElicitRequest)) *sdk.Client {
	return sdk.NewClient(&sdk.Implementation{Name: "dagu", Version: config.Version}, &sdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			if onElicit != nil {
				onElicit(req)
			}
			return &sdk.ElicitResult{Action: "decline"}, nil
		},
	})
}
