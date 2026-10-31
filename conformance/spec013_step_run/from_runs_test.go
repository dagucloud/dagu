// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec013_step_run_test

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fromRunsDAG makes the output of build differ in every run: it is the run
// ID. A run started with FAIL=1 fails at build. The ID of build differs from
// its name, so that a request can name the step either way.
const fromRunsDAG = `params:
  - FAIL=0
steps:
  - name: build
    id: img
    run: |
      if [ "$FAIL" = "1" ]; then exit 1; fi
      T="$DAG_RUN_ID"
      echo "built $T"
      printf "image_tag=%s\n" "$T" >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
  - name: consume
    id: consume
    depends: [build]
    run: 'echo "tag=${steps.img.outputs.image_tag}"'
`

// builtTag reads the value that build printed in a run.
func builtTag(t *testing.T, server test.Server, name, runID string) string {
	t.Helper()
	log := stepLog(t, server, name, runID, "build")
	_, tag, ok := strings.Cut(strings.TrimSpace(log), "built ")
	require.True(t, ok, "log of build: %q", log)
	return strings.TrimSpace(tag)
}

// A step named in outputsFromRuns carries its outputs from that run, in place
// of outputsFromRunId, and the run record says which run and when the step ran.
func TestOutputsFromRunsSeedsTheStep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures assume a POSIX sh is available")
	}
	server := newServer(t)
	const name = "from_runs_seed"
	createDAG(t, server, name, fromRunsDAG)
	path := "/api/v1/dags/" + name + "/start"

	first := startRun(t, server, path, map[string]any{})
	waitForStatus(t, server, name, first, api.StatusSuccess)
	second := startRun(t, server, path, map[string]any{})
	waitForStatus(t, server, name, second, api.StatusSuccess)
	firstTag, secondTag := builtTag(t, server, name, first), builtTag(t, server, name, second)
	require.NotEqual(t, firstTag, secondTag)

	// The main source gives build; outputsFromRuns gives it from the other run.
	runID := startRun(t, server, path, map[string]any{
		"steps":            []string{"consume"},
		"outputsFromRunId": first,
		"outputsFromRuns":  map[string]string{"build": second},
	})
	waitForStatus(t, server, name, runID, api.StatusSuccess)
	assert.Contains(t, stepLog(t, server, name, runID, "consume"), "tag="+secondTag)

	details := runDetails(t, server, name, runID)
	require.NotNil(t, details.ReusedInputs)
	inputs := *details.ReusedInputs
	require.Len(t, inputs, 1)
	assert.Equal(t, "build", inputs[0].Step)
	assert.Equal(t, second, string(inputs[0].RunId))
	assert.Equal(t, api.ReusedInputViaRun, inputs[0].Via)

	// The times are the ones that build had in the source run.
	source := runDetails(t, server, name, second)
	var node *api.Node
	for i := range source.Nodes {
		if source.Nodes[i].Step.Name == "build" {
			node = &source.Nodes[i]
		}
	}
	require.NotNil(t, node)
	require.NotNil(t, inputs[0].StartedAt)
	require.NotNil(t, inputs[0].FinishedAt)
	assert.Equal(t, node.StartedAt, *inputs[0].StartedAt)
	assert.Equal(t, node.FinishedAt, *inputs[0].FinishedAt)
	start, err := time.Parse(time.RFC3339, *inputs[0].StartedAt)
	require.NoError(t, err)
	finish, err := time.Parse(time.RFC3339, *inputs[0].FinishedAt)
	require.NoError(t, err)
	assert.False(t, finish.Before(start))
	require.NotNil(t, inputs[0].CapturedAt)
	assert.Equal(t, *inputs[0].FinishedAt, *inputs[0].CapturedAt)

	// Without outputsFromRunId, the step still takes its outputs from its run.
	alone := startRun(t, server, path, map[string]any{
		"steps":           []string{"consume"},
		"outputsFromRuns": map[string]string{"build": first},
	})
	waitForStatus(t, server, name, alone, api.StatusSuccess)
	assert.Contains(t, stepLog(t, server, name, alone, "consume"), "tag="+firstTag)

	// A step given by its ID names the same step.
	byID := startRun(t, server, path, map[string]any{
		"steps":           []string{"consume"},
		"outputsFromRuns": map[string]string{"img": second},
	})
	waitForStatus(t, server, name, byID, api.StatusSuccess)
	assert.Contains(t, stepLog(t, server, name, byID, "consume"), "tag="+secondTag)
	byIDInputs := *runDetails(t, server, name, byID).ReusedInputs
	require.Len(t, byIDInputs, 1)
	assert.Equal(t, "build", byIDInputs[0].Step)

	// A test run can be the source while it exists, and a run that reused a step
	// of another run records that run.
	ephemeral := startRun(t, server, path, map[string]any{
		"steps":           []string{"consume"},
		"ephemeral":       true,
		"outputsFromRuns": map[string]string{"build": first},
	})
	waitForStatus(t, server, name, ephemeral, api.StatusSuccess)
	chained := startRun(t, server, path, map[string]any{
		"steps":           []string{"consume"},
		"outputsFromRuns": map[string]string{"build": ephemeral},
	})
	waitForStatus(t, server, name, chained, api.StatusSuccess)
	assert.Contains(t, stepLog(t, server, name, chained, "consume"), "tag="+firstTag)
	chainedInputs := *runDetails(t, server, name, chained).ReusedInputs
	assert.Equal(t, ephemeral, string(chainedInputs[0].RunId))
}

