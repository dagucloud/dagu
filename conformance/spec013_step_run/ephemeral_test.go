// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec013_step_run_test

import (
	"fmt"
	"net/http"
	"runtime"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ephemeralTTL is how long a test server keeps an ended ephemeral run. The
// product constant is ten minutes; the test server takes a short value
// through a server option that no configuration key reaches.
const ephemeralTTL = 6 * time.Second

const stepRunDAG = `steps:
  - name: build
    id: build
    run: 'printf "image_tag=from-run\n" >> "$DAGU_OUTPUT_FILE"'
    outputs:
      - name: image_tag
  - name: capture
    id: capture
    run: "echo from-run"
    output: CAPTURED
  - name: consume
    id: consume
    depends: [build, capture]
    run: 'echo "tag=${steps.build.outputs.image_tag} captured=$CAPTURED"'
`

const timeout = 30 * time.Second

func newServer(t *testing.T) test.Server {
	t.Helper()
	return test.SetupServer(t, test.WithServerOptions(
		frontend.WithEphemeralRunCleanup(ephemeralTTL, 500*time.Millisecond),
	))
}

func createDAG(t *testing.T, server test.Server, name, spec string) {
	t.Helper()
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: api.DAGName(name),
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)
}

func startRun(t *testing.T, server test.Server, path string, body any) string {
	t.Helper()
	resp := server.Client().Post(path, body).ExpectStatus(http.StatusOK).Send(t)
	var result struct {
		DagRunID string `json:"dagRunId"`
	}
	resp.Unmarshal(t, &result)
	require.NotEmpty(t, result.DagRunID)
	return result.DagRunID
}

func waitForStatus(t *testing.T, server test.Server, name, runID string, want api.Status) {
	t.Helper()
	var last api.Status
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, runID)).Send(t)
		if !assert.Equal(c, http.StatusOK, resp.Response.StatusCode()) {
			return
		}
		var details api.GetDAGRunDetails200JSONResponse
		resp.Unmarshal(t, &details)
		last = details.DagRunDetails.Status
		assert.Equal(c, want, last)
	}, timeout, 200*time.Millisecond, "run %s/%s never reached %v (last %v)", name, runID, want, last)
}

func stepLog(t *testing.T, server test.Server, name, runID, step string) string {
	t.Helper()
	resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/%s/log", name, runID, step)).
		ExpectStatus(http.StatusOK).Send(t)
	var log api.Log
	resp.Unmarshal(t, &log)
	return log.Content
}

func stepsOnly(outputs map[string]map[string]string, extra func(map[string]any)) map[string]any {
	body := map[string]any{"steps": []string{"consume"}}
	if outputs != nil {
		body["outputs"] = outputs
		body["outputsSources"] = explicitSources
	}
	if extra != nil {
		extra(body)
	}
	return body
}

var explicitOutputs = map[string]map[string]string{
	"build":   {"image_tag": "v9.9.9"},
	"capture": {"CAPTURED": "from-the-front"},
}

// explicitSources names the run each step of explicitOutputs was captured
// from. The runs do not exist: a source run can be gone.
var explicitSources = map[string]map[string]string{
	"build":   {"runId": "earlier-run-1", "capturedAt": "2026-10-25T08:00:00Z"},
	"capture": {"runId": "earlier-run-2"},
}

