// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"net/http"
	"sync"
)

// observingTransport injects static headers and remembers the first
// authentication rejection or network failure, which the SDK does not expose
// as typed errors.
type observingTransport struct {
	base    http.RoundTripper
	headers map[string]string

	mu      sync.Mutex
	authz   int
	redir   int
	netFail error
}

func (t *observingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.base.RoundTrip(req)
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
	}
	return resp, err
}

func (t *observingTransport) authStatus() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.authz
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
