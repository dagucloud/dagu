// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/dagrun/humantask"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanTaskInputMiddlewarePreservesValidatedBody(t *testing.T) {
	const raw = `{"count":9007199254740993}`
	originalBody := &trackingReadCloser{Reader: strings.NewReader(raw)}
	called := false
	handler := humanTaskInputMiddleware("/base/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		input, ok := r.Context().Value(humanTaskInputContextKey{}).(humantask.Input)
		require.True(t, ok)
		assert.Equal(t, json.Number("9007199254740993"), input.Values["count"])
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, raw, string(body))
		assert.Equal(t, int64(len(raw)), r.ContentLength)
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(
		http.MethodPost,
		"/base/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete",
		originalBody,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.True(t, called)
	assert.True(t, originalBody.closed)
	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestHumanTaskInputMiddlewareRejectsAmbiguousJSON(t *testing.T) {
	for _, raw := range []string{
		`{} {}`,
		`{"nested":{"confirmed":true,"confirmed":false}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			called := false
			handler := humanTaskInputMiddleware("/api/v1")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete",
				strings.NewReader(raw),
			)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.False(t, called)
			assert.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
}

func TestHumanTaskInputMiddlewareRejectsOversizedBody(t *testing.T) {
	called := false
	handler := humanTaskInputMiddlewareWithLimit("/api/v1", 4)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete",
		strings.NewReader(`{"confirmed":true}`),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.False(t, called)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	var apiError struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &apiError))
	assert.Equal(t, "payload_too_large", apiError.Code)
}

func TestHumanTaskInputMiddlewareValidatesBeforeRemoteProxy(t *testing.T) {
	called := false
	handler := humanTaskInputMiddleware("/api/v1")(
		WithRemoteNode(nil, "/api/v1")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called = true
		})),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete?remoteNode=edge",
		strings.NewReader(`{"confirmed":true,"confirmed":false}`),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.False(t, called)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestHumanTaskInputMiddlewareIgnoresOtherRoutes(t *testing.T) {
	called := false
	handler := humanTaskInputMiddleware("/api/v1")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/dag-runs/deploy/run-1/retry", strings.NewReader(`not json`))

	handler.ServeHTTP(httptest.NewRecorder(), request)

	assert.True(t, called)
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (b *trackingReadCloser) Close() error {
	b.closed = true
	return nil
}
