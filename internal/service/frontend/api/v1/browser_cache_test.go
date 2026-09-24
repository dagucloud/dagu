// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cache is keyed by the DAG name, which differs from the file name here.
func TestClearDAGBrowserCache(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	dag := server.DAG(t, `name: billing
steps:
  - name: "1"
    run: echo "hello"
`)
	fileName := strings.TrimSuffix(filepath.Base(dag.Location), filepath.Ext(dag.Location))
	seedBrowserReplayCache(t, server, "billing", "login", "download")

	assert.Equal(t, []string{"login"}, clearBrowserCache(t, server, fileName+"/browser-cache?step=login", ""))
	assert.Equal(t, []string{"download"}, clearBrowserCache(t, server, fileName+"/browser-cache", ""))
	assert.Empty(t, clearBrowserCache(t, server, fileName+"/browser-cache", ""))
}

func TestClearDAGBrowserCacheUnknownDAG(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)
	server.Client().Delete("/api/v1/dags/missing/browser-cache").
		ExpectStatus(http.StatusNotFound).
		Send(t)
}

func TestClearDAGBrowserCacheRequiresExecute(t *testing.T) {
	t.Parallel()

	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	viewerKey := createAPIKeyForRole(t, server, adminToken, "viewer-browser-cache", api.UserRoleViewer)
	operatorKey := createAPIKeyForRole(t, server, adminToken, "operator-browser-cache", api.UserRoleOperator)

	spec := fmt.Sprintf(`
steps:
  - %s
`, test.ShellQuote("exit 0"))
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "billing",
		Spec: &spec,
	}).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusCreated).
		Send(t)
	seedBrowserReplayCache(t, server, "billing", "login")

	server.Client().Delete("/api/v1/dags/billing/browser-cache").
		WithBearerToken(viewerKey).
		ExpectStatus(http.StatusForbidden).
		Send(t)
	assert.Equal(t, []string{"login"}, clearBrowserCache(t, server, "billing/browser-cache", operatorKey))
}

func seedBrowserReplayCache(t *testing.T, server test.Server, dagName string, steps ...string) {
	t.Helper()
	cache := browserhost.NewReplayCache(filepath.Join(server.Config.Paths.DataDir, browserhost.DataDirName))
	for _, step := range steps {
		path := cache.Path(dagName, step)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

// clearBrowserCache sends the request under /api/v1/dags/ and returns the
// removed steps. An empty token sends no credentials.
func clearBrowserCache(t *testing.T, server test.Server, path, token string) []string {
	t.Helper()
	req := server.Client().Delete("/api/v1/dags/" + path)
	if token != "" {
		req = req.WithBearerToken(token)
	}
	resp := req.ExpectStatus(http.StatusOK).Send(t)

	var body api.DAGBrowserCacheClearResult
	resp.Unmarshal(t, &body)
	return body.Steps
}
