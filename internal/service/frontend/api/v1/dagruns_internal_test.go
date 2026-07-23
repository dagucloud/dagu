// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	runtimepkg "github.com/dagucloud/dagu/internal/runtime"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func labelsFromPatchedSpec(t *testing.T, data []byte) []any {
	t.Helper()

	var firstDoc yaml.MapSlice
	require.NoError(t, yaml.Unmarshal(data, &firstDoc))

	raw, ok := getInlineEnqueueMapValue(firstDoc, "labels")
	require.True(t, ok)

	labels, ok := raw.([]any)
	require.True(t, ok)
	return labels
}

func requireNoDeprecatedTagsKey(t *testing.T, data []byte) {
	t.Helper()

	var firstDoc yaml.MapSlice
	require.NoError(t, yaml.Unmarshal(data, &firstDoc))

	_, ok := getInlineEnqueueMapValue(firstDoc, "tags")
	require.False(t, ok)
}

func TestDeriveManualDAGRunStatusRetryingIsRunning(t *testing.T) {
	t.Parallel()

	status := deriveManualDAGRunStatus([]*exec.Node{
		{
			Step:   core.Step{Name: "retrying"},
			Status: core.NodeRetrying,
		},
	}, core.Failed)

	assert.Equal(t, core.Running, status)
}

func TestDeriveManualDAGRunStatusContinueOnMarkSuccessIsContinuable(t *testing.T) {
	t.Parallel()

	status := deriveManualDAGRunStatus([]*exec.Node{
		{
			Step: core.Step{
				Name: "failed-continuable",
				ContinueOn: core.ContinueOn{
					Failure:     true,
					MarkSuccess: true,
				},
			},
			Status: core.NodeFailed,
		},
		{
			Step:   core.Step{Name: "succeeded"},
			Status: core.NodeSucceeded,
		},
	}, core.Running)

	assert.Equal(t, core.PartiallySucceeded, status)
}

func TestDeriveManualDAGRunStatusMixedNotStartedAndSucceededIsNonRunning(t *testing.T) {
	t.Parallel()

	status := deriveManualDAGRunStatus([]*exec.Node{
		{
			Step:   core.Step{Name: "succeeded"},
			Status: core.NodeSucceeded,
		},
		{
			Step:   core.Step{Name: "reset"},
			Status: core.NodeNotStarted,
		},
	}, core.Succeeded)

	assert.Equal(t, core.PartiallySucceeded, status)
}

