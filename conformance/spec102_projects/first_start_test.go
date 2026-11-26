// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestFirstStartWaitsForV2Scheduler proves Spec 102 "First Start" and the
// migration errors. While a v2 scheduler is in the service registry, the
// first server start with projects on waits to migrate: its health endpoint
// answers 200 with the progress, which names the v2 scheduler, and every
// other request gets 503. Once the v2 scheduler leaves the registry, the
// migration finishes and the server serves.
func TestFirstStartWaitsForV2Scheduler(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	home := filepath.Join(t.TempDir(), "dagu")
	entry := filepath.Join(home, "data", "service-registry", "scheduler", "v2-scheduler.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(entry), 0o750))
	require.NoError(t, os.WriteFile(entry, []byte(
		`{"id":"v2-scheduler","host":"old-host","port":8090,"pid":1,"status":1,"startedAt":"2026-01-01T00:00:00Z"}`,
	), 0o600))

	port := harness.FreePort(t)
	server := dagu.StartWithEnv(
		[]string{"DAGU_HOME=" + home, "DAGU_EXPERIMENTAL_PROJECTS=true"},
		"server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port),
	)
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)

	waitForHealth(t, server, base, func(body string) bool {
		// The registry drops an entry not renewed for 30 seconds.
		now := time.Now()
		require.NoError(t, os.Chtimes(entry, now, now))
		return strings.Contains(body, `"phase":"waiting-for-v2"`) &&
			strings.Contains(body, "scheduler v2-scheduler on old-host")
	})
	code, body := get(t, base+"/dags")
	require.Equal(t, http.StatusServiceUnavailable, code, "%s", body)

	require.NoError(t, os.Remove(entry))
	waitForHealth(t, server, base, func(body string) bool {
		return strings.Contains(body, `"status":"healthy"`)
	})
	code, body = get(t, base+"/dags")
	require.NotContains(t, []int{0, http.StatusServiceUnavailable}, code, "%s", body)
}

// waitForHealth polls the health endpoint until it answers 200 with a body
// that ready accepts.
func waitForHealth(t *testing.T, server *harness.Process, base string, ready func(body string) bool) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	last := ""
	for time.Now().Before(deadline) {
		select {
		case <-server.Done():
			require.FailNowf(t, "the server exited", "%s", server.FailureOutput())
		default:
		}
		code, body := get(t, base+"/health")
		if code == http.StatusOK && ready(body) {
			return
		}
		last = fmt.Sprintf("%d %s", code, body)
		time.Sleep(50 * time.Millisecond)
	}
	require.FailNowf(t, "the health endpoint did not answer as expected", "last answer: %s\n%s", last, server.Output())
}

// get returns the status code and body of a GET request, or 0 while the
// server does not accept connections.
func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}
