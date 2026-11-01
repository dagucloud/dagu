// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStepOutputsAPI(t *testing.T, nodes ...*ir.Node) *API {
	t.Helper()
	repository := testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{})
	ctx := t.Context()
	dag := &ir.DAG{Name: "demo"}
	attempt, err := repository.CreateAttempt(ctx, dag, time.Now().UTC(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := ir.InitialStatus(dag)
	status.DAGRunID = "run-1"
	status.AttemptID = attempt.ID()
	status.Status = ir.Succeeded
	status.Nodes = nodes
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
	cfg := &config.Config{}
	return &API{
		dagRunRepository: repository,
		dagRunMgr:        runtimepkg.NewManager(repository, nil, cfg),
		config:           cfg,
	}
}

func stepOutputsNode(name, id string, outputs map[string]any) *ir.Node {
	raw, _ := json.Marshal(outputs)
	s := string(raw)
	return &ir.Node{Step: ir.Step{Name: name, ID: id}, Status: ir.NodeSucceeded, StepOutputsValue: &s}
}

func getStepOutputs(t *testing.T, a *API, runID, step string) openapiv1.GetDAGRunStepOutputsResponseObject {
	t.Helper()
	resp, err := a.GetDAGRunStepOutputs(t.Context(), openapiv1.GetDAGRunStepOutputsRequestObject{
		Name: "demo", DagRunId: runID, StepName: step,
	})
	require.NoError(t, err)
	return resp
}

func requireStepOutputs(t *testing.T, resp openapiv1.GetDAGRunStepOutputsResponseObject) openapiv1.StepOutputs {
	t.Helper()
	ok, is := resp.(openapiv1.GetDAGRunStepOutputs200JSONResponse)
	require.True(t, is, "got %T", resp)
	return openapiv1.StepOutputs(ok)
}

func TestStepOutputsByName(t *testing.T) {
	node := stepOutputsNode("build", "b1", map[string]any{"zeta": "z", "alpha": 3})
	vars := &collections.SyncMap{}
	vars.Store("RESULT", "RESULT=hello")
	node.Step.Output = "RESULT"
	node.OutputVariables = vars
	a := newStepOutputsAPI(t, node)

	got := requireStepOutputs(t, getStepOutputs(t, a, "latest", "build"))
	assert.Equal(t, "build", got.StepName)
	require.NotNil(t, got.StepId)
	assert.Equal(t, "b1", *got.StepId)
	assert.Equal(t, openapiv1.NodeStatus(ir.NodeSucceeded), got.Status)
	assert.Equal(t, openapiv1.NodeStatusLabel(ir.NodeSucceeded.String()), got.StatusLabel)
	require.Len(t, got.Outputs, 2)
	assert.Equal(t, "alpha", got.Outputs[0].Name)
	assert.Equal(t, "3", got.Outputs[0].Value)
	assert.Equal(t, "zeta", got.Outputs[1].Name)
	require.NotNil(t, got.OutputVariable)
	assert.Equal(t, "RESULT", got.OutputVariable.Name)
	assert.Equal(t, "hello", got.OutputVariable.Value)
}

// An executor that publishes outputs without declaring them fills only the
// channel that ${steps.<id>.outputs.<name>} falls back to. Its outputs show,
// unless the step has declared outputs, which win as they do in a reference.
func TestStepOutputsFromPublishedPayload(t *testing.T) {
	payload := `{"rows":3,"name":"x"}`
	published := &ir.Node{Step: ir.Step{Name: "load"}, Status: ir.NodeSucceeded, OutputsValue: &payload}
	declared := stepOutputsNode("both", "", map[string]any{"a": "1"})
	declared.OutputsValue = &payload
	a := newStepOutputsAPI(t, published, declared)

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "load"))
	assert.Equal(t, []openapiv1.StepOutputValue{
		{Name: "name", Value: "x", Size: 1},
		{Name: "rows", Value: "3", Size: 1},
	}, got.Outputs)

	got = requireStepOutputs(t, getStepOutputs(t, a, "run-1", "both"))
	assert.Equal(t, []openapiv1.StepOutputValue{{Name: "a", Value: "1", Size: 1}}, got.Outputs)
}