func TestApplyPushBackRewindToResetsNamedStepAndDependents(t *testing.T) {
	t.Parallel()

	inputs := map[string]string{"FEEDBACK": "try again"}
	status := &exec.DAGRunStatus{
		Nodes: []*exec.Node{
			{
				Step:       core.Step{Name: "bootstrap"},
				Status:     core.NodeSucceeded,
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step:       core.Step{Name: "prepare", Depends: []string{"bootstrap"}},
				Status:     core.NodeSucceeded,
				Stdout:     "/tmp/prepare-prev.out",
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step:       core.Step{Name: "sidecar", Depends: []string{"prepare"}},
				Status:     core.NodeSucceeded,
				Stdout:     "/tmp/sidecar-prev.out",
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step: core.Step{
					Name:    "review",
					Depends: []string{"prepare"},
					Approval: &core.ApprovalConfig{
						Input:    []string{"FEEDBACK"},
						RewindTo: "prepare",
					},
				},
				Status:     core.NodeWaiting,
				Stdout:     "/tmp/review-prev.out",
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step:       core.Step{Name: "deploy", Depends: []string{"review"}},
				Status:     core.NodeNotStarted,
				Stdout:     "",
				StartedAt:  "-",
				FinishedAt: "-",
			},
			{
				Step:       core.Step{Name: "notify", Depends: []string{"bootstrap"}},
				Status:     core.NodeSucceeded,
				StartedAt:  "started",
				FinishedAt: "finished",
			},
		},
	}

	err := applyPushBack(context.Background(), status.Nodes[3], status, &openapiv1.PushBackStepRequest{
		Inputs: &inputs,
	})
	require.NoError(t, err)

	assert.Equal(t, core.NodeSucceeded, status.Nodes[0].Status)
	assert.Equal(t, core.NodeNotStarted, status.Nodes[1].Status)
	assert.Equal(t, core.NodeNotStarted, status.Nodes[2].Status)
	assert.Equal(t, core.NodeNotStarted, status.Nodes[3].Status)
	assert.Equal(t, core.NodeNotStarted, status.Nodes[4].Status)
	assert.Equal(t, core.NodeSucceeded, status.Nodes[5].Status)
	assert.Equal(t, "-", status.Nodes[1].StartedAt)
	assert.Equal(t, "-", status.Nodes[2].StartedAt)
	assert.Equal(t, "-", status.Nodes[3].StartedAt)
	assert.Equal(t, "", status.Nodes[3].Error)
	assert.Zero(t, status.Nodes[0].ApprovalIteration)
	assert.Nil(t, status.Nodes[0].PushBackInputs)
	assert.Zero(t, status.Nodes[5].ApprovalIteration)
	assert.Nil(t, status.Nodes[5].PushBackInputs)

	for _, idx := range []int{1, 2, 3, 4} {
		assert.Equal(t, 1, status.Nodes[idx].ApprovalIteration)
		assert.Equal(t, inputs, status.Nodes[idx].PushBackInputs)
	}
	assert.Equal(t, "/tmp/prepare-prev.out", status.Nodes[1].PushBackPreviousStdout)
	assert.Equal(t, "/tmp/sidecar-prev.out", status.Nodes[2].PushBackPreviousStdout)
	assert.Equal(t, "/tmp/review-prev.out", status.Nodes[3].PushBackPreviousStdout)
	assert.Empty(t, status.Nodes[4].PushBackPreviousStdout)

	rawNode, err := json.Marshal(status.Nodes[3])
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rawNode, &payload))

	history, ok := payload["pushBackHistory"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)

	first, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), first["iteration"])

	historyInputs, ok := first["inputs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "try again", historyInputs["FEEDBACK"])

	for _, idx := range []int{1, 2, 4} {
		rawNode, err := json.Marshal(status.Nodes[idx])
		require.NoError(t, err)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(rawNode, &payload))

		history, ok := payload["pushBackHistory"].([]any)
		require.True(t, ok)
		require.Len(t, history, 1)

		first, ok := history[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(1), first["iteration"])

		historyInputs, ok := first["inputs"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "try again", historyInputs["FEEDBACK"])
	}
}

func TestRollbackPushBackIgnoresCancellationAndPreservesConcurrentUnrelatedNodeChanges(t *testing.T) {
	t.Parallel()

	approvalStep := core.Step{Name: "approval", Approval: &core.ApprovalConfig{}}
	humanStep := core.Step{ID: "review", Name: "review", HumanTask: &core.HumanTaskConfig{Prompt: "Review"}}
	original := &exec.DAGRunStatus{
		Name: "test", DAGRunID: "run-1", AttemptID: "attempt-1", AttemptKey: "key-1", Status: core.Waiting,
		Nodes: []*exec.Node{
			{Step: approvalStep, Status: core.NodeWaiting, StartedAt: "started"},
			{Step: humanStep, Status: core.NodeWaiting},
		},
	}
	applied, err := cloneManualStatus(original)
	require.NoError(t, err)
	require.NoError(t, applyPushBack(context.Background(), applied.Nodes[0], applied, nil))
	current, err := cloneManualStatus(applied)
	require.NoError(t, err)
	current.Nodes[1].Status = core.NodeSucceeded
	current.Nodes[1].HumanTaskInput = json.RawMessage(`{"confirmed":true}`)

	store := &manualCASStore{status: current}
	a := &API{dagRunStore: store}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, a.rollbackPushBack(ctx, current.DAGRun(), applied, original))

	assert.Equal(t, core.NodeWaiting, current.Nodes[0].Status)
	assert.Equal(t, "started", current.Nodes[0].StartedAt)
	assert.Equal(t, core.NodeSucceeded, current.Nodes[1].Status)
	assert.JSONEq(t, `{"confirmed":true}`, string(current.Nodes[1].HumanTaskInput))
}

type manualCASStore struct {
	exec.DAGRunStore
	status *exec.DAGRunStatus
}

