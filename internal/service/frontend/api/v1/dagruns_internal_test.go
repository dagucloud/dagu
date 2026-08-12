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

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proc"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/testutil"
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

	status := deriveManualDAGRunStatus([]*ir.Node{
		{
			Step:   ir.Step{Name: "retrying"},
			Status: ir.NodeRetrying,
		},
	}, ir.Failed)

	assert.Equal(t, ir.Running, status)
}

func TestDeriveManualDAGRunStatusContinueOnMarkSuccessIsContinuable(t *testing.T) {
	t.Parallel()

	status := deriveManualDAGRunStatus([]*ir.Node{
		{
			Step: ir.Step{
				Name: "failed-continuable",
				ContinueOn: ir.ContinueOn{
					Failure:     true,
					MarkSuccess: true,
				},
			},
			Status: ir.NodeFailed,
		},
		{
			Step:   ir.Step{Name: "succeeded"},
			Status: ir.NodeSucceeded,
		},
	}, ir.Running)

	assert.Equal(t, ir.PartiallySucceeded, status)
}

func TestDeriveManualDAGRunStatusMixedNotStartedAndSucceededIsNonRunning(t *testing.T) {
	t.Parallel()

	status := deriveManualDAGRunStatus([]*ir.Node{
		{
			Step:   ir.Step{Name: "succeeded"},
			Status: ir.NodeSucceeded,
		},
		{
			Step:   ir.Step{Name: "reset"},
			Status: ir.NodeNotStarted,
		},
	}, ir.Succeeded)

	assert.Equal(t, ir.PartiallySucceeded, status)
}

func TestApplyPushBackRewindToResetsNamedStepAndDependents(t *testing.T) {
	t.Parallel()

	inputs := map[string]string{"FEEDBACK": "try again"}
	status := &ir.DAGRunStatus{
		Nodes: []*ir.Node{
			{
				Step:       ir.Step{Name: "bootstrap"},
				Status:     ir.NodeSucceeded,
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step:       ir.Step{Name: "prepare", Depends: []string{"bootstrap"}},
				Status:     ir.NodeSucceeded,
				Stdout:     "/tmp/prepare-prev.out",
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step:       ir.Step{Name: "sidecar", Depends: []string{"prepare"}},
				Status:     ir.NodeSucceeded,
				Stdout:     "/tmp/sidecar-prev.out",
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step: ir.Step{
					Name:    "review",
					Depends: []string{"prepare"},
					Approval: &ir.ApprovalConfig{
						Input:    []string{"FEEDBACK"},
						RewindTo: "prepare",
					},
				},
				Status:     ir.NodeWaiting,
				Stdout:     "/tmp/review-prev.out",
				StartedAt:  "started",
				FinishedAt: "finished",
			},
			{
				Step:       ir.Step{Name: "deploy", Depends: []string{"review"}},
				Status:     ir.NodeNotStarted,
				Stdout:     "",
				StartedAt:  "-",
				FinishedAt: "-",
			},
			{
				Step:       ir.Step{Name: "notify", Depends: []string{"bootstrap"}},
				Status:     ir.NodeSucceeded,
				StartedAt:  "started",
				FinishedAt: "finished",
			},
		},
	}

	err := applyPushBack(context.Background(), status.Nodes[3], status, &openapiv1.PushBackStepRequest{
		Inputs: &inputs,
	})
	require.NoError(t, err)

	assert.Equal(t, ir.NodeSucceeded, status.Nodes[0].Status)
	assert.Equal(t, ir.NodeNotStarted, status.Nodes[1].Status)
	assert.Equal(t, ir.NodeNotStarted, status.Nodes[2].Status)
	assert.Equal(t, ir.NodeNotStarted, status.Nodes[3].Status)
	assert.Equal(t, ir.NodeNotStarted, status.Nodes[4].Status)
	assert.Equal(t, ir.NodeSucceeded, status.Nodes[5].Status)
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

	approvalStep := ir.Step{Name: "approval", Approval: &ir.ApprovalConfig{}}
	humanStep := ir.Step{ID: "review", Name: "review", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}}
	original := &ir.DAGRunStatus{
		Name: "test", DAGRunID: "run-1", AttemptID: "attempt-1", AttemptKey: "key-1", Status: ir.Waiting,
		Nodes: []*ir.Node{
			{Step: approvalStep, Status: ir.NodeWaiting, StartedAt: "started"},
			{Step: humanStep, Status: ir.NodeWaiting},
		},
	}
	applied, err := cloneManualStatus(original)
	require.NoError(t, err)
	require.NoError(t, applyPushBack(context.Background(), applied.Nodes[0], applied, nil))
	current, err := cloneManualStatus(applied)
	require.NoError(t, err)
	current.Nodes[1].Status = ir.NodeSucceeded
	current.Nodes[1].HumanTaskInput = json.RawMessage(`{"confirmed":true}`)

	repository := &manualCASStore{status: current}
	a := &API{dagRunRepository: dagrun.NewRepository(repository, nil, dagrun.RepositoryOptions{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, a.rollbackPushBack(ctx, current.DAGRun(), applied, original))

	assert.Equal(t, ir.NodeWaiting, current.Nodes[0].Status)
	assert.Equal(t, "started", current.Nodes[0].StartedAt)
	assert.Equal(t, ir.NodeSucceeded, current.Nodes[1].Status)
	assert.JSONEq(t, `{"confirmed":true}`, string(current.Nodes[1].HumanTaskInput))
}

type manualCASStore struct {
	testutil.DAGRunBackendStub
	status *ir.DAGRunStatus
}

type manualStepAttempt struct {
	dagrun.Attempt
	dag      *ir.DAG
	statuses []*ir.DAGRunStatus
	reads    int
}

func (a *manualStepAttempt) ReadDAG(context.Context) (*ir.DAG, error) {
	return a.dag, nil
}

func (a *manualStepAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	idx := a.reads
	if idx >= len(a.statuses) {
		idx = len(a.statuses) - 1
	}
	a.reads++
	return a.statuses[idx], nil
}

type manualStepProcStore struct {
	proc.ProcStore
	alive bool
	err   error
}

func (s *manualStepProcStore) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return s.alive, s.err
}