// An ephemeral step run executes like any run and is read by its ID, but it
// reaches no history, no DAG list entry and no retry; it disappears after the
// TTL.
func TestEphemeralStepRunLeavesNoTrace(t *testing.T) {
	server := newServer(t)
	const name = "ephemeral_trace"
	createDAG(t, server, name, stepRunDAG)

	normalID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{})
	waitForStatus(t, server, name, normalID, api.StatusSuccess)

	ephemeralID := startRun(t, server, "/api/v1/dags/"+name+"/start", stepsOnly(explicitOutputs, func(body map[string]any) {
		body["ephemeral"] = true
	}))
	waitForStatus(t, server, name, ephemeralID, api.StatusSuccess)

	// The run reads like any run.
	assert.Contains(t, stepLog(t, server, name, ephemeralID, "consume"), "tag=v9.9.9 captured=from-the-front")
	server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s/spec", name, ephemeralID)).
		ExpectStatus(http.StatusOK).Send(t)

	// The run is in no list.
	var runs api.DAGRunsPageResponse
	server.Client().Get("/api/v1/dag-runs?name="+name).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &runs)
	require.Len(t, runs.DagRuns, 1)
	assert.Equal(t, normalID, string(runs.DagRuns[0].DagRunId))

	var history api.GetDAGDAGRunHistory200JSONResponse
	server.Client().Get("/api/v1/dags/"+name+"/dag-runs").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &history)
	require.Len(t, history.DagRuns, 1)
	assert.Equal(t, normalID, string(history.DagRuns[0].DagRunId))

	var dags api.ListDAGs200JSONResponse
	server.Client().Get("/api/v1/dags?name="+name).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &dags)
	require.Len(t, dags.Dags, 1)
	assert.Equal(t, normalID, string(dags.Dags[0].LatestDAGRun.DagRunId))

	// The run cannot enter the history again.
	server.Client().Post(fmt.Sprintf("/api/v1/dag-runs/%s/%s/retry", name, ephemeralID), map[string]any{
		"dagRunId": ephemeralID,
	}).ExpectStatus(http.StatusConflict).Send(t)
	server.Client().Post(fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", name, ephemeralID), map[string]any{}).
		ExpectStatus(http.StatusConflict).Send(t)

	// The run goes away once it has ended for the TTL; the normal run stays.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, ephemeralID)).Send(t)
		assert.Equal(c, http.StatusNotFound, resp.Response.StatusCode())
	}, timeout+ephemeralTTL, 500*time.Millisecond, "ephemeral run %s was not removed", ephemeralID)
	server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, normalID)).
		ExpectStatus(http.StatusOK).Send(t)
}

// Outputs supplied in the request feed the selected step, with no source run,
// for a saved DAG and for a spec text, whether the run is ephemeral or not.
func TestOutputsFeedSelectedStep(t *testing.T) {
	server := newServer(t)
	const name = "step_run_outputs"
	createDAG(t, server, name, stepRunDAG)

	for _, ephemeral := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved/ephemeral=%v", ephemeral), func(t *testing.T) {
			runID := startRun(t, server, "/api/v1/dags/"+name+"/start", stepsOnly(explicitOutputs, func(body map[string]any) {
				body["ephemeral"] = ephemeral
			}))
			waitForStatus(t, server, name, runID, api.StatusSuccess)
			assert.Contains(t, stepLog(t, server, name, runID, "consume"), "tag=v9.9.9 captured=from-the-front")
		})
		t.Run(fmt.Sprintf("spec/ephemeral=%v", ephemeral), func(t *testing.T) {
			specName := fmt.Sprintf("inline_outputs_%v", ephemeral)
			runID := startRun(t, server, "/api/v1/dag-runs", stepsOnly(explicitOutputs, func(body map[string]any) {
				body["spec"] = stepRunDAG
				body["name"] = specName
				body["ephemeral"] = ephemeral
			}))
			waitForStatus(t, server, specName, runID, api.StatusSuccess)
			assert.Contains(t, stepLog(t, server, specName, runID, "consume"), "tag=v9.9.9 captured=from-the-front")
		})
	}
}

// A value that nobody supplied is not an error of the request. The reference
// stays literal text (Spec 007), so the selected step runs with that text and
// fails when its shell rejects it.
func TestUnsuppliedOutputLeavesReferenceLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on sh rejecting the unresolved reference")
	}
	server := newServer(t)
	const name = "step_run_missing_output"
	createDAG(t, server, name, `steps:
  - name: build
    id: build
    run: "true"
  - name: consume
    id: consume
    depends: [build]
    run: 'echo "tag=${steps.build.outputs.image_tag}"'
`)

	runID := startRun(t, server, "/api/v1/dags/"+name+"/start", stepsOnly(nil, nil))
	waitForStatus(t, server, name, runID, api.StatusFailed)
}

