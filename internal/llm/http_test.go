// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
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

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
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
	assert.EqualValues(t, 2, calls.Load())
	assert.Contains(t, logs.String(), "retrying")
	assert.Contains(t, logs.String(), "status=529")
	assert.NotContains(t, logs.String(), "secret-from-request")
}

func TestHTTPRetryStatus(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500, 501, 502, 503, 504, 505, 529} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
			}))
			defer server.Close()
			cfg := DefaultConfig()
			cfg.MaxRetries = 1
			cfg.InitialInterval = time.Millisecond
			_, err := NewHTTPClient(cfg).Do(t.Context(), server.URL, nil, nil)
			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			expected := int32(1)
			if apiErr.Retryable {
				expected++
			}
			assert.Equal(t, expected, calls.Load())
			if status == http.StatusNotImplemented {
				assert.False(t, apiErr.Retryable)
			}
		})
	}
}

func TestHTTPErrorBodyLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("x", 65*1024))
	}))
	defer server.Close()
	_, err := NewHTTPClient(DefaultConfig()).Do(t.Context(), server.URL, nil, nil)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 64*1024, len(apiErr.Message))
}
