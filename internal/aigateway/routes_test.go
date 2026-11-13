// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
)

func TestAllow(t *testing.T) {
	t.Parallel()

	anth, oai := aiendpoint.ProtocolAnthropic, aiendpoint.ProtocolOpenAI
	long := strings.Repeat("a", 201)
	tests := []struct {
		name     string
		protocol aiendpoint.Protocol
		method   string
		path     string
		query    string
		route    string
		reason   aigateway.Reason
	}{
		{"messages", anth, "POST", "/v1/messages", "", aigateway.RouteMessages, ""},
		{"messages beta", anth, "POST", "/v1/messages", "beta=true", aigateway.RouteMessages, ""},
		{"count tokens", anth, "POST", "/v1/messages/count_tokens", "beta=true", aigateway.RouteCountTokens, ""},
		{"models", anth, "GET", "/v1/models", "limit=5&after_id=x&before_id=y", aigateway.RouteModels, ""},
		{"one model", anth, "GET", "/v1/models/claude-sonnet-4-6", "", aigateway.RouteModels, ""},
		{"model with a colon and a slash", anth, "GET", "/v1/models/a:b/c.d_e-f", "", aigateway.RouteModels, ""},
		{"model with dots", anth, "GET", "/v1/models/a..b", "", "", aigateway.ReasonPathNotAllowed},
		{"model too long", anth, "GET", "/v1/models/" + long, "", "", aigateway.ReasonPathNotAllowed},
		{"model with a space", anth, "GET", "/v1/models/a b", "", "", aigateway.ReasonPathNotAllowed},
		{"empty model", anth, "GET", "/v1/models/", "", "", aigateway.ReasonPathNotAllowed},
		{"messages with GET", anth, "GET", "/v1/messages", "", "", aigateway.ReasonMethodNotAllowed},
		{"models with POST", anth, "POST", "/v1/models", "", "", aigateway.ReasonMethodNotAllowed},
		{"query key not allowed", anth, "POST", "/v1/messages", "x=1", "", aigateway.ReasonPathNotAllowed},
		{"query on count tokens only beta", anth, "POST", "/v1/messages/count_tokens", "limit=1", "", aigateway.ReasonPathNotAllowed},
		{"query too long", anth, "POST", "/v1/messages", "beta=" + strings.Repeat("a", 2048), "", aigateway.ReasonPathNotAllowed},
		{"unknown path", anth, "POST", "/v1/complete", "", "", aigateway.ReasonPathNotAllowed},
		{"trailing slash", anth, "POST", "/v1/messages/", "", "", aigateway.ReasonPathNotAllowed},
		{"no v1", anth, "POST", "/messages", "", "", aigateway.ReasonPathNotAllowed},
		{"traversal", anth, "POST", "/v1/messages/../../admin", "", "", aigateway.ReasonPathNotAllowed},
		{"chat completions", oai, "POST", "/v1/chat/completions", "", aigateway.RouteChatCompletions, ""},
		{"responses", oai, "POST", "/v1/responses", "", aigateway.RouteResponses, ""},
		{"openai models", oai, "GET", "/v1/models", "", aigateway.RouteModels, ""},
		{"openai query", oai, "GET", "/v1/models", "limit=1", "", aigateway.ReasonPathNotAllowed},
		{"openai uses no messages", oai, "POST", "/v1/messages", "", "", aigateway.ReasonPathNotAllowed},
		{"anthropic has no chat", anth, "POST", "/v1/chat/completions", "", "", aigateway.ReasonPathNotAllowed},
		{"connect", anth, "CONNECT", "/v1/messages", "", "", aigateway.ReasonMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			route, failure := aigateway.Allow(tc.protocol, tc.method, tc.path, tc.query)
			if tc.reason != "" {
				require.NotNil(t, failure)
				assert.Equal(t, tc.reason, failure.Reason)
				return
			}
			require.Nil(t, failure)
			assert.Equal(t, tc.route, route.Name)
		})
	}
}

