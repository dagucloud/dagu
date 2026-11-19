// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type headerRecorder struct{ seen map[string]string }

func (r *headerRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.seen[req.URL.String()] = req.Header.Get("Authorization")
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

// A server of an inline MCP entry can answer with a redirect to another host.
// The credentials of the entry go to the origin of the entry only.
func TestHeaderTransportKeepsTheCredentialsOnTheOrigin(t *testing.T) {
	t.Parallel()
	rec := &headerRecorder{seen: map[string]string{}}
	origin, err := url.Parse("https://mcp.example.com/mcp")
	require.NoError(t, err)
	transport := headerTransport{base: rec, origin: originOf(origin), headers: map[string]string{"Authorization": "Bearer t0ken"}}
	for _, target := range []string{"https://MCP.example.com/mcp", "https://other.example.com/steal", "http://mcp.example.com/mcp"} {
		req, err := http.NewRequest(http.MethodPost, target, http.NoBody)
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	assert.Equal(t, "Bearer t0ken", rec.seen["https://MCP.example.com/mcp"])
	assert.Empty(t, rec.seen["https://other.example.com/steal"])
	assert.Empty(t, rec.seen["http://mcp.example.com/mcp"], "a change of scheme is another origin")
}

// A server can answer with a redirect. The client follows none, so the body of a
// request, with the arguments of a call, never goes to another server.
func TestMCPClientFollowsNoRedirect(t *testing.T) {
	t.Parallel()
	var reached atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		http.NotFound(w, r)
	}))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	_, err := listMCPTools(context.Background(), origin.URL+"/mcp", nil)
	require.Error(t, err)
	assert.Zero(t, reached.Load(), "the redirect target gets no request")
}

// The URL of an MCP entry can hold a key in its query. No error of a failed
// connection repeats it, also in the error that the HTTP client wraps.
func TestMCPErrorsHideTheURLSecrets(t *testing.T) {
	t.Parallel()
	const secret = "s3cr3t-key"
	server := httptest.NewServer(http.NotFoundHandler())
	closedURL := server.URL
	server.Close()

	_, err := listMCPTools(context.Background(), closedURL+"/mcp?api_key="+secret, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.Contains(t, err.Error(), strings.TrimPrefix(closedURL, "http://")+"/mcp")
	urlErr, ok := errors.AsType[*url.Error](err)
	require.True(t, ok, "the cause stays reachable: %v", err)
	assert.NotContains(t, urlErr.URL, secret)

	_, err = listMCPTools(context.Background(), "https://user:"+secret+"%zz@mcp.example.com/mcp", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
}