type manualStepAttempt struct {
	exec.DAGRunAttempt
	dag      *core.DAG
	statuses []*exec.DAGRunStatus
	reads    int
}

func (a *manualStepAttempt) ReadDAG(context.Context) (*core.DAG, error) {
	return a.dag, nil
}

func (a *manualStepAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	idx := a.reads
	if idx >= len(a.statuses) {
		idx = len(a.statuses) - 1
	}
	a.reads++
	return a.statuses[idx], nil
}

type manualStepProcStore struct {
	exec.ProcStore
	alive bool
	err   error
}

func (s *manualStepProcStore) IsAttemptAlive(context.Context, string, exec.DAGRunRef, string) (bool, error) {
	return s.alive, s.err
}

type failingManualCASStore struct {
	exec.DAGRunStore
	err error
}

func (s *failingManualCASStore) CompareAndSwapLatestAttemptStatus(
	context.Context,
	exec.DAGRunRef,
	string,
	core.Status,
	func(*exec.DAGRunStatus) error,
	...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	return nil, false, s.err
}

func (s *manualCASStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	_ exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	_ ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.status.AttemptID != expectedAttemptID || s.status.Status != expectedStatus {
		return s.status, false, nil
	}
	if err := mutate(s.status); err != nil {
		return nil, false, err
	}
	return s.status, true, nil
}

func TestWaitForManualStepMutationReadyFailsClosedOnLivenessError(t *testing.T) {
	status := &exec.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Waiting,
		WorkerID:  "local",
	}
	livenessErr := errors.New("liveness unavailable")
	a := &API{procStore: &manualStepProcStore{err: livenessErr}}
	attempt := &manualStepAttempt{dag: &core.DAG{Name: status.Name}}

	updated, err := a.waitForManualStepMutationReady(t.Context(), attempt, status)

	assert.Nil(t, updated)
	require.ErrorIs(t, err, livenessErr)
}

func TestWaitForManualStepMutationReadyHonorsCancellation(t *testing.T) {
	status := &exec.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Waiting,
		WorkerID:  "local",
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := &API{procStore: &manualStepProcStore{alive: true}}
	attempt := &manualStepAttempt{dag: &core.DAG{Name: status.Name}}

	updated, err := a.waitForManualStepMutationReady(ctx, attempt, status)

	assert.Nil(t, updated)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWaitForManualStepMutationReadyWaitsForRemotePersistence(t *testing.T) {
	status := &exec.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Waiting,
		WorkerID:  "worker-1",
	}
	finalized := *status
	finalized.FinishedAt = exec.FormatTime(time.Now())
	attempt := &manualStepAttempt{statuses: []*exec.DAGRunStatus{status, &finalized}}

	updated, err := (&API{}).waitForManualStepMutationReady(t.Context(), attempt, status)

	require.NoError(t, err)
	assert.Same(t, &finalized, updated)
	assert.Equal(t, 2, attempt.reads)
}

func TestWaitForManualStepMutationReadyWaitsForLocalPersistence(t *testing.T) {
	status := &exec.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Waiting,
		WorkerID:  "local",
	}
	finalized := *status
	finalized.FinishedAt = exec.FormatTime(time.Now())
	attempt := &manualStepAttempt{
		dag:      &core.DAG{Name: status.Name},
		statuses: []*exec.DAGRunStatus{status, &finalized},
	}
	a := &API{procStore: &manualStepProcStore{}}

	updated, err := a.waitForManualStepMutationReady(t.Context(), attempt, status)

	require.NoError(t, err)
	assert.Same(t, &finalized, updated)
	assert.Equal(t, 2, attempt.reads)
}

