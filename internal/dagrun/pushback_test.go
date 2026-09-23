// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pushBackStatus() *ir.DAGRunStatus {
	return &ir.DAGRunStatus{
		Name: "review-loop", DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Waiting,
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "setup"}, Status: ir.NodeSucceeded, Stdout: "/logs/setup.out"},
			{Step: ir.Step{Name: "implement", Depends: []string{"setup"}}, Status: ir.NodeSucceeded, Stdout: "/logs/implement.out"},
			{Step: ir.Step{Name: "test", Depends: []string{"implement"}}, Status: ir.NodeSucceeded, Stdout: "/logs/test.out"},
			{
				Step: ir.Step{
					ID: "review", Name: "review", Depends: []string{"test"},
					HumanTask: &ir.HumanTaskConfig{Prompt: "Review"},
				},
				Status: ir.NodeWaiting,
			},
			{Step: ir.Step{Name: "publish", Depends: []string{"review"}}, Status: ir.NodeNotStarted},
			{Step: ir.Step{Name: "notify", Depends: []string{"setup"}}, Status: ir.NodeSucceeded, Stdout: "/logs/notify.out"},
		},
	}
}

func TestApplyPushBack(t *testing.T) {
	t.Parallel()

	status := pushBackStatus()
	iteration, err := dagrun.ApplyPushBack(status, status.Nodes[3], dagrun.PushBack{
		TargetName: "implement",
		Inputs:     map[string]string{"feedback": "add tests"},
		By:         "reviewer",
		ByID:       "user-1",
		At:         "2026-09-23T10:00:00Z",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, iteration)

	for _, idx := range []int{0, 5} {
		assert.Equal(t, ir.NodeSucceeded, status.Nodes[idx].Status, status.Nodes[idx].Step.Name)
		assert.Zero(t, status.Nodes[idx].ApprovalIteration, status.Nodes[idx].Step.Name)
	}
	wantHistory := []ir.PushBackEntry{{
		Iteration: 1, By: "reviewer", ByID: "user-1", At: "2026-09-23T10:00:00Z",
		Inputs: map[string]string{"feedback": "add tests"}, Step: "review", HumanTask: true,
	}}
	for _, idx := range []int{1, 2, 3, 4} {
		node := status.Nodes[idx]
		assert.Equal(t, ir.NodeNotStarted, node.Status, node.Step.Name)
		assert.Equal(t, 1, node.ApprovalIteration, node.Step.Name)
		assert.Equal(t, map[string]string{"feedback": "add tests"}, node.PushBackInputs, node.Step.Name)
		assert.Equal(t, wantHistory, node.PushBackHistory, node.Step.Name)
	}
	assert.Equal(t, "/logs/implement.out", status.Nodes[1].PushBackPreviousStdout)
	assert.Equal(t, "/logs/test.out", status.Nodes[2].PushBackPreviousStdout)
	assert.Empty(t, status.Nodes[3].PushBackPreviousStdout)
	assert.Equal(t, "Review", status.Nodes[3].Step.HumanTask.Prompt)
}

// A later push-back builds on the source step's history and drops inputs the
// allowlist does not declare, including those recorded earlier.
func TestApplyPushBackExtendsSourceHistory(t *testing.T) {
	t.Parallel()

	status := pushBackStatus()
	source := status.Nodes[3]
	source.ApprovalIteration = 1
	source.PushBackHistory = []ir.PushBackEntry{{
		Iteration: 1, By: "first",
		Inputs: map[string]string{"feedback": "add tests", "PATH": "/tmp"},
	}}

	iteration, err := dagrun.ApplyPushBack(status, source, dagrun.PushBack{
		TargetName:    "implement",
		AllowedInputs: []string{"feedback"},
		Inputs:        map[string]string{"feedback": "rename it", "PATH": "/tmp"},
		By:            "second",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, iteration)

	implement := status.Nodes[1]
	assert.Equal(t, 2, implement.ApprovalIteration)
	assert.Equal(t, map[string]string{"feedback": "rename it"}, implement.PushBackInputs)
	assert.Equal(t, []ir.PushBackEntry{
		{Iteration: 1, By: "first", Inputs: map[string]string{"feedback": "add tests"}},
		{Iteration: 2, By: "second", Inputs: map[string]string{"feedback": "rename it"}, Step: "review", HumanTask: true},
	}, implement.PushBackHistory)
}

// Each entry was scoped by the step that recorded it, so a later push-back
// from another step must keep it intact. Only legacy entries, which do not
// name their step, are filtered with the new source's allowlist.
func TestApplyPushBackKeepsEntriesRecordedByOtherSteps(t *testing.T) {
	t.Parallel()

	status := pushBackStatus()
	source := status.Nodes[3]
	source.ApprovalIteration = 2
	source.PushBackHistory = []ir.PushBackEntry{
		{Iteration: 1, Inputs: map[string]string{"FEEDBACK": "legacy", "PATH": "/tmp"}},
		{Iteration: 2, Inputs: map[string]string{"FEEDBACK": "tighten"}, Step: "approve"},
	}

	_, err := dagrun.ApplyPushBack(status, source, dagrun.PushBack{
		TargetName:    "implement",
		AllowedInputs: []string{"feedback"},
		Inputs:        map[string]string{"feedback": "add tests"},
	})
	require.NoError(t, err)

	history := status.Nodes[1].PushBackHistory
	require.Len(t, history, 3)
	assert.Nil(t, history[0].Inputs)
	assert.Equal(t, map[string]string{"FEEDBACK": "tighten"}, history[1].Inputs)
	assert.Equal(t, map[string]string{"feedback": "add tests"}, history[2].Inputs)
}

// Human-task feedback holds only declared properties, so a step's approval
// input allowlist never hides it; approval push-back inputs stay filtered.
func TestVisiblePushBackInputs(t *testing.T) {
	t.Parallel()

	allowed := []string{"FEEDBACK"}
	latest := map[string]string{"feedback": "add tests", "FEEDBACK": "tighten"}
	approvalEntry := ir.PushBackEntry{Iteration: 1, Inputs: latest, Step: "draft"}
	humanTaskEntry := ir.PushBackEntry{Iteration: 2, Inputs: latest, Step: "review", HumanTask: true}

	assert.Equal(t, map[string]string{"FEEDBACK": "tighten"},
		dagrun.VisiblePushBackInputs(allowed, latest, []ir.PushBackEntry{approvalEntry}))
	assert.Equal(t, latest,
		dagrun.VisiblePushBackInputs(allowed, latest, []ir.PushBackEntry{approvalEntry, humanTaskEntry}))
	assert.Equal(t, map[string]string{"FEEDBACK": "tighten"},
		dagrun.VisiblePushBackInputs(allowed, latest, nil))

	history := dagrun.NormalizePushBackHistory(allowed, 2, latest, []ir.PushBackEntry{approvalEntry, humanTaskEntry})
	require.Len(t, history, 2)
	assert.Equal(t, map[string]string{"FEEDBACK": "tighten"}, history[0].Inputs)
	assert.Equal(t, latest, history[1].Inputs)
}

// Another push-back may already have moved a reset step to a higher
// iteration. The new iteration must exceed every reset step's iteration so an
// expected-iteration check can never match an earlier review again.
func TestApplyPushBackKeepsIterationsIncreasing(t *testing.T) {
	t.Parallel()

	status := pushBackStatus()
	for _, idx := range []int{2, 3} {
		status.Nodes[idx].ApprovalIteration = 2
	}

	iteration, err := dagrun.ApplyPushBack(status, status.Nodes[3], dagrun.PushBack{TargetName: "implement"})
	require.NoError(t, err)
	assert.Equal(t, 3, iteration)

	source := pushBackStatus()
	source.Nodes[4].ApprovalIteration = 5
	iteration, err = dagrun.ApplyPushBack(source, source.Nodes[3], dagrun.PushBack{TargetName: "implement"})
	require.NoError(t, err)
	assert.Equal(t, 6, iteration)
	for _, idx := range []int{1, 2, 3, 4} {
		assert.Equal(t, 6, source.Nodes[idx].ApprovalIteration, source.Nodes[idx].Step.Name)
	}
}

// Build steps consume producer outputs through declared paths without
// declaring depends. Such consumers depend on the rewind target and must run
// again, or they keep outputs built from the rejected result.
func TestApplyPushBackResetsBuildConsumers(t *testing.T) {
	t.Parallel()

	binary := filepath.Join(t.TempDir(), "bin", "app")
	status := &ir.DAGRunStatus{Status: ir.Waiting, Nodes: []*ir.Node{
		{
			Step:   ir.Step{Name: "compile", Outputs: []ir.StepOutputDeclaration{{Name: "binary", Path: binary}}},
			Status: ir.NodeSucceeded,
		},
		{
			Step:   ir.Step{Name: "package", Inputs: []ir.StepInputDeclaration{{Name: "binary", Path: binary}}},
			Status: ir.NodeSucceeded,
		},
		{
			Step: ir.Step{
				ID: "review", Name: "review", Depends: []string{"compile", "package"},
				HumanTask: &ir.HumanTaskConfig{Prompt: "Review"},
			},
			Status: ir.NodeWaiting,
		},
		{Step: ir.Step{Name: "docs"}, Status: ir.NodeSucceeded},
	}}

	_, err := dagrun.ApplyPushBack(status, status.Nodes[2], dagrun.PushBack{TargetName: "compile"})
	require.NoError(t, err)

	for _, idx := range []int{0, 1, 2} {
		assert.Equal(t, ir.NodeNotStarted, status.Nodes[idx].Status, status.Nodes[idx].Step.Name)
		assert.Equal(t, 1, status.Nodes[idx].ApprovalIteration, status.Nodes[idx].Step.Name)
	}
	assert.Equal(t, ir.NodeSucceeded, status.Nodes[3].Status)
}

func TestApplyPushBackRejectsMissingTarget(t *testing.T) {
	t.Parallel()

	status := pushBackStatus()
	before := mustJSON(t, status)
	_, err := dagrun.ApplyPushBack(status, status.Nodes[3], dagrun.PushBack{TargetName: "missing"})
	require.ErrorContains(t, err, "missing")
	assert.JSONEq(t, before, mustJSON(t, status))
}

// Reverting restores the steps a push-back reset and keeps changes other
// requests made to steps outside it.
func TestRevertPushBack(t *testing.T) {
	t.Parallel()

	original := pushBackStatus()
	applied := cloneStatus(t, original)
	_, err := dagrun.ApplyPushBack(applied, applied.Nodes[3], dagrun.PushBack{TargetName: "implement"})
	require.NoError(t, err)

	latest := cloneStatus(t, applied)
	latest.Nodes[5].Status = ir.NodeFailed
	require.NoError(t, dagrun.RevertPushBack(latest, original, applied))

	for _, idx := range []int{0, 1, 2, 3, 4} {
		assert.Equal(t, original.Nodes[idx], latest.Nodes[idx], original.Nodes[idx].Step.Name)
	}
	assert.Equal(t, ir.NodeFailed, latest.Nodes[5].Status)
}

func TestRevertPushBackRejectsChangedStep(t *testing.T) {
	t.Parallel()

	original := pushBackStatus()
	applied := cloneStatus(t, original)
	_, err := dagrun.ApplyPushBack(applied, applied.Nodes[3], dagrun.PushBack{TargetName: "implement"})
	require.NoError(t, err)

	latest := cloneStatus(t, applied)
	latest.Nodes[1].Status = ir.NodeRunning
	before := mustJSON(t, latest)
	require.ErrorContains(t, dagrun.RevertPushBack(latest, original, applied), "implement changed after push-back")
	assert.JSONEq(t, before, mustJSON(t, latest))
}

func TestValidatePushBackInputsSize(t *testing.T) {
	t.Parallel()

	require.NoError(t, dagrun.ValidatePushBackInputsSize(nil))
	require.NoError(t, dagrun.ValidatePushBackInputsSize(map[string]string{"feedback": strings.Repeat("x", 16000)}))
	err := dagrun.ValidatePushBackInputsSize(map[string]string{"feedback": strings.Repeat("x", dagrun.MaxPushBackInputsSize)})
	require.ErrorContains(t, err, "maximum size of 16384 bytes")
}

func cloneStatus(t *testing.T, status *ir.DAGRunStatus) *ir.DAGRunStatus {
	t.Helper()
	var clone ir.DAGRunStatus
	require.NoError(t, json.Unmarshal([]byte(mustJSON(t, status)), &clone))
	return &clone
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
