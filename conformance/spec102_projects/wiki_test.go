// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestDefaultWikiLeavesOutMigratedProject proves Spec 102 "Wiki of Migrated
// Projects". Migration made project ops from workspace ops, whose pages are
// in ops/ of the default project's wiki directory. With projects on, the
// default project's wiki lists, reads, and writes none of them. With
// projects off, the wiki holds them as on v2.
func TestDefaultWikiLeavesOutMigratedProject(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	home := filepath.Join(t.TempDir(), "dagu")
	for rel, content := range map[string]string{
		"dags/wiki/home.md":        "home\n",
		"dags/wiki/ops/runbook.md": "runbook\n",
		// The registry is written as migration would write it.
		"data/projects.json": `{"schema_version":1,"projects":[` +
			`{"id":"7f3e9a1c-2b4d-4e6f-8a0b-1c2d3e4f5a6b","name":"ops","display_name":"ops",` +
			`"created_at":"2026-01-01T00:00:00Z","migrated_from":"ops","slot_seed":"data"}]}`,
	} {
		path := filepath.Join(home, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	env := []string{"DAGU_HOME=" + home}

	t.Run("projects on", func(t *testing.T) {
		base := startWikiServer(t, dagu, append(slices.Clone(env), "DAGU_EXPERIMENTAL_PROJECTS=true"))

		code, body := get(t, base+"/wiki?flat=true")
		require.Equal(t, http.StatusOK, code, "%s", body)
		require.Contains(t, body, `"id":"home"`)
		require.NotContains(t, body, "runbook")

		code, body = get(t, base+"/wiki/page?path=ops/runbook")
		require.Equal(t, http.StatusNotFound, code, "%s", body)

		for _, write := range []struct{ method, path, contentType, body string }{
			{http.MethodPost, "/wiki", "application/json", `{"id":"ops/notes","content":"x"}`},
			{http.MethodPatch, "/wiki/page?path=ops/runbook", "application/json", `{"content":"x"}`},
			{http.MethodPut, "/wiki/page/attachment?path=ops/runbook&name=a.txt", "application/octet-stream", "x"},
			{http.MethodPost, "/wiki/page/rename?path=home", "application/json", `{"newPath":"ops/home"}`},
			{http.MethodDelete, "/wiki/page?path=ops/runbook", "", ""},
		} {
			code, body = request(t, write.method, base+write.path, write.contentType, write.body)
			require.Equal(t, http.StatusConflict, code, "%s %s: %s", write.method, write.path, body)
		}
		require.NoFileExists(t, filepath.Join(home, "dags", "wiki", "ops", "notes.md"))
		require.FileExists(t, filepath.Join(home, "dags", "wiki", "ops", "runbook.md"))
	})

	t.Run("projects off", func(t *testing.T) {
		base := startWikiServer(t, dagu, env)

		code, body := get(t, base+"/wiki?flat=true")
		require.Equal(t, http.StatusOK, code, "%s", body)
		require.Contains(t, body, `"id":"ops/runbook"`)
	})
}

// startWikiServer starts a server with env and returns the base URL of its
// API once it is healthy.
func startWikiServer(t *testing.T, dagu *harness.Runner, env []string) string {
	t.Helper()
	port := harness.FreePort(t)
	server := dagu.StartWithEnv(env, "server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port))
	t.Cleanup(server.Stop)
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)
	waitForHealth(t, server, base, func(body string) bool {
		return strings.Contains(body, `"status":"healthy"`)
	})
	return base
}

// request returns the status code and body of a request with a body of
// the given content type.
func request(t *testing.T, method, url, contentType, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(answer)
}