type failingManualCASStore struct {
	testutil.DAGRunBackendStub
	base *dagrun.Repository
	err  error
}

func (s *failingManualCASStore) CompareAndSwapLatestAttemptStatus(
	context.Context,
	dagrun.CompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	return nil, false, s.err
}

func (s *failingManualCASStore) FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	return s.base.FindAttempt(ctx, ref)
}

func (s *manualCASStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	req dagrun.CompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.status.AttemptID != req.ExpectedAttemptID || s.status.Status != req.ExpectedStatus {
		return s.status, false, nil
	}
	if err := req.Mutate(s.status); err != nil {
		return nil, false, err
	}
	return s.status, true, nil
}

func TestWaitForManualStepMutationReadyFailsClosedOnLivenessError(t *testing.T) {
	status := &ir.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Waiting,
		WorkerID:  "local",
	}
	livenessErr := errors.New("liveness unavailable")
	a := &API{procStore: &manualStepProcStore{err: livenessErr}}
	attempt := &manualStepAttempt{dag: &ir.DAG{Name: status.Name}}

	updated, err := a.waitForManualStepMutationReady(t.Context(), attempt, status)

	assert.Nil(t, updated)
	require.ErrorIs(t, err, livenessErr)
}

func TestWaitForManualStepMutationReadyHonorsCancellation(t *testing.T) {
	status := &ir.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Waiting,
		WorkerID:  "local",
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := &API{procStore: &manualStepProcStore{alive: true}}
	attempt := &manualStepAttempt{dag: &ir.DAG{Name: status.Name}}

	updated, err := a.waitForManualStepMutationReady(ctx, attempt, status)

	assert.Nil(t, updated)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWaitForManualStepMutationReadyWaitsForRemotePersistence(t *testing.T) {
	status := &ir.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Waiting,
		WorkerID:  "worker-1",
	}
	finalized := *status
	finalized.FinishedAt = stringutil.FormatTime(time.Now())
	attempt := &manualStepAttempt{statuses: []*ir.DAGRunStatus{status, &finalized}}

	updated, err := (&API{}).waitForManualStepMutationReady(t.Context(), attempt, status)

	require.NoError(t, err)
	assert.Same(t, &finalized, updated)
	assert.Equal(t, 2, attempt.reads)
}

func TestWaitForManualStepMutationReadyWaitsForLocalPersistence(t *testing.T) {
	status := &ir.DAGRunStatus{
		Name:      "manual-dag",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Waiting,
		WorkerID:  "local",
	}
	finalized := *status
	finalized.FinishedAt = stringutil.FormatTime(time.Now())
	attempt := &manualStepAttempt{
		dag:      &ir.DAG{Name: status.Name},
		statuses: []*ir.DAGRunStatus{status, &finalized},
	}
	a := &API{procStore: &manualStepProcStore{}}

	updated, err := a.waitForManualStepMutationReady(t.Context(), attempt, status)

	require.NoError(t, err)
	assert.Same(t, &finalized, updated)
	assert.Equal(t, 2, attempt.reads)
}

