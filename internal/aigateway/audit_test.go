// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

// The audit line is a line of the log. It has the fields of Spec 094, "Audit", and
// never the key, a header value, a body or a model name.
func TestDo_AuditLineHasNoKey(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"claude-secret-model-name"}`))
	}))
	defer upstream.Close()
	src := &fakeSource{ep: endpointFor(upstream.URL, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), key: realKey}
	g := newGateway(t, src, testLimits(), nil)

	buf := &syncBuffer{b: &bytes.Buffer{}}
	ctx := logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(buf)))
	req := request("/v1/messages", `{"model":"claude-secret-model-name","prompt":"top secret prompt"}`)
	req.Header.Set("Authorization", "Bearer header-value-secret")
	require.Nil(t, g.Do(ctx, req, &recSink{}))
	require.NotNil(t, g.Do(ctx, request("/v1/complete", `{}`), &recSink{}))

	buf.mu.Lock()
	line := buf.b.String()
	buf.mu.Unlock()
	assert.Equal(t, 2, strings.Count(line, `"action":"ai_relay_request"`), "one line for each request")
	for _, want := range []string{`"route":"messages"`, `"status":200`, `"target":"cle1"`, `"worker_id":"w1"`, `"run_id":"run-1"`,
		`"key_fingerprint":"0123456789ab"`, `"first_byte_ms":`, `"reason":"path_not_allowed"`, `"request_bytes":65`} {
		assert.Contains(t, line, want)
	}
	for _, secret := range []string{realKey, "header-value-secret", "claude-secret-model-name", "top secret prompt", realKey[:8]} {
		assert.NotContains(t, line, secret)
	}
}
