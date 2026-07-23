// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

type openAPIDocument struct {
	Servers []struct {
		URL string `json:"url"`
	} `json:"servers"`
	Paths map[string]json.RawMessage `json:"paths"`
}

func TestOpenapiJSON_StrictValidation(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.StrictValidation = true
	}))

	resp := server.Client().Get("/api/v1/openapi.json").
		ExpectStatus(http.StatusOK).
		Send(t)

	doc := decodeOpenAPIDocument(t, resp.Body)
	require.Equal(t, "/api/v1", openAPIServerURL(t, doc))
	require.Contains(t, doc.Paths, "/openapi.json")
}

func TestHumanTaskCompletionBodyLimit(t *testing.T) {
	server := test.SetupServer(t)

	response := server.Client().Post(
		"/api/v1/dag-runs/test/run-1/human-tasks/review/complete",
		strings.Repeat("x", 16<<20),
	).ExpectStatus(http.StatusRequestEntityTooLarge).Send(t)

	var apiError struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal([]byte(response.Body), &apiError))
	require.Equal(t, "payload_too_large", apiError.Code)
}

func TestOpenapiJSON_BuiltinAuth(t *testing.T) {
	t.Parallel()

	server := builtinServer(t)

	server.Client().Get("/api/v1/openapi.json").
		ExpectStatus(http.StatusUnauthorized).
		Send(t)

	token := loginAndGetToken(t, server, "admin", "adminpass")
	resp := server.Client().Get("/api/v1/openapi.json").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).
		Send(t)

	doc := decodeOpenAPIDocument(t, resp.Body)
	require.Equal(t, "/api/v1", openAPIServerURL(t, doc))
	responses := openAPIResponses(t, doc, "/openapi.json")
	require.Contains(t, responses, "401")
	require.NotContains(t, responses, "403")
}

func TestOpenapiJSON_MountedServerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		basePath    string
		apiBasePath string
		requestPath string
		wantServer  string
	}{
		{
			name:        "default base path",
			basePath:    "",
			apiBasePath: "/api/v1",
			requestPath: "/api/v1/openapi.json",
			wantServer:  "/api/v1",
		},
		{
			name:        "non-root base path",
			basePath:    "/dagu",
			apiBasePath: "/api/v1",
			requestPath: "/dagu/api/v1/openapi.json",
			wantServer:  "/dagu/api/v1",
		},
		{
			name:        "non-root base and custom api path",
			basePath:    "/dagu",
			apiBasePath: "/rest",
			requestPath: "/dagu/rest/openapi.json",
			wantServer:  "/dagu/rest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
				cfg.Server.BasePath = tt.basePath
				cfg.Server.APIBasePath = tt.apiBasePath
				cfg.Server.StrictValidation = true
			}))

			resp := server.Client().Get(tt.requestPath).
				ExpectStatus(http.StatusOK).
				Send(t)

			doc := decodeOpenAPIDocument(t, resp.Body)
			serverURL := openAPIServerURL(t, doc)
			require.Equal(t, tt.wantServer, serverURL)
			require.NotContains(t, serverURL, "://")
		})
	}
}

func decodeOpenAPIDocument(t *testing.T, body string) openAPIDocument {
	t.Helper()

	var doc openAPIDocument
	require.NoError(t, json.Unmarshal([]byte(body), &doc))
	return doc
}

func openAPIServerURL(t *testing.T, doc openAPIDocument) string {
	t.Helper()

	require.Len(t, doc.Servers, 1)
	return doc.Servers[0].URL
}

func openAPIResponses(t *testing.T, doc openAPIDocument, path string) map[string]json.RawMessage {
	t.Helper()

	pathDoc, ok := doc.Paths[path]
	require.True(t, ok)

	var operation struct {
		Get struct {
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"get"`
	}
	require.NoError(t, json.Unmarshal(pathDoc, &operation))
	return operation.Get.Responses
}
