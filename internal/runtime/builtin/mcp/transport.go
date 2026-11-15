// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// observingTransport injects static headers and remembers the first
// authentication rejection, unavailable status, or network failure, which the
// SDK does not expose as typed errors.
type observingTransport struct {
	base    http.RoundTripper
	headers map[string]string

	mu      sync.Mutex
	authz   int
	redir   int
	unavail int
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
	case err == nil && t.unavail == 0 && isUnavailableStatus(resp.StatusCode):
		t.unavail = resp.StatusCode
	}
	return resp, err
}

// isUnavailableStatus reports the statuses the SDK treats as transient server
// failures.
func isUnavailableStatus(code int) bool {
	switch code {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout, http.StatusTooManyRequests:
		return true
	}
	return false
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
