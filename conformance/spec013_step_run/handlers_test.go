// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec013_step_run_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handlersDAG is a chain a > b > c with handlers that leave marker files in
// the directory given by the MARKERS constant. The step c is the end step.
func handlersDAG(markers string, failAt string) string {
	return fmt.Sprintf(`steps:
  - name: a
    run: "true"
  - name: b
    depends: [a]
    run: %q
  - name: c
    depends: [b]
    run: "true"
handler_on:
  init:
    run: 'touch %[2]s/init'
  success:
    run: 'touch %[2]s/success'
  failure:
    run: 'touch %[2]s/failure'
  exit:
    run: 'touch %[2]s/exit'
`, failAt, markers)
}

func markerNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// A selected-steps run calls the handlers of the DAG that run after the steps
// only when the selection has a graph end step. A handler that is not called
// leaves no node in the run status. The init handler runs as for any run.
func TestSelectedStepsCallHandlersOnlyForEndStep(t *testing.T) {
	server := newServer(t)

	tests := []struct {
		name      string
		steps     []string
		failAt    string
		wantRun   api.Status
		wantFiles []string
		wantNodes bool
	}{
		{name: "interior", steps: []string{"b"}, failAt: "true", wantRun: api.StatusSuccess, wantFiles: []string{"init"}},
		{name: "first_and_interior", steps: []string{"a", "b"}, failAt: "true", wantRun: api.StatusSuccess, wantFiles: []string{"init"}},
		{name: "interior_failure", steps: []string{"b"}, failAt: "exit 1", wantRun: api.StatusFailed, wantFiles: []string{"init"}},
		{name: "end", steps: []string{"c"}, failAt: "true", wantRun: api.StatusSuccess, wantFiles: []string{"exit", "init", "success"}, wantNodes: true},
		{name: "interior_and_end", steps: []string{"b", "c"}, failAt: "true", wantRun: api.StatusSuccess, wantFiles: []string{"exit", "init", "success"}, wantNodes: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markers := t.TempDir()
			dagName := "handlers_" + tt.name
			createDAG(t, server, dagName, handlersDAG(markers, tt.failAt))

			runID := startRun(t, server, "/api/v1/dags/"+dagName+"/start", map[string]any{"steps": tt.steps})
			waitForStatus(t, server, dagName, runID, tt.wantRun)

			// The final status is written after the handlers, so the markers
			// are complete once the run has ended.
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				assert.Equal(c, tt.wantFiles, markerNames(t, markers))
			}, timeout, 200*time.Millisecond)
			time.Sleep(500 * time.Millisecond)
			assert.Equal(t, tt.wantFiles, markerNames(t, markers))

			var details api.GetDAGRunDetails200JSONResponse
			server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, runID)).
				ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
			if tt.wantNodes {
				assert.NotNil(t, details.DagRunDetails.OnSuccess)
				assert.NotNil(t, details.DagRunDetails.OnExit)
			} else {
				assert.Nil(t, details.DagRunDetails.OnSuccess)
				assert.Nil(t, details.DagRunDetails.OnFailure)
				assert.Nil(t, details.DagRunDetails.OnExit)
			}
			assert.FileExists(t, filepath.Join(markers, "init"))
		})
	}
}

// A run of the whole DAG calls the handlers as before.
func TestFullRunCallsHandlers(t *testing.T) {
	server := newServer(t)
	markers := t.TempDir()
	const dagName = "handlers_full"
	createDAG(t, server, dagName, handlersDAG(markers, "true"))

	runID := startRun(t, server, "/api/v1/dags/"+dagName+"/start", map[string]any{})
	waitForStatus(t, server, dagName, runID, api.StatusSuccess)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"exit", "init", "success"}, markerNames(t, markers))
	}, timeout, 200*time.Millisecond)
}
