// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithRemoteNode_ControllerEndpointsAreLocalOnly(t *testing.T) {
	t.Parallel()

	for _, requestURL := range []string{
		"/api/v1/controllers?remoteNode=local",
		"/api/v1/controllers/ctrl_aaaaaaaaaaaaaaaa?remoteNode=server-2",
	} {
		t.Run(requestURL, func(t *testing.T) {
			t.Parallel()

			called := false
			handler := WithRemoteNode(nil, "/api/v1")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestURL, nil))

			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.False(t, called)
		})
	}
}

func TestWithRemoteNode_ControllerEndpointWithoutRemoteNodeContinuesLocally(t *testing.T) {
	t.Parallel()

	called := false
	handler := WithRemoteNode(nil, "/base/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/base/api/v1/controllers/ctrl_aaaaaaaaaaaaaaaa", nil))

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.True(t, called)
}