func TestUpstreamPath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/anthropic/v1/messages", aigateway.UpstreamPath(aiendpoint.ProtocolAnthropic, "/anthropic", "/v1/messages"))
	assert.Equal(t, "/v1/messages", aigateway.UpstreamPath(aiendpoint.ProtocolAnthropic, "", "/v1/messages"))
	assert.Equal(t, "/openai/v1/chat/completions", aigateway.UpstreamPath(aiendpoint.ProtocolOpenAI, "/openai/v1", "/v1/chat/completions"))
	assert.Equal(t, "/v1/chat/completions", aigateway.UpstreamPath(aiendpoint.ProtocolOpenAI, "/v1/", "/v1/chat/completions"))
}

func TestFilterHeaders(t *testing.T) {
	t.Parallel()
	in := http.Header{
		"Content-Type": {"application/json"}, "Accept": {"*/*"}, "User-Agent": {"cli"},
		"Anthropic-Version": {"v"}, "Anthropic-Beta": {"a", "b"}, "Openai-Beta": {"c"},
		"Cookie": {"x"}, "X-Forwarded-For": {"1.2.3.4"}, "Authorization": {"Bearer x"}, "Host": {"evil"},
	}
	out, size, failure := aigateway.FilterRequestHeaders(in)
	require.Nil(t, failure)
	assert.Len(t, out, 6)
	assert.Empty(t, out.Get("Cookie"))
	assert.Empty(t, out.Get("Authorization"))
	assert.Equal(t, []string{"a", "b"}, out.Values("Anthropic-Beta"))
	assert.Positive(t, size)

	_, _, failure = aigateway.FilterRequestHeaders(http.Header{"Content-Encoding": {"gzip"}})
	require.NotNil(t, failure)

	resp := aigateway.FilterResponseHeaders(http.Header{
		"Retry-After": {"5"}, "Set-Cookie": {"a"}, "Connection": {"close"}, "Transfer-Encoding": {"chunked"},
		"Proxy-Authenticate": {"x"}, "Proxy-Foo": {"x"}, "Content-Type": {"text/event-stream"},
		"X-Ratelimit-Limit": {"1"}, "Upgrade": {"h2c"},
	})
	assert.Equal(t, "5", resp.Get("Retry-After"))
	assert.Equal(t, "1", resp.Get("X-Ratelimit-Limit"))
	assert.Equal(t, "text/event-stream", resp.Get("Content-Type"))
	for _, name := range []string{"Set-Cookie", "Connection", "Transfer-Encoding", "Proxy-Authenticate", "Proxy-Foo", "Upgrade"} {
		assert.Empty(t, resp.Get(name), name)
	}
}

func TestReasonStatuses(t *testing.T) {
	t.Parallel()
	want := map[aigateway.Reason]int{
		aigateway.ReasonRelayUnauthorized: 401, aigateway.ReasonHostNotAllowed: 403, aigateway.ReasonPathNotAllowed: 404,
		aigateway.ReasonMethodNotAllowed: 405, aigateway.ReasonEndpointUnknown: 404, aigateway.ReasonEndpointForbidden: 403,
		aigateway.ReasonEndpointProtocolMismatch: 400, aigateway.ReasonEndpointKeyMissing: 503, aigateway.ReasonRequestTooLarge: 413,
		aigateway.ReasonResponseTooLarge: 502, aigateway.ReasonTooManyStreams: 503, aigateway.ReasonEndpointUnavailable: 503,
		aigateway.ReasonRelayUnavailable: 503, aigateway.ReasonUpstreamError: 502, aigateway.ReasonUpstreamTimeout: 504,
	}
	for reason, status := range want {
		assert.Equal(t, status, reason.Status(), string(reason))
	}
	failure, ok := aigateway.ParseFailure("endpoint_forbidden: the DAG is not in the allow list")
	require.True(t, ok)
	assert.Equal(t, aigateway.ReasonEndpointForbidden, failure.Reason)
	assert.Equal(t, "the DAG is not in the allow list", failure.Message)
	_, ok = aigateway.ParseFailure("something else")
	assert.False(t, ok)
}
