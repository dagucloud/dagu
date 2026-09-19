// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPRetry(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(statusOverloaded)
			_, _ = io.WriteString(w, `{"error":"secret-from-request"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.InitialInterval = time.Millisecond
	body, err := NewHTTPClient(cfg).Do(t.Context(), server.URL, nil, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, body.Close()) }()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(data))
	assert.Equal(t, 2, calls)
	assert.Contains(t, logs.String(), "retrying")
	assert.NotContains(t, logs.String(), "secret-from-request")
}