// A request for the whole DAG cannot be ephemeral.
func TestEphemeralNeedsSteps(t *testing.T) {
	server := newServer(t)
	const name = "ephemeral_needs_steps"
	createDAG(t, server, name, stepRunDAG)

	for _, tc := range []struct {
		path string
		body map[string]any
	}{
		{"/api/v1/dags/" + name + "/start", map[string]any{"ephemeral": true}},
		{"/api/v1/dag-runs", map[string]any{"spec": stepRunDAG, "name": "inline_whole", "ephemeral": true}},
	} {
		resp := server.Client().Post(tc.path, tc.body).ExpectStatus(http.StatusBadRequest).Send(t)
		assert.Contains(t, resp.Body, "ephemeral requires steps")
	}

	// An explicit false is the default and runs the whole DAG.
	runID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{"ephemeral": false})
	waitForStatus(t, server, name, runID, api.StatusSuccess)
}

// outputsSources is optional: a step in outputs without an entry runs with its
// values and has no reused input. A source must be for a step in outputs. A
// source run that does not exist is accepted.
func TestOutputsSources(t *testing.T) {
	server := newServer(t)
	const name = "outputs_sources_rules"
	createDAG(t, server, name, stepRunDAG)
	path := "/api/v1/dags/" + name + "/start"

	post := func(body map[string]any) string {
		return server.Client().Post(path, body).ExpectStatus(http.StatusBadRequest).Send(t).Body
	}

	noSources := startRun(t, server, path, map[string]any{
		"steps":   []string{"consume"},
		"outputs": explicitOutputs,
	})
	waitForStatus(t, server, name, noSources, api.StatusSuccess)
	assert.Contains(t, stepLog(t, server, name, noSources, "consume"), "tag=v9.9.9 captured=from-the-front")
	assert.Nil(t, runDetails(t, server, name, noSources).ReusedInputs)

	// Only the step that names its run has an entry.
	oneSource := startRun(t, server, path, map[string]any{
		"steps":          []string{"consume"},
		"outputs":        explicitOutputs,
		"outputsSources": map[string]any{"build": explicitSources["build"]},
	})
	waitForStatus(t, server, name, oneSource, api.StatusSuccess)
	inputs := *runDetails(t, server, name, oneSource).ReusedInputs
	require.Len(t, inputs, 1)
	assert.Equal(t, "build", inputs[0].Step)
	assert.Equal(t, api.ReusedInputViaCache, inputs[0].Via)

	body := post(map[string]any{
		"steps":          []string{"consume"},
		"outputs":        map[string]map[string]string{"build": {"image_tag": "v1"}},
		"outputsSources": explicitSources,
	})
	assert.Contains(t, body, "outputsSources names step capture, which is not in outputs")

	body = post(map[string]any{
		"steps":          []string{"consume"},
		"outputsSources": explicitSources,
	})
	assert.Contains(t, body, "outputsSources names step")

	// The inline-spec endpoint applies the same rule.
	resp := server.Client().Post("/api/v1/dag-runs", map[string]any{
		"spec":           stepRunDAG,
		"name":           "inline_source_only",
		"steps":          []string{"consume"},
		"outputsSources": explicitSources,
	}).ExpectStatus(http.StatusBadRequest).Send(t)
	assert.Contains(t, resp.Body, "outputsSources names step")

	// An invalid run ID or time is a 400 too.
	post(map[string]any{
		"steps":   []string{"consume"},
		"outputs": map[string]map[string]string{"build": {"image_tag": "v1"}},
		"outputsSources": map[string]any{
			"build": map[string]string{"runId": "not/a/run id"},
		},
	})
	post(map[string]any{
		"steps":   []string{"consume"},
		"outputs": map[string]map[string]string{"build": {"image_tag": "v1"}},
		"outputsSources": map[string]any{
			"build": map[string]string{"runId": "run-1", "capturedAt": "yesterday"},
		},
	})

	// Sources for every step of outputs make a valid request.
	runID := startRun(t, server, path, stepsOnly(explicitOutputs, nil))
	waitForStatus(t, server, name, runID, api.StatusSuccess)
}