// A run of a step that cannot give it is a bad request that names the step and
// the run: the run is gone, belongs to another DAG, or the step failed there.
func TestOutputsFromRunsRejects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixtures assume a POSIX sh is available")
	}
	server := newServer(t)
	const name = "from_runs_rules"
	createDAG(t, server, name, fromRunsDAG)
	const other = "from_runs_other"
	createDAG(t, server, other, fromRunsDAG)
	path := "/api/v1/dags/" + name + "/start"

	good := startRun(t, server, path, map[string]any{})
	waitForStatus(t, server, name, good, api.StatusSuccess)
	failed := startRun(t, server, path, map[string]any{"params": "FAIL=1"})
	waitForStatus(t, server, name, failed, api.StatusFailed)
	foreign := startRun(t, server, "/api/v1/dags/"+other+"/start", map[string]any{})
	waitForStatus(t, server, other, foreign, api.StatusSuccess)

	reject := func(from map[string]string) string {
		return server.Client().Post(path, map[string]any{
			"steps":           []string{"consume"},
			"outputsFromRuns": from,
		}).ExpectStatus(http.StatusBadRequest).Send(t).Body
	}

	assert.Contains(t, reject(map[string]string{"build": "gone-run"}),
		"step build cannot be reused from run gone-run: the run does not exist")
	assert.Contains(t, reject(map[string]string{"build": foreign}),
		fmt.Sprintf("step build cannot be reused from run %s: the run does not exist", foreign))
	assert.Contains(t, reject(map[string]string{"build": failed}),
		fmt.Sprintf("step build cannot be reused from run %s: the step did not succeed there", failed))
	assert.Contains(t, reject(map[string]string{"missing": good}), "unknown step")
	assert.Contains(t, reject(map[string]string{"build": "bad/id"}),
		"dag-run ID must only contain alphanumeric characters, dashes, and underscores")

	// A run that selected build records consume as skipped with nothing to
	// carry, so it cannot give consume.
	partial := startRun(t, server, path, map[string]any{"steps": []string{"build"}})
	waitForStatus(t, server, name, partial, api.StatusSuccess)
	body := server.Client().Post(path, map[string]any{
		"steps":           []string{"build"},
		"outputsFromRuns": map[string]string{"consume": partial},
	}).ExpectStatus(http.StatusBadRequest).Send(t).Body
	assert.Contains(t, body,
		fmt.Sprintf("step consume cannot be reused from run %s: the step did not succeed there", partial))

	// A step that is selected to run cannot be a key.
	body = server.Client().Post(path, map[string]any{
		"steps":           []string{"build"},
		"outputsFromRuns": map[string]string{"build": good},
	}).ExpectStatus(http.StatusBadRequest).Send(t).Body
	assert.Contains(t, body, "which is selected to run")

	// outputsFromRuns needs steps, on both endpoints.
	body = server.Client().Post(path, map[string]any{
		"outputsFromRuns": map[string]string{"build": good},
	}).ExpectStatus(http.StatusBadRequest).Send(t).Body
	assert.Contains(t, body, "outputsFromRuns")
	server.Client().Post("/api/v1/dag-runs", map[string]any{
		"spec":            fromRunsDAG,
		"name":            "from_runs_inline",
		"outputsFromRuns": map[string]string{"build": good},
	}).ExpectStatus(http.StatusBadRequest).Send(t)
}
