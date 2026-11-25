// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package http

import (
	"io"
	"maps"
	nethttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/stepoauth/stepoauthtest"
)

func oauthStep(url string, extra map[string]any) ir.Step {
	config := map[string]any{
		"method": "POST",
		"url":    url,
		"body":   `{"summary":"standup"}`,
		"oauth": map[string]any{
			"provider": "dagu-cloud",
			"account":  "me@example.com",
			"scopes":   []any{"https://www.googleapis.com/auth/calendar.events"},
		},
	}
	maps.Copy(config, extra)
	return ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "http", Config: config}}
}

// The API rejects the first token, so the request succeeds only by being
// sent again, body included, with a new one.
func TestHTTPExecutor_OAuth(t *testing.T) {
	t.Parallel()
	console := stepoauthtest.NewConsole(t,
		stepoauthtest.Token("revoked", time.Hour),
		stepoauthtest.Token("fresh", time.Hour),
	)
	var (
		mu     sync.Mutex
		seen   []string
		bodies []string
	)
	api := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		bodies = append(bodies, string(body))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(nethttp.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"id":"event-1"}`)
	}))
	t.Cleanup(api.Close)

	ctx := console.Context(t.Context())
	exec, err := newHTTP(ctx, oauthStep(api.URL, map[string]any{"silent": true}))
	require.NoError(t, err)
	out := &testWriter{}
	exec.SetStdout(out)
	require.NoError(t, exec.Run(ctx))

	assert.Equal(t, `{"id":"event-1"}`, out.String())
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"Bearer revoked", "Bearer fresh"}, seen)
	assert.Equal(t, []string{`{"summary":"standup"}`, `{"summary":"standup"}`}, bodies)
}

func TestHTTPExecutor_OAuthRejectsAuthorizationHeader(t *testing.T) {
	t.Parallel()
	step := oauthStep("https://www.googleapis.com/calendar/v3/calendars/primary/events",
		map[string]any{"headers": map[string]any{"Authorization": "Bearer static"}})

	require.EqualError(t, registry.ValidateStep(step),
		"http executor: oauth cannot be combined with an Authorization header")
	_, err := newHTTP(t.Context(), step)
	require.EqualError(t, err, "http executor: oauth cannot be combined with an Authorization header")
}

func TestHTTPExecutor_OAuthSchema(t *testing.T) {
	t.Parallel()
	valid := oauthStep("https://example.com", nil).ExecutorConfig.Config
	require.NoError(t, registry.ValidateExecutorConfig("http", valid))

	unknown := oauthStep("https://example.com", map[string]any{
		"oauth": map[string]any{"provider": "github"},
	}).ExecutorConfig.Config
	require.ErrorContains(t, registry.ValidateExecutorConfig("http", unknown), "oauth")
}