func runDetails(t *testing.T, server test.Server, name, runID string) api.DAGRunDetails {
	t.Helper()
	var details api.GetDAGRunDetails200JSONResponse
	server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, runID)).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	return details.DagRunDetails
}

// A run seeded from outputsFromRunId records the source run for each step
// that carried outputs.
func TestReusedInputsFromRun(t *testing.T) {
	server := newServer(t)
	const name = "reused_from_run"
	createDAG(t, server, name, stepRunDAG)

	sourceID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{})
	waitForStatus(t, server, name, sourceID, api.StatusSuccess)

	runID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{
		"steps":            []string{"consume"},
		"outputsFromRunId": sourceID,
	})
	waitForStatus(t, server, name, runID, api.StatusSuccess)

	details := runDetails(t, server, name, runID)
	require.NotNil(t, details.ReusedInputs)
	inputs := *details.ReusedInputs
	require.Len(t, inputs, 2)
	for i, step := range []string{"build", "capture"} {
		assert.Equal(t, step, inputs[i].Step)
		assert.Equal(t, sourceID, string(inputs[i].RunId))
		assert.Equal(t, api.ReusedInputViaRun, inputs[i].Via)
		require.NotNil(t, inputs[i].CapturedAt, step)
		_, err := time.Parse(time.RFC3339, *inputs[i].CapturedAt)
		assert.NoError(t, err, step)
	}
	assert.Nil(t, details.Ephemeral)

	// A normal run has no record.
	assert.Nil(t, runDetails(t, server, name, sourceID).ReusedInputs)
}

// Only a skipped step that a selected step depends on carries outputs: the
// steps that nothing selected reads have no entry, whatever the source run has.
func TestReusedInputsOnlyUpstream(t *testing.T) {
	server := newServer(t)
	const name = "reused_only_upstream"
	createDAG(t, server, name, stepRunDAG)

	sourceID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{})
	waitForStatus(t, server, name, sourceID, api.StatusSuccess)

	// build depends on nothing, so capture and consume carry nothing.
	runID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{
		"steps":            []string{"build"},
		"outputsFromRunId": sourceID,
	})
	waitForStatus(t, server, name, runID, api.StatusSuccess)
	details := runDetails(t, server, name, runID)
	assert.Nil(t, details.ReusedInputs)
	for _, node := range details.Nodes {
		if node.Step.Name == "capture" || node.Step.Name == "consume" {
			assert.Equal(t, api.NodeStatusSkipped, node.Status, node.Step.Name)
			assert.Empty(t, node.Stdout, node.Step.Name)
		}
	}
}

// A run seeded from supplied outputs records the run each step names, and a
// supplied step wins over outputsFromRunId.
func TestReusedInputsFromCache(t *testing.T) {
	server := newServer(t)
	const name = "reused_from_cache"
	createDAG(t, server, name, stepRunDAG)

	sourceID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{})
	waitForStatus(t, server, name, sourceID, api.StatusSuccess)

	runID := startRun(t, server, "/api/v1/dags/"+name+"/start", stepsOnly(explicitOutputs, nil))
	waitForStatus(t, server, name, runID, api.StatusSuccess)
	inputs := *runDetails(t, server, name, runID).ReusedInputs
	require.Len(t, inputs, 2)
	assert.Equal(t, "build", inputs[0].Step)
	assert.Equal(t, "earlier-run-1", string(inputs[0].RunId))
	assert.Equal(t, api.ReusedInputViaCache, inputs[0].Via)
	require.NotNil(t, inputs[0].CapturedAt)
	assert.Equal(t, "2026-10-25T08:00:00Z", *inputs[0].CapturedAt)
	assert.Equal(t, "capture", inputs[1].Step)
	assert.Equal(t, "earlier-run-2", string(inputs[1].RunId))
	assert.Nil(t, inputs[1].CapturedAt)

	// Only build is supplied; capture still comes from the source run.
	mixedID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{
		"steps":            []string{"consume"},
		"outputsFromRunId": sourceID,
		"outputs":          map[string]map[string]string{"build": {"image_tag": "v1.2.3"}},
		"outputsSources":   map[string]any{"build": explicitSources["build"]},
	})
	waitForStatus(t, server, name, mixedID, api.StatusSuccess)
	inputs = *runDetails(t, server, name, mixedID).ReusedInputs
	require.Len(t, inputs, 2)
	assert.Equal(t, api.ReusedInputViaCache, inputs[0].Via)
	assert.Equal(t, "earlier-run-1", string(inputs[0].RunId))
	assert.Equal(t, api.ReusedInputViaRun, inputs[1].Via)
	assert.Equal(t, sourceID, string(inputs[1].RunId))
}

