// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	apiimpl "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsWebhookTriggerPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"webhook trigger", "/api/v1/webhooks/my-dag", true},
		{"webhook trigger with base path", "/base/api/v1/webhooks/my-dag", true},
		{"webhook list (no segment)", "/api/v1/webhooks", false},
		{"webhook list trailing slash", "/api/v1/webhooks/", false},
		{"non-webhook path", "/api/v1/dags", false},
		{"dag webhook management", "/api/v1/dags/my-dag/webhook", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := apiimpl.IsWebhookTriggerPath(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMarshalWebhookPayload_RawBodyFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    *api.WebhookRequest
		rawBody []byte
		want    string
	}{
		{
			name:    "structured payload takes precedence",
			body:    &api.WebhookRequest{Payload: &map[string]any{"key": "val"}},
			rawBody: []byte(`{"event":"push"}`),
			want:    `{"key":"val"}`,
		},
		{
			name:    "falls back to raw body when payload is nil",
			body:    &api.WebhookRequest{},
			rawBody: []byte(`{"event":"push","repo":"foo"}`),
			want:    `{"event":"push","repo":"foo"}`,
		},
		{
			name:    "raw body with dagRunId is passed through",
			body:    &api.WebhookRequest{},
			rawBody: []byte(`{"dagRunId":"abc","event":"push"}`),
			want:    `{"dagRunId":"abc","event":"push"}`,
		},
		{
			name:    "nil body falls back to raw body",
			body:    nil,
			rawBody: []byte(`{"event":"push"}`),
			want:    `{"event":"push"}`,
		},
		{
			name:    "no raw body returns empty object",
			body:    &api.WebhookRequest{},
			rawBody: nil,
			want:    "{}",
		},
		{
			name:    "invalid JSON raw body returns empty object",
			body:    &api.WebhookRequest{},
			rawBody: []byte(`not-json`),
			want:    "{}",
		},
		{
			name:    "empty raw body returns empty object",
			body:    &api.WebhookRequest{},
			rawBody: []byte{},
			want:    "{}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tt.rawBody != nil {
				ctx = apiimpl.WithRawBody(ctx, tt.rawBody)
			}
			got, err := apiimpl.MarshalWebhookPayload(ctx, tt.body)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMarshalWebhookHeaders_Filtering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		allowList []string
		headers   http.Header
		want      string
	}{
		{
			name:      "returns empty object when allowlist is empty",
			allowList: nil,
			headers: http.Header{
				"X-GitHub-Event": {"push"},
			},
			want: "{}",
		},
		{
			name:      "filters configured headers and preserves repeated values",
			allowList: []string{"x-github-event", "x-github-delivery"},
			headers: http.Header{
				"X-GitHub-Event":    {"push"},
				"X-GitHub-Delivery": {"abc", "def"},
				"X-Unrelated":       {"ignored"},
			},
			want: `{"x-github-delivery":["abc","def"],"x-github-event":["push"]}`,
		},
		{
			name:      "authorization is never forwarded",
			allowList: []string{"authorization", "x-github-event"},
			headers: http.Header{
				"Authorization":  {"Bearer top-secret"},
				"X-GitHub-Event": {"push"},
			},
			want: `{"x-github-event":["push"]}`,
		},
		{
			name:      "profile selection header is never forwarded",
			allowList: []string{"x-dagu-profile", "x-github-event"},
			headers: http.Header{
				"X-Dagu-Profile": {"prod"},
				"X-GitHub-Event": {"push"},
			},
			want: `{"x-github-event":["push"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := apiimpl.WithRequestHeaders(context.Background(), tt.headers)
			got, err := apiimpl.MarshalWebhookHeaders(ctx, tt.allowList)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, got)
		})
	}
}

func TestRequestedWebhookProfile(t *testing.T) {
	t.Parallel()

	profileName := api.RuntimeProfileName("prod")
	ctx := apiimpl.WithRequestHeaders(context.Background(), http.Header{
		"X-Dagu-Profile": {"prod", "staging"},
	})

	got, apiErr := apiimpl.RequestedWebhookProfile(ctx, &profileName)
	assert.Empty(t, got)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
}
