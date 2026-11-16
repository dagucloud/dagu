// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package a2a

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// maxResponseBytes bounds every response body, since the SDK decodes bodies
// without a limit.
const maxResponseBytes = 32 << 20

var errBodyTooLarge = fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)

// statusError reports a response that is refused instead of followed: a
// redirect, or a rejection of the credentials.
type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

// observer remembers what the SDK does not expose as typed errors.
type observer struct {
	mu     sync.Mutex
	status int
	netErr error
}

func (o *observer) setStatus(code int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.status = code
}

func (o *observer) setNetErr(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.netErr == nil {
		o.netErr = err
	}
}

func (o *observer) snapshot() (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.status, o.netErr
}

// guard sends only https requests, refuses redirects and credential
// rejections, applies headers only to requests for its own origin, and caps
// response bodies.
type guard struct {
	base    http.RoundTripper
	origin  string
	headers map[string]string
	obs     *observer
}

// originOf returns scheme://host:port with the default port made explicit,
// so https://h and https://h:443 are one origin.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[scheme]
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func (g *guard) client() *http.Client {
	return &http.Client{
		Transport:     g,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.User != nil {
		return nil, errors.New("only https URLs without credentials are allowed")
	}
	req = req.Clone(req.Context())
	if originOf(req.URL) == g.origin {
		for k, v := range g.headers {
			req.Header.Set(k, v)
		}
	}
	resp, err := g.base.RoundTrip(req)
	if err != nil {
		g.obs.setNetErr(err)
		return nil, err
	}
	g.obs.setStatus(resp.StatusCode)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = resp.Body.Close()
		return nil, &statusError{code: resp.StatusCode}
	}
	resp.Body = &limitBody{ReadCloser: resp.Body, left: maxResponseBytes, obs: g.obs}
	return resp, nil
}

// limitBody caps the body and reports a read failure after the response
// headers, such as a dropped connection, to the observer.
type limitBody struct {
	io.ReadCloser
	left int64
	obs  *observer
}

func (b *limitBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		b.obs.setNetErr(errBodyTooLarge)
		return 0, errBodyTooLarge
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		b.obs.setNetErr(err)
	}
	return n, err
}
