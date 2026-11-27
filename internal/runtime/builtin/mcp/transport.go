// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

// maxRateLimitBody bounds the part of an HTTP 429 body read for the
// provider's message.
const maxRateLimitBody = 4 << 10

// observingTransport injects static headers and remembers the first
// authentication rejection, rate limit, unavailable status, or network
// failure, which the SDK does not expose as typed errors.
type observingTransport struct {
	base    http.RoundTripper
	headers map[string]string

	mu      sync.Mutex
	authz   int
	redir   int
	unavail int
	limited *rateLimit
	netFail error
}

func (t *observingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	cancel := func() {}
	if req.Method == http.MethodDelete {
		ctx, cancel = context.WithTimeout(ctx, closeTimeout)
	}
	req = req.Clone(ctx)
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		cancel()
	} else {
		resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel, obs: t}
	}
	var limit *rateLimit
	if err == nil && resp.StatusCode == http.StatusTooManyRequests {
		limit = newRateLimit(resp)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case err == nil && t.redir == 0 && resp.StatusCode >= 300 && resp.StatusCode < 400:
		t.redir = resp.StatusCode
	case err != nil && t.netFail == nil:
		t.netFail = err
	case err == nil && t.authz == 0 &&
		(resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden):
		t.authz = resp.StatusCode
	case limit != nil && t.limited == nil:
		t.limited = limit
	case err == nil && t.unavail == 0 && isUnavailableStatus(resp.StatusCode):
		t.unavail = resp.StatusCode
	}
	return resp, err
}

// isUnavailableStatus reports the statuses the SDK treats as transient server
// failures, apart from rate limiting.
func isUnavailableStatus(code int) bool {
	switch code {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// rateLimit is an HTTP 429 answer: the provider's message and its
// Retry-After, as sent.
type rateLimit struct {
	message    string
	retryAfter string
}

// newRateLimit reads the start of resp's body for the provider's message,
// and leaves the whole body for the SDK to read.
func newRateLimit(resp *http.Response) *rateLimit {
	// A read error recurs when the SDK reads the rest of the body.
	head, _ := io.ReadAll(io.LimitReader(resp.Body, maxRateLimitBody))
	resp.Body = readCloser{Reader: io.MultiReader(bytes.NewReader(head), resp.Body), Closer: resp.Body}
	return &rateLimit{message: providerMessage(head), retryAfter: resp.Header.Get("Retry-After")}
}

// providerMessage returns the message of a JSON error body, as JSON-RPC and
// Google APIs send one, or the body as text.
func providerMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error.Message != "" {
		return payload.Error.Message
	}
	return strings.TrimSpace(string(body))
}

func (l *rateLimit) callError(redact redactor) *callError {
	msg := "server responded with HTTP 429 " + http.StatusText(http.StatusTooManyRequests)
	if l.message != "" {
		msg += ": " + redact.apply(l.message)
	}
	if l.retryAfter != "" {
		msg += "; retry after " + l.retryAfter
	}
	return &callError{kind: kindRateLimited, message: msg, retryAfter: l.retryAfter}
}

type readCloser struct {
	io.Reader
	io.Closer
}

func (t *observingTransport) rateLimit() *rateLimit {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.limited
}

func (t *observingTransport) authStatus() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.authz
}

func (t *observingTransport) recordNetworkError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.netFail == nil {
		t.netFail = err
	}
}

func (t *observingTransport) networkError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.netFail
}

func (t *observingTransport) redirectStatus() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.redir
}

func (t *observingTransport) unavailableStatus() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.unavail
}

// cancelBody releases the per-request context when the body is closed and
// reports a read failure after the response headers as a network error.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	obs    *observingTransport
}

func (b *cancelBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.obs.recordNetworkError(err)
	}
	return n, err
}

func (b *cancelBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}
