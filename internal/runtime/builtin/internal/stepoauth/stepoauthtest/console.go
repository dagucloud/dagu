// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stepoauthtest fakes the Dagu Console endpoint that issues Google
// access tokens to steps with oauth provider dagu-cloud.
package stepoauthtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/license"
)

// Credentials of the server connected to every fake console.
const (
	LicenseID       = "lic-test"
	ServerID        = "srv-test"
	HeartbeatSecret = "heartbeat-secret"
)

// Console is a fake Dagu Console. It answers token requests with its replies
// in order and repeats the last one.
type Console struct {
	server *httptest.Server

	mu       sync.Mutex
	replies  []Reply
	requests []TokenRequest
}

// TokenRequest is a token request the console received.
type TokenRequest struct {
	Path            string   `json:"-"`
	LicenseID       string   `json:"license_id"`
	ServerID        string   `json:"server_id"`
	HeartbeatSecret string   `json:"heartbeat_secret"`
	Account         string   `json:"account"`
	Scopes          []string `json:"scopes"`
}

// Reply is how the console answers one token request.
type Reply struct {
	status int
	body   map[string]any
}

// Token issues token, which expires after ttl.
func Token(token string, ttl time.Duration) Reply {
	return Reply{status: http.StatusOK, body: map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_at":   time.Now().Add(ttl).UTC().Format(time.RFC3339),
		"scopes":       []string{},
	}}
}

// Refusal refuses the request with status and an error body.
func Refusal(status int, code, message, connectURL string) Reply {
	return Reply{status: status, body: map[string]any{
		"code":        code,
		"message":     message,
		"connect_url": connectURL,
	}}
}

// NewConsole starts a console that answers with replies.
func NewConsole(t *testing.T, replies ...Reply) *Console {
	t.Helper()
	c := &Console{replies: replies}
	c.server = httptest.NewServer(http.HandlerFunc(c.serve))
	t.Cleanup(c.server.Close)
	return c
}

func (c *Console) serve(w http.ResponseWriter, r *http.Request) {
	req := TokenRequest{Path: r.URL.Path}
	_ = json.NewDecoder(r.Body).Decode(&req)

	c.mu.Lock()
	c.requests = append(c.requests, req)
	reply := c.replies[0]
	if len(c.replies) > 1 {
		c.replies = c.replies[1:]
	}
	c.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(reply.status)
	_ = json.NewEncoder(w).Encode(reply.body)
}

// Context returns ctx carrying the credentials of a server connected to the
// console.
func (c *Console) Context(ctx context.Context) context.Context {
	creds := license.CloudCredentials{
		CloudURL:        c.server.URL,
		LicenseID:       LicenseID,
		ServerID:        ServerID,
		HeartbeatSecret: HeartbeatSecret,
	}
	return license.WithCloudCredentials(ctx, func() (license.CloudCredentials, bool) { return creds, true })
}

// Requests returns the token requests received so far.
func (c *Console) Requests() []TokenRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]TokenRequest(nil), c.requests...)
}