func TestApproveDAGRunStepReturnsInternalErrorWhenStatusWriteFails(t *testing.T) {
	ctx := t.Context()
	persistedRepository := testutil.NewFileDAGRunRepository(t.TempDir(), dagrun.RepositoryOptions{LatestStatusToday: true})
	dag := &ir.DAG{
		Name: "approval-write-failure",
		Steps: []ir.Step{{
			Name:     "approve",
			Approval: &ir.ApprovalConfig{Prompt: "Approve"},
		}},
	}
	attempt, err := persistedRepository.CreateAttempt(ctx, dag, time.Now(), "run-1", dagrun.CreateAttemptOptions{})
	require.NoError(t, err)
	status := ir.InitialStatus(dag)
	status.DAGRunID = "run-1"
	status.AttemptID = attempt.ID()
	status.Status = ir.Waiting
	status.FinishedAt = stringutil.FormatTime(time.Now())
	status.Nodes[0].Status = ir.NodeWaiting
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	writeErr := errors.New("status repository unavailable")
	failingStore := &failingManualCASStore{base: persistedRepository, err: writeErr}
	repository := dagrun.NewRepository(failingStore, nil, dagrun.RepositoryOptions{})
	cfg := &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
		config.PermissionRunDAGs: true,
	}}}
	a := &API{
		dagRunRepository: repository,
		dagRunMgr:        runtimepkg.NewManager(repository, nil, cfg),
		procStore:        &manualStepProcStore{},
		config:           cfg,
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
	status := &ir.DAGRunStatus{
		Nodes: []*ir.Node{
			{
				Step: ir.Step{
					Name: "review",
					Approval: &ir.ApprovalConfig{
						Input: []string{"FEEDBACK"},
					},
				},
				Status:            ir.NodeWaiting,
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
	status := &ir.DAGRunStatus{
		Nodes: []*ir.Node{
			{
				Step: ir.Step{
					Name: "review",
					Approval: &ir.ApprovalConfig{
						Input: []string{"FEEDBACK"},
					},
				},
				Status: ir.NodeWaiting,
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
	approved := &ir.Node{}
	applyApproval(ctx, approved, nil)
	assert.Equal(t, "reviewer", approved.ApprovedBy)
	assert.Equal(t, "user-1", approved.ApprovedByID)

	rejected := &ir.Node{}
	status := &ir.DAGRunStatus{}
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

	applied := statusQueryFromOptions(t, opts.query)

	require.Equal(t, []ir.Status{
		ir.Status(openapiv1.StatusQueued),
		ir.Status(openapiv1.StatusRunning),
		ir.Status(openapiv1.StatusPartialSuccess),
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

type blockingDAGRunBackend struct {
	testutil.DAGRunBackendStub
}

type queryCapturingDAGRunBackend struct {
	testutil.DAGRunBackendStub
	query dagrun.StatusQuery
}

func (b *queryCapturingDAGRunBackend) QueryStatuses(
	_ context.Context,
	query dagrun.StatusQuery,
) (dagrun.StatusPage, error) {
	b.query = query
	return dagrun.StatusPage{}, nil
}

func statusQueryFromOptions(t *testing.T, opts []dagrun.ListStatusesOption) dagrun.StatusQuery {
	t.Helper()
	backend := &queryCapturingDAGRunBackend{}
	repository := dagrun.NewRepository(backend, nil, dagrun.RepositoryOptions{})
	_, err := repository.ListStatuses(context.Background(), opts...)
	require.NoError(t, err)
	return backend.query
}

func (blockingDAGRunBackend) QueryStatuses(ctx context.Context, _ dagrun.StatusQuery) (dagrun.StatusPage, error) {
	<-ctx.Done()
	return dagrun.StatusPage{}, ctx.Err()
}

func TestAPIListDAGRunsReturnsGatewayTimeoutWhenReadDeadlineExpires(t *testing.T) {
	t.Parallel()

	api := &API{
		dagRunRepository: dagrun.NewRepository(blockingDAGRunBackend{}, nil, dagrun.RepositoryOptions{}),
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

		listOpts := statusQueryFromOptions(t, opts.query)

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

		listOpts := statusQueryFromOptions(t, opts.query)

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

		listOpts := statusQueryFromOptions(t, opts.query)

		assert.Nil(t, listOpts.WorkspaceFilter)
	})
}

type blockingLatestAttemptStore struct {
	testutil.DAGRunBackendStub
}

func (blockingLatestAttemptStore) LatestAttempt(ctx context.Context, _ dagrun.LatestAttemptQuery) (dagrun.Attempt, error) {
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
		dagRunRepository: dagrun.NewRepository(blockingLatestAttemptStore{}, nil, dagrun.RepositoryOptions{}),
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