// An ephemeral run says so in its details, appears in the DAG history only
// when the request opts in, and is still removed after the TTL.
func TestEphemeralRunInHistory(t *testing.T) {
	server := newServer(t)
	const name = "ephemeral_history"
	createDAG(t, server, name, stepRunDAG)

	normalID := startRun(t, server, "/api/v1/dags/"+name+"/start", map[string]any{})
	waitForStatus(t, server, name, normalID, api.StatusSuccess)
	ephemeralID := startRun(t, server, "/api/v1/dags/"+name+"/start", stepsOnly(explicitOutputs, func(body map[string]any) {
		body["ephemeral"] = true
	}))
	waitForStatus(t, server, name, ephemeralID, api.StatusSuccess)

	details := runDetails(t, server, name, ephemeralID)
	require.NotNil(t, details.Ephemeral)
	assert.True(t, *details.Ephemeral)
	require.NotNil(t, details.ReusedInputs)

	history := func(query string) api.GetDAGDAGRunHistory200JSONResponse {
		var response api.GetDAGDAGRunHistory200JSONResponse
		server.Client().Get("/api/v1/dags/"+name+"/dag-runs"+query).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &response)
		return response
	}

	// Without the option, and with it set to false, only the normal run shows.
	for _, query := range []string{"", "?includeEphemeral=false"} {
		got := history(query)
		require.Len(t, got.DagRuns, 1, query)
		assert.Equal(t, normalID, string(got.DagRuns[0].DagRunId), query)
		assert.Nil(t, got.DagRuns[0].Ephemeral, query)
	}

	// With the option, the ephemeral run comes first: it started last.
	got := history("?includeEphemeral=true")
	require.Len(t, got.DagRuns, 2)
	assert.Equal(t, ephemeralID, string(got.DagRuns[0].DagRunId))
	require.NotNil(t, got.DagRuns[0].Ephemeral)
	assert.True(t, *got.DagRuns[0].Ephemeral)
	assert.Equal(t, normalID, string(got.DagRuns[1].DagRunId))
	assert.Nil(t, got.DagRuns[1].Ephemeral)

	// The other lists never show the run, whatever the query says.
	var runs api.DAGRunsPageResponse
	server.Client().Get("/api/v1/dag-runs?name="+name+"&includeEphemeral=true").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &runs)
	require.Len(t, runs.DagRuns, 1)
	assert.Equal(t, normalID, string(runs.DagRuns[0].DagRunId))

	// The TTL removes the run; the history is back to the normal run.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, ephemeralID)).Send(t)
		assert.Equal(c, http.StatusNotFound, resp.Response.StatusCode())
	}, timeout+ephemeralTTL, 500*time.Millisecond, "ephemeral run %s was not removed", ephemeralID)
	got = history("?includeEphemeral=true")
	require.Len(t, got.DagRuns, 1)
	assert.Equal(t, normalID, string(got.DagRuns[0].DagRunId))
}