func TestApproveDAGRunStepReturnsInternalErrorWhenStatusWriteFails(t *testing.T) {
	ctx := t.Context()
	store := dagrun.New(t.TempDir())
	dag := &core.DAG{
		Name: "approval-write-failure",
		Steps: []core.Step{{
			Name:     "approve",
			Approval: &core.ApprovalConfig{Prompt: "Approve"},
		}},
	}
	attempt, err := store.CreateAttempt(ctx, dag, time.Now(), "run-1", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	status := exec.InitialStatus(dag)
	status.DAGRunID = "run-1"
	status.AttemptID = attempt.ID()
	status.Status = core.Waiting
	status.FinishedAt = exec.FormatTime(time.Now())
	status.Nodes[0].Status = core.NodeWaiting
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	writeErr := errors.New("status store unavailable")
	failingStore := &failingManualCASStore{DAGRunStore: store, err: writeErr}
	cfg := &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
		config.PermissionRunDAGs: true,
	}}}
	a := &API{
		dagRunStore: failingStore,
		dagRunMgr:   runtimepkg.NewManager(failingStore, nil, cfg),
		procStore:   &manualStepProcStore{},
		config:      cfg,
	}

	response, err := a.ApproveDAGRunStep(ctx, openapiv1.ApproveDAGRunStepRequestObject{
		Name:     dag.Name,
		DagRunId: status.DAGRunID,
		StepName: "approve",
		Body:     &openapiv1.ApproveStepRequest{},
	})

	assert.Nil(t, response)
	require.ErrorIs(t, err, writeErr)
	code, message, statusCode := a.resolveError(err)
	assert.Equal(t, openapiv1.ErrorCodeInternalError, code)
	assert.Equal(t, "An unexpected error occurred", message)
	assert.Equal(t, http.StatusInternalServerError, statusCode)
}

func TestApplyPushBackAppendsLegacyPushBackInputsToHistory(t *testing.T) {
	t.Parallel()

	firstInputs := map[string]string{"FEEDBACK": "first pass"}
	secondInputs := map[string]string{"FEEDBACK": "second pass"}
	status := &exec.DAGRunStatus{
		Nodes: []*exec.Node{
			{
				Step: core.Step{
					Name: "review",
					Approval: &core.ApprovalConfig{
						Input: []string{"FEEDBACK"},
					},
				},
				Status:            core.NodeWaiting,
				ApprovalIteration: 1,
				PushBackInputs:    firstInputs,
			},
		},
	}

	err := applyPushBack(context.Background(), status.Nodes[0], status, &openapiv1.PushBackStepRequest{
		Inputs: &secondInputs,
	})
	require.NoError(t, err)

	assert.Equal(t, 2, status.Nodes[0].ApprovalIteration)
	assert.Equal(t, secondInputs, status.Nodes[0].PushBackInputs)

	rawNode, err := json.Marshal(status.Nodes[0])
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rawNode, &payload))

	history, ok := payload["pushBackHistory"].([]any)
	require.True(t, ok)
	require.Len(t, history, 2)

	first, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), first["iteration"])
	firstHistoryInputs, ok := first["inputs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "first pass", firstHistoryInputs["FEEDBACK"])

	second, ok := history[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(2), second["iteration"])
	secondHistoryInputs, ok := second["inputs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "second pass", secondHistoryInputs["FEEDBACK"])
}

func TestApplyPushBackRecordsAuthenticatedUserInHistory(t *testing.T) {
	t.Parallel()

	inputs := map[string]string{"FEEDBACK": "needs revision"}
	status := &exec.DAGRunStatus{
		Nodes: []*exec.Node{
			{
				Step: core.Step{
					Name: "review",
					Approval: &core.ApprovalConfig{
						Input: []string{"FEEDBACK"},
					},
				},
				Status: core.NodeWaiting,
			},
		},
	}

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "user-1", Username: "reviewer1"})
	err := applyPushBack(ctx, status.Nodes[0], status, &openapiv1.PushBackStepRequest{
		Inputs: &inputs,
	})
	require.NoError(t, err)

	require.Len(t, status.Nodes[0].PushBackHistory, 1)
	assert.Equal(t, "reviewer1", status.Nodes[0].PushBackHistory[0].By)
	assert.Equal(t, "user-1", status.Nodes[0].PushBackHistory[0].ByID)

	rawNode, err := json.Marshal(status.Nodes[0])
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rawNode, &payload))

	history, ok := payload["pushBackHistory"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)

	first, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "reviewer1", first["by"])
	assert.Equal(t, "user-1", first["byId"])
	at, ok := first["at"].(string)
	require.True(t, ok)
	_, err = time.Parse(time.RFC3339, at)
	require.NoError(t, err)
}