// The output variable is the value that ${NAME} gives, the captured stdout,
// also when output_schema keeps a normalized copy as the output value.
func TestStepOutputsVariableIsCaptured(t *testing.T) {
	normalized := `{"a":1}`
	vars := &collections.SyncMap{}
	vars.Store("RESULT", `RESULT={ "a": 1 }`)
	node := &ir.Node{
		Step:            ir.Step{Name: "build", Output: "RESULT"},
		Status:          ir.NodeSucceeded,
		OutputValue:     &normalized,
		OutputVariables: vars,
	}
	a := newStepOutputsAPI(t, node)

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "build"))
	require.NotNil(t, got.OutputVariable)
	assert.Equal(t, "RESULT", got.OutputVariable.Name)
	assert.Equal(t, `{ "a": 1 }`, got.OutputVariable.Value)
}

func TestStepOutputsByID(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("build", "b1", map[string]any{"k": "v"}))

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "b1"))
	assert.Equal(t, "build", got.StepName)
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "v", got.Outputs[0].Value)
}

func TestStepOutputsEmpty(t *testing.T) {
	a := newStepOutputsAPI(t, &ir.Node{Step: ir.Step{Name: "quiet"}, Status: ir.NodeNotStarted})

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "quiet"))
	assert.NotNil(t, got.Outputs)
	assert.Empty(t, got.Outputs)
	assert.Nil(t, got.OutputVariable)
	assert.Nil(t, got.StepId)
}

func TestStepOutputsUnknown(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("build", "", nil))

	resp := getStepOutputs(t, a, "run-1", "nope")
	nf, ok := resp.(openapiv1.GetDAGRunStepOutputs404JSONResponse)
	require.True(t, ok, "got %T", resp)
	assert.Equal(t, openapiv1.ErrorCodeNotFound, nf.Code)

	resp = getStepOutputs(t, a, "missing-run", "build")
	nf, ok = resp.(openapiv1.GetDAGRunStepOutputs404JSONResponse)
	require.True(t, ok, "got %T", resp)
	assert.Equal(t, openapiv1.ErrorCodeNotFound, nf.Code)
}

func TestStepOutputsTruncated(t *testing.T) {
	// "é" has two bytes, so an odd cut point falls inside a rune.
	big := "a" + strings.Repeat("é", maxStepOutputBytes)
	small := strings.Repeat("x", 10)
	a := newStepOutputsAPI(t, stepOutputsNode("build", "", map[string]any{"big": big, "small": small}))

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "build"))
	require.Len(t, got.Outputs, 2)
	bigOut, smallOut := got.Outputs[0], got.Outputs[1]
	assert.True(t, bigOut.Truncated)
	assert.Equal(t, len(big), bigOut.Size)
	assert.True(t, utf8.ValidString(bigOut.Value))
	assert.LessOrEqual(t, len(bigOut.Value), maxStepOutputBytes)
	assert.Equal(t, maxStepOutputBytes-1, len(bigOut.Value))
	assert.False(t, smallOut.Truncated)
	assert.Equal(t, small, smallOut.Value)
	assert.Equal(t, 10, smallOut.Size)
}

func TestStepOutputsMasked(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("build", "", map[string]any{"token": "*******"}))

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "build"))
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "*******", got.Outputs[0].Value)
}

// updateStepOutputsStatus rewrites the latest persisted status of the demo DAG.
func updateStepOutputsStatus(t *testing.T, a *API, mutate func(*ir.DAGRunStatus)) {
	t.Helper()
	ctx := t.Context()
	attempt, err := a.dagRunRepository.LatestAttempt(ctx, "demo", persis.DAGRunLatestAttemptOptions{})
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	mutate(status)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, *status))
	require.NoError(t, attempt.Close(ctx))
}

func TestStepOutputsPrefersTopLevel(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("same", "", map[string]any{"from": "top"}))
	// A handler with the same name must not win over the top-level step.
	updateStepOutputsStatus(t, a, func(s *ir.DAGRunStatus) {
		s.OnExit = stepOutputsNode("same", "", map[string]any{"from": "handler"})
	})

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "same"))
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "top", got.Outputs[0].Value)
}

