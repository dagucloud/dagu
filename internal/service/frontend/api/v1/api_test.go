// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Strict validation stays off so the generated handler, not the OpenAPI
// validator, decodes the body.
func TestConfigureRoutesInvalidJSON(t *testing.T) {
	a := &API{
		config: &config.Config{
			Server: config.Server{
				APIBasePath: "/api/v1",
				Auth:        config.Auth{Mode: config.AuthModeNone},
			},
		},
	}
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		require.NoError(t, a.ConfigureRoutes(t.Context(), r, time.Second))
	})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/sync/publish-all", strings.NewReader(`{"`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	var body apiv1.Error
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&body))
	assert.Equal(t, apiv1.ErrorCodeBadRequest, body.Code)
}