func TestApprovalMutationsRecordAuthenticatedSubjectID(t *testing.T) {
	t.Parallel()

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "user-1", Username: "reviewer"})
	approved := &exec.Node{}
	applyApproval(ctx, approved, nil)
	assert.Equal(t, "reviewer", approved.ApprovedBy)
	assert.Equal(t, "user-1", approved.ApprovedByID)

	rejected := &exec.Node{}
	status := &exec.DAGRunStatus{}
	applyRejection(ctx, rejected, status, nil)
	assert.Equal(t, "reviewer", rejected.RejectedBy)
	assert.Equal(t, "user-1", rejected.RejectedByID)
}

func TestApplyInlineEnqueueLabels_ArrayLabels(t *testing.T) {
	t.Parallel()

	data := []byte(`name: test
labels:
  - env=prod
steps:
  - name: s1
    run: echo hi
`)

	patched, err := applyInlineEnqueueLabels(data, "team=backend")
	require.NoError(t, err)

	labels := labelsFromPatchedSpec(t, patched)
	assert.Contains(t, labels, "env=prod")
	assert.Contains(t, labels, "team=backend")
	requireNoDeprecatedTagsKey(t, patched)
}

func TestApplyInlineEnqueueLabels_CommaSeparatedStringLabels(t *testing.T) {
	t.Parallel()

	data := []byte(`name: test
labels: "daily, weekly"
steps:
  - name: s1
    run: echo hi
`)

	patched, err := applyInlineEnqueueLabels(data, "team=backend")
	require.NoError(t, err)

	labels := labelsFromPatchedSpec(t, patched)
	assert.Contains(t, labels, "daily")
	assert.Contains(t, labels, "weekly")
	assert.Contains(t, labels, "team=backend")
	requireNoDeprecatedTagsKey(t, patched)
}

func TestApplyInlineEnqueueLabels_SpaceSeparatedKeyValueLabels(t *testing.T) {
	t.Parallel()

	data := []byte(`name: test
labels: "env=prod team=platform"
steps:
  - name: s1
    run: echo hi
`)

	patched, err := applyInlineEnqueueLabels(data, "team=backend")
	require.NoError(t, err)

	labels := labelsFromPatchedSpec(t, patched)
	assert.Contains(t, labels, "env=prod")
	assert.Contains(t, labels, "team=platform")
	assert.Contains(t, labels, "team=backend")
	requireNoDeprecatedTagsKey(t, patched)
}

func TestApplyInlineEnqueueLabels_MapLabels(t *testing.T) {
	t.Parallel()

	data := []byte(`name: test
labels:
  env: prod
  team: platform
steps:
  - name: s1
    run: echo hi
`)

	patched, err := applyInlineEnqueueLabels(data, "priority=high")
	require.NoError(t, err)

	labels := labelsFromPatchedSpec(t, patched)
	assert.Contains(t, labels, "env=prod")
	assert.Contains(t, labels, "team=platform")
	assert.Contains(t, labels, "priority=high")
	requireNoDeprecatedTagsKey(t, patched)
}

func TestApplyInlineEnqueueLabels_DeprecatedTagsCanonicalizeToLabels(t *testing.T) {
	t.Parallel()

	data := []byte(`name: test
tags:
  - env=prod
steps:
  - name: s1
    run: echo hi
`)

	patched, err := applyInlineEnqueueLabels(data, "team=backend")
	require.NoError(t, err)

	labels := labelsFromPatchedSpec(t, patched)
	assert.Contains(t, labels, "env=prod")
	assert.Contains(t, labels, "team=backend")
	requireNoDeprecatedTagsKey(t, patched)
}

func TestApplyInlineEnqueueLabels_PreservesLaterDocuments(t *testing.T) {
	t.Parallel()

	data := []byte(`name: main
steps:
  - name: s1
    run: echo hi
---
name: child
steps:
  - name: s2
    run: echo bye
`)

	patched, err := applyInlineEnqueueLabels(data, "env=prod")
	require.NoError(t, err)

	content := string(patched)
	assert.Contains(t, content, "labels:")
	assert.Contains(t, content, "env=prod")
	assert.Contains(t, content, "---")
	assert.True(t, strings.Contains(content, "name: child") || strings.Contains(content, "name: \"child\""))
	assert.Contains(t, content, "echo bye")
	requireNoDeprecatedTagsKey(t, patched)
}