// A top-level step id wins over a handler step name.
func TestStepOutputsTopLevelIDBeforeHandlerName(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("build", "onExit", map[string]any{"from": "top"}))
	updateStepOutputsStatus(t, a, func(s *ir.DAGRunStatus) {
		s.OnExit = stepOutputsNode("onExit", "", map[string]any{"from": "handler"})
	})

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "onExit"))
	assert.Equal(t, "build", got.StepName)
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "top", got.Outputs[0].Value)
}

func TestStepOutputsHandlerKeys(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("build", "", nil))
	updateStepOutputsStatus(t, a, func(s *ir.DAGRunStatus) {
		s.OnInit = stepOutputsNode("onInit", "", map[string]any{"from": "init"})
		s.OnSuccess = stepOutputsNode("onSuccess", "", map[string]any{"from": "success"})
		s.OnFailure = stepOutputsNode("onFailure", "", map[string]any{"from": "failure"})
		s.OnAbort = stepOutputsNode("onAbort", "", map[string]any{"from": "abort"})
		s.OnExit = stepOutputsNode("onExit", "exit-id", map[string]any{"from": "exit"})
		s.OnWait = stepOutputsNode("onWait", "", map[string]any{"from": "wait"})
	})

	tests := []struct{ ref, want string }{
		{"handler_on.init", "init"},
		{"handler_on.success", "success"},
		{"handler_on.failure", "failure"},
		{"handler_on.abort", "abort"},
		{"handler_on.exit", "exit"},
		{"handler_on.wait", "wait"},
		{"onFailure", "failure"},
		{"exit-id", "exit"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", tt.ref))
			require.Len(t, got.Outputs, 1)
			assert.Equal(t, tt.want, got.Outputs[0].Value)
		})
	}

	resp := getStepOutputs(t, a, "run-1", "handler_on.other")
	_, ok := resp.(openapiv1.GetDAGRunStepOutputs404JSONResponse)
	assert.True(t, ok, "got %T", resp)
}

// A handler key of an event without a handler step is not found.
func TestStepOutputsHandlerKeyAbsent(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("build", "", nil))

	resp := getStepOutputs(t, a, "run-1", "handler_on.failure")
	_, ok := resp.(openapiv1.GetDAGRunStepOutputs404JSONResponse)
	assert.True(t, ok, "got %T", resp)
}

// addStepOutputsRun persists one more run of the demo DAG that starts at the given time.
func addStepOutputsRun(t *testing.T, a *API, runID string, at time.Time, nodes ...*ir.Node) {
	t.Helper()
	ctx := t.Context()
	dag := &ir.DAG{Name: "demo"}
	attempt, err := a.dagRunRepository.CreateAttempt(ctx, dag, at, runID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := ir.InitialStatus(dag)
	status.DAGRunID = runID
	status.AttemptID = attempt.ID()
	status.Status = ir.Succeeded
	status.Nodes = nodes
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func TestStepOutputsFailedNode(t *testing.T) {
	node := stepOutputsNode("build", "", map[string]any{"partial": "yes"})
	node.Status = ir.NodeFailed
	a := newStepOutputsAPI(t, node)

	got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "build"))
	assert.Equal(t, openapiv1.NodeStatus(ir.NodeFailed), got.Status)
	assert.Equal(t, openapiv1.NodeStatusLabel("failed"), got.StatusLabel)
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "yes", got.Outputs[0].Value)
}

func TestStepOutputsLatestRun(t *testing.T) {
	a := newStepOutputsAPI(t, stepOutputsNode("build", "", map[string]any{"run": "first"}))
	addStepOutputsRun(t, a, "run-2", time.Now().UTC().Add(time.Hour),
		stepOutputsNode("build", "", map[string]any{"run": "second"}))

	got := requireStepOutputs(t, getStepOutputs(t, a, "latest", "build"))
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "second", got.Outputs[0].Value)

	got = requireStepOutputs(t, getStepOutputs(t, a, "run-1", "build"))
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "first", got.Outputs[0].Value)
}

