// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stepoauth

import (
	"io"
	"net/http"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/oauth"
)

// drainLimit bounds what is read of a rejected response so its connection
// can be reused.
const drainLimit = 4 << 10

// TokenError reports that a request was not sent because no access token
// could be obtained.
type TokenError struct {
	err error
}

func (e *TokenError) Error() string { return "oauth: " + e.err.Error() }

func (e *TokenError) Unwrap() error { return e.err }

// transport sends each request with a bearer access token and sends a
// request the server rejects with HTTP 401 once more with a new token.
type transport struct {
	base   http.RoundTripper
	source *oauth.Source
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.source.Token(req.Context())
	if err != nil {
		closeBody(req)
		return nil, &TokenError{err: err}
	}
	resp, err := t.base.RoundTrip(authorized(req, token))
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	again, ok := rewind(req)
	if !ok {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimit))
	_ = resp.Body.Close()

	t.source.Invalidate(token)
	token, err = t.source.Token(req.Context())
	if err != nil {
		closeBody(again)
		return nil, &TokenError{err: err}
	}
	return t.base.RoundTrip(authorized(again, token))
}

// authorized returns a copy of req that carries token.
func authorized(req *http.Request, token *oauth2.Token) *http.Request {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	return req
}

// rewind returns req with its body from the start, or false when the body
// cannot be read again.
func rewind(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	again := *req
	again.Body = body
	return &again, true
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}