func TestApplyInlineEnqueueLabels_InvalidYAML(t *testing.T) {
	t.Parallel()

	_, err := applyInlineEnqueueLabels([]byte("{{invalid yaml"), "env=prod")
	require.Error(t, err)
}

func TestDAGRunListOptionsFromQueryStringParsesMultipleStatuses(t *testing.T) {
	t.Parallel()

	api := &API{}
	opts, err := api.dagRunListOptionsFromQueryString(
		context.Background(),
		"status=5&status=1,6&limit=20",
	)
	require.NoError(t, err)

	var applied exec.ListDAGRunStatusesOptions
	for _, opt := range opts.query {
		opt(&applied)
	}

	require.Equal(t, []core.Status{
		core.Status(openapiv1.StatusQueued),
		core.Status(openapiv1.StatusRunning),
		core.Status(openapiv1.StatusPartialSuccess),
	}, applied.Statuses)
	require.Equal(t, 20, applied.Limit)
}

func TestDAGRunListOptionsFromQueryStringRejectsInvalidStatuses(t *testing.T) {
	t.Parallel()

	api := &API{}
	_, err := api.dagRunListOptionsFromQueryString(
		context.Background(),
		"status=1&status=running",
	)
	require.Error(t, err)

	apiErr, ok := err.(*Error)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	require.Equal(t, openapiv1.ErrorCodeBadRequest, apiErr.Code)
	require.Contains(t, apiErr.Message, "invalid status parameter")
}

var _ exec.DAGRunStore = (*blockingDAGRunStore)(nil)

type blockingDAGRunStore struct{}

func (blockingDAGRunStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	panic("not implemented")
}

func (blockingDAGRunStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	panic("not implemented")
}

func (blockingDAGRunStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	panic("not implemented")
}

func (blockingDAGRunStore) ListStatuses(ctx context.Context, _ ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingDAGRunStore) ListStatusesPage(ctx context.Context, _ ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	<-ctx.Done()
	return exec.DAGRunStatusPage{}, ctx.Err()
}

func (blockingDAGRunStore) CompareAndSwapLatestAttemptStatus(context.Context, exec.DAGRunRef, string, core.Status, func(*exec.DAGRunStatus) error, ...exec.CompareAndSwapStatusOption) (*exec.DAGRunStatus, bool, error) {
	panic("not implemented")
}

func (blockingDAGRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	panic("not implemented")
}

func (blockingDAGRunStore) FindSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	panic("not implemented")
}

func (blockingDAGRunStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	panic("not implemented")
}

func (blockingDAGRunStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	panic("not implemented")
}

func (blockingDAGRunStore) RenameDAGRuns(context.Context, string, string) error {
	panic("not implemented")
}

func (blockingDAGRunStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	panic("not implemented")
}