// Foreach and sub-DAG steps return the values of the parent node only. The
// children name sub-run ids; the handler never reads a run for them.
func TestStepOutputsParentOnly(t *testing.T) {
	children := []ir.SubDAGRun{
		{DAGRunID: "sub-1", DAGName: "child-dag", ParallelItem: "a"},
		{DAGRunID: "sub-2", DAGName: "child-dag", ParallelItem: "b"},
	}
	foreach := stepOutputsNode("each", "", map[string]any{"parent": "p"})
	foreach.Step.Foreach = &ir.ForeachConfig{}
	foreach.SubRuns = children
	foreach.SubRunsRepeated = children[:1]
	subDAG := stepOutputsNode("call", "", map[string]any{"parent": "p"})
	subDAG.Step.SubDAG = &ir.SubDAG{Name: "child-dag"}
	subDAG.SubRuns = children[:1]

	a := newStepOutputsAPI(t, foreach, subDAG)
	// A run with the id of a child, holding steps of the same names, stays unused.
	addStepOutputsRun(t, a, "sub-1", time.Now().UTC().Add(-time.Hour),
		stepOutputsNode("each", "", map[string]any{"child": "c"}),
		stepOutputsNode("call", "", map[string]any{"child": "c"}))

	for _, name := range []string{"each", "call"} {
		t.Run(name, func(t *testing.T) {
			got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", name))
			assert.Equal(t, name, got.StepName)
			require.Len(t, got.Outputs, 1)
			assert.Equal(t, "parent", got.Outputs[0].Name)
			assert.Equal(t, "p", got.Outputs[0].Value)
		})
	}
}

// The runtime masks node outputs with a masker built from the secret
// environment before it persists them (internal/runtime/agent/status_masking.go).
// The test applies the same masker to the same node fields, persists the result,
// and checks that the endpoint shows the masked text only.
func TestStepOutputsMaskedBeforePersistence(t *testing.T) {
	const secret = "s3cr3t-token-value"
	masker := masking.NewMasker(masking.SourcedEnvVars{Secrets: []string{"API_TOKEN=" + secret}})

	node := stepOutputsNode("build", "", map[string]any{"url": "https://host/?token=" + secret})
	node.Step.Output = "RESULT"
	vars := &collections.SyncMap{}
	vars.Store("RESULT", masker.MaskString("RESULT=value-"+secret))
	node.OutputVariables = vars
	value := masker.MaskString("value-" + secret)
	node.OutputValue = &value
	outputs := masker.MaskString(*node.StepOutputsValue)
	node.StepOutputsValue = &outputs

	a := newStepOutputsAPI(t, node)
	got := requireStepOutputs(t, getStepOutputs(t, a, "latest", "build"))

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secret)
	require.Len(t, got.Outputs, 1)
	assert.Equal(t, "https://host/?token="+masking.DefaultMaskString, got.Outputs[0].Value)
	require.NotNil(t, got.OutputVariable)
	assert.Equal(t, "value-"+masking.DefaultMaskString, got.OutputVariable.Value)
}

func TestStepOutputsSizeLimit(t *testing.T) {
	limit := maxStepOutputBytes
	tests := []struct {
		name      string
		value     string
		wantLen   int
		truncated bool
	}{
		{"exact", strings.Repeat("a", limit), limit, false},
		{"over", strings.Repeat("a", limit+1), limit, true},
		// "€" has three bytes; the limit falls after the first byte of a rune.
		{"rune3", strings.Repeat("a", limit-1) + "€", limit - 1, true},
		// "😀" has four bytes; the limit falls after the third byte of a rune.
		{"rune4", strings.Repeat("a", limit-3) + "😀", limit - 3, true},
		// A rune that ends exactly at the limit stays whole.
		{"rune-fits", strings.Repeat("a", limit-3) + "€" + "b", limit, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := stepOutputsNode("build", "", map[string]any{"v": tt.value})
			node.Step.Output = "RESULT"
			node.OutputValue = &tt.value
			a := newStepOutputsAPI(t, node)

			got := requireStepOutputs(t, getStepOutputs(t, a, "run-1", "build"))
			require.Len(t, got.Outputs, 1)
			require.NotNil(t, got.OutputVariable)
			for _, v := range []openapiv1.StepOutputValue{got.Outputs[0], *got.OutputVariable} {
				assert.Equal(t, len(tt.value), v.Size)
				assert.Equal(t, tt.truncated, v.Truncated)
				assert.Len(t, v.Value, tt.wantLen)
				assert.True(t, utf8.ValidString(v.Value))
				assert.True(t, strings.HasPrefix(tt.value, v.Value))
			}
		})
	}
}