func TestAPIListDAGRunsReturnsGatewayTimeoutWhenReadDeadlineExpires(t *testing.T) {
	t.Parallel()

	api := &API{
		dagRunStore: blockingDAGRunStore{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	resp, err := api.ListDAGRuns(ctx, openapiv1.ListDAGRunsRequestObject{})
	require.NoError(t, err)

	timeoutResp, ok := resp.(openapiv1.ListDAGRunsdefaultJSONResponse)
	require.True(t, ok)
	require.Equal(t, http.StatusGatewayTimeout, timeoutResp.StatusCode)
	require.Equal(t, openapiv1.ErrorCodeTimeout, timeoutResp.Body.Code)
	require.Equal(t, "dag-run list request timed out", timeoutResp.Body.Message)
}

func TestDAGRunListOptionsFromQueryStringIncludesWorkspaceFilter(t *testing.T) {
	t.Parallel()

	api := &API{}

	t.Run("workspace scope", func(t *testing.T) {
		t.Parallel()

		opts, err := api.dagRunListOptionsFromQueryString(
			context.Background(),
			"workspace=ops",
		)
		require.NoError(t, err)

		var listOpts exec.ListDAGRunStatusesOptions
		for _, opt := range opts.query {
			opt(&listOpts)
		}

		require.NotNil(t, listOpts.WorkspaceFilter)
		assert.True(t, listOpts.WorkspaceFilter.Enabled)
		assert.Equal(t, []string{"ops"}, listOpts.WorkspaceFilter.Workspaces)
		assert.False(t, listOpts.WorkspaceFilter.IncludeUnlabelled)
	})

	t.Run("default scope", func(t *testing.T) {
		t.Parallel()

		opts, err := api.dagRunListOptionsFromQueryString(
			context.Background(),
			"workspace=default",
		)
		require.NoError(t, err)

		var listOpts exec.ListDAGRunStatusesOptions
		for _, opt := range opts.query {
			opt(&listOpts)
		}

		require.NotNil(t, listOpts.WorkspaceFilter)
		assert.True(t, listOpts.WorkspaceFilter.Enabled)
		assert.Empty(t, listOpts.WorkspaceFilter.Workspaces)
		assert.True(t, listOpts.WorkspaceFilter.IncludeUnlabelled)
	})

	t.Run("all scope without auth keeps aggregate unfiltered", func(t *testing.T) {
		t.Parallel()

		opts, err := api.dagRunListOptionsFromQueryString(
			context.Background(),
			"workspace=all",
		)
		require.NoError(t, err)

		var listOpts exec.ListDAGRunStatusesOptions
		for _, opt := range opts.query {
			opt(&listOpts)
		}

		assert.Nil(t, listOpts.WorkspaceFilter)
	})
}

type blockingLatestAttemptStore struct {
	blockingDAGRunStore
}

func (blockingLatestAttemptStore) LatestAttempt(ctx context.Context, _ string) (exec.DAGRunAttempt, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWithDAGRunReadTimeoutReturnsDeadlineExceededOnLateSuccess(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err := withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dag-runs/{name}/{dagRunId}",
	}, func(readCtx context.Context) (string, error) {
		<-readCtx.Done()
		return "late-success", nil
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestGetDAGRunDetailsReturnsClientClosedRequestWhenReadCanceled(t *testing.T) {
	t.Parallel()

	api := &API{
		dagRunStore: blockingLatestAttemptStore{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := api.GetDAGRunDetails(ctx, openapiv1.GetDAGRunDetailsRequestObject{
		Name:     "test",
		DagRunId: "latest",
	})
	require.NoError(t, err)

	canceledResp, ok := resp.(*openapiv1.GetDAGRunDetailsdefaultJSONResponse)
	require.True(t, ok)
	require.Equal(t, statusClientClosedRequest, canceledResp.StatusCode)
	require.Equal(t, openapiv1.ErrorCodeInternalError, canceledResp.Body.Code)
	require.Equal(t, "dag-run details request canceled", canceledResp.Body.Message)
}

func TestRebuildDAGRunSnapshotFromYAMLRestoresHarnessConfig(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name: "snapshot-harness",
		YamlData: []byte(`
harnesses:
  gemini:
    binary: gemini
    prefix_args: ["run"]
    prompt_mode: flag
    prompt_flag: --prompt
harness:
  provider: gemini
  model: gemini-2.5-pro
  fallback:
    - provider: claude
      model: sonnet
steps:
  - run: "Review the repository"
`),
	}

	restored, err := rebuildDAGRunSnapshotFromYAML(context.Background(), dag)
	require.NoError(t, err)
	require.Same(t, dag, restored)

	require.NotNil(t, restored.Harness)
	assert.Equal(t, "gemini", restored.Harness.Config["provider"])
	assert.Equal(t, "gemini-2.5-pro", restored.Harness.Config["model"])
	require.Len(t, restored.Harness.Fallback, 1)
	assert.Equal(t, "claude", restored.Harness.Fallback[0]["provider"])

	require.NotNil(t, restored.Harnesses)
	require.Contains(t, restored.Harnesses, "gemini")
	require.NotNil(t, restored.Harnesses["gemini"])
	assert.Equal(t, "gemini", restored.Harnesses["gemini"].Binary)
	assert.Equal(t, core.HarnessPromptModeFlag, restored.Harnesses["gemini"].PromptMode)
	assert.Equal(t, "--prompt", restored.Harnesses["gemini"].PromptFlag)
}
