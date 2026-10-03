// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/queue"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

type historyDAGDefinitionStore struct {
	persis.DAGDefinitionStore
}

func (historyDAGDefinitionStore) Get(context.Context, string) (persis.DAGDefinition, error) {
	return persis.DAGDefinition{
		ID: "daily.yaml",
		Source: []byte(`name: daily
steps:
  - name: run
    command: echo ok
`),
	}, nil
}

func (historyDAGDefinitionStore) GetMetadata(context.Context, string) (*ir.DAG, error) {
	return &ir.DAG{Name: "daily"}, nil
}

type failingHistoryDAGRunStore struct {
	testutil.DAGRunStoreStub
	err error
}

type pagedStopDAGRunStore struct {
	testutil.DAGRunStoreStub

	attempts map[string]*stopDAGRunAttempt
	queries  []persis.DAGRunStatusQuery
}

type stopDAGRunAttempt struct {
	dagrun.Attempt

	status  *ir.DAGRunStatus
	aborted bool
}

type stopDAGRunProcRepository struct{}

func (s *pagedStopDAGRunStore) QueryStatuses(_ context.Context, query persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	s.queries = append(s.queries, query)
	if query.Cursor == "" {
		return persis.DAGRunStatusPage{
			Items:      []*ir.DAGRunStatus{s.attempts["run-1"].status},
			NextCursor: "next-page",
		}, nil
	}
	return persis.DAGRunStatusPage{Items: []*ir.DAGRunStatus{s.attempts["run-2"].status}}, nil
}

func (s *pagedStopDAGRunStore) FindAttempt(_ context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	return s.attempts[ref.ID], nil
}

func (a *stopDAGRunAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	return a.status, nil
}

func (a *stopDAGRunAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}

func (a *stopDAGRunAttempt) Abort(context.Context) error {
	a.aborted = true
	return nil
}

func (stopDAGRunProcRepository) IsRunAlive(context.Context, string, ir.DAGRunRef) (bool, error) {
	return false, nil
}

func (stopDAGRunProcRepository) ListAlive(context.Context, string) ([]ir.DAGRunRef, error) {
	return nil, nil
}

func (stopDAGRunProcRepository) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return false, nil
}

func (stopDAGRunProcRepository) LatestFreshEntryByDAGName(context.Context, string, string) (*proc.ProcEntry, error) {
	return nil, nil
}

func (s failingHistoryDAGRunStore) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	return nil, s.err
}

func (s failingHistoryDAGRunStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	return nil, s.err
}

func TestEnsureDAGRunIDUniquePropagatesRepositoryErrors(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("storage unavailable")
	a := &API{dagRunRepository: persis.NewDAGRunRepository(
		failingHistoryDAGRunStore{err: storeErr},
		nil,
		persis.DAGRunRepositoryOptions{},
	)}

	err := a.ensureDAGRunIDUnique(context.Background(), &ir.DAG{Name: "daily"}, "run-1")
	require.ErrorIs(t, err, storeErr)
	require.ErrorContains(t, err, "failed to verify dag-run ID uniqueness")
}

func TestDAGHistoryReturnsRecentStatusStoreErrors(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("storage unavailable")
	a := &API{
		dagRepository: persis.NewDAGRepository(historyDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
		dagRunRepository: persis.NewDAGRunRepository(
			failingHistoryDAGRunStore{err: storeErr},
			nil,
			persis.DAGRunRepositoryOptions{},
		),
	}

	response, err := a.GetDAGDAGRunHistory(context.Background(), openapiv1.GetDAGDAGRunHistoryRequestObject{
		FileName: "daily.yaml",
	})
	require.Nil(t, response)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.HTTPStatus)
	assert.Equal(t, openapiv1.ErrorCodeInternalError, apiErr.Code)
	assert.Contains(t, apiErr.Message, "list recent DAG runs for daily")
	assert.Contains(t, apiErr.Message, storeErr.Error())

	_, err = a.GetDAGHistoryData(context.Background(), "daily.yaml")
	require.ErrorIs(t, err, storeErr)
	require.ErrorContains(t, err, "list recent DAG runs for daily")
}

func TestStopAllDAGRunsProcessesBoundedPages(t *testing.T) {
	t.Parallel()

	store := &pagedStopDAGRunStore{attempts: map[string]*stopDAGRunAttempt{}}
	for _, runID := range []string{"run-1", "run-2"} {
		store.attempts[runID] = &stopDAGRunAttempt{status: &ir.DAGRunStatus{
			Name:      "daily",
			DAGRunID:  runID,
			AttemptID: "attempt-1",
			Status:    ir.Running,
		}}
	}

	cfg := &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
		config.PermissionRunDAGs: true,
	}}}
	repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
	a := &API{
		dagRepository:    persis.NewDAGRepository(historyDAGDefinitionStore{}, persis.DAGRepositoryOptions{}),
		dagRunRepository: repository,
		dagRunMgr:        runtimepkg.NewManager(repository, stopDAGRunProcRepository{}, cfg),
		config:           cfg,
	}

	response, err := a.StopAllDAGRuns(t.Context(), openapiv1.StopAllDAGRunsRequestObject{FileName: "daily.yaml"})
	require.NoError(t, err)
	result, ok := response.(*openapiv1.StopAllDAGRuns200JSONResponse)
	require.True(t, ok)
	assert.Empty(t, result.Errors)
	assert.True(t, store.attempts["run-1"].aborted)
	assert.True(t, store.attempts["run-2"].aborted)
	require.Len(t, store.queries, 2)
	assert.Equal(t, "daily", store.queries[0].ExactName)
	assert.Equal(t, []ir.Status{ir.Running}, store.queries[0].Statuses)
	assert.Positive(t, store.queries[0].Limit)
	assert.Empty(t, store.queries[0].Cursor)
	assert.Equal(t, "next-page", store.queries[1].Cursor)
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
	a := &API{dagRunRepository: persis.NewDAGRunRepository(repository, nil, persis.DAGRunRepositoryOptions{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, a.rollbackPushBack(ctx, current.DAGRun(), applied, original))

	assert.Equal(t, ir.NodeWaiting, current.Nodes[0].Status)
	assert.Equal(t, "started", current.Nodes[0].StartedAt)
	assert.Equal(t, ir.NodeSucceeded, current.Nodes[1].Status)
	assert.JSONEq(t, `{"confirmed":true}`, string(current.Nodes[1].HumanTaskInput))
}

// A manual resume must not replace state recorded after the approval, even
// when the replacement is a new attempt that is also waiting.
func TestResumeWaitingDAGRun(t *testing.T) {
	const attemptID = "attempt-1"
	for _, humanTask := range []bool{false, true} {
		name := "ApprovalOnly"
		if humanTask {
			name = "HumanTask"
		}
		t.Run(name, func(t *testing.T) {
			for _, tt := range []struct {
				name      string
				status    ir.Status
				attemptID string
				wantErr   bool
			}{
				{name: "Ready", status: ir.Waiting, attemptID: attemptID},
				{name: "Rejected", status: ir.Rejected, attemptID: attemptID, wantErr: true},
				{name: "Running", status: ir.Running, attemptID: attemptID, wantErr: true},
				{name: "NewAttempt", status: ir.Waiting, attemptID: "attempt-2", wantErr: true},
				{name: "AlreadyQueued", status: ir.Queued, attemptID: attemptID},
			} {
				t.Run(tt.name, func(t *testing.T) {
					approved := &ir.DAGRunStatus{
						Name: "manual-dag", DAGRunID: "run-1", AttemptID: attemptID, Status: ir.Waiting,
						Nodes: []*ir.Node{
							{Step: ir.Step{Name: "gate_a", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded},
							{Step: ir.Step{Name: "gate_b", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeWaiting},
							{Step: ir.Step{Name: "after_a", Depends: []string{"gate_a"}}, Status: ir.NodeNotStarted},
						},
					}
					if humanTask {
						approved.Nodes = append(approved.Nodes, &ir.Node{
							Step: ir.Step{Name: "human", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}}, Status: ir.NodeWaiting,
						})
					}
					current, err := cloneManualStatus(approved)
					require.NoError(t, err)
					current.Status = tt.status
					current.AttemptID = tt.attemptID
					if tt.status == ir.Rejected {
						applyRejection(t.Context(), current.Nodes[1], current, nil)
					}
					before, err := cloneManualStatus(current)
					require.NoError(t, err)
					backend := &manualCASStore{
						status: current,
						attempt: &manualStepAttempt{
							dag: &ir.DAG{Name: current.Name, WorkerSelector: map[string]string{"region": "apac"}}, statuses: []*ir.DAGRunStatus{current},
						},
					}
					queueStore := store.NewQueueStore(file.NewCollection(t.TempDir()))
					if tt.status == ir.Queued {
						require.NoError(t, queueStore.Enqueue(t.Context(), current.Name, queue.QueuePriorityLow, current.DAGRun()))
					}
					recorder := &retryCoordinatorRecorder{}
					a := &API{
						config: &config.Config{Queues: config.Queues{Enabled: true, Config: []config.QueueConfig{{Name: current.Name, MaxActiveRuns: 1}}}}, coordinatorCli: recorder,
						dagRunRepository: persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{}),
						queueStore:       queueStore,
					}

					err = a.resumeWaitingDAGRun(t.Context(), approved.DAGRun(), approved)

					assert.Empty(t, recorder.dispatched)
					if tt.wantErr {
						require.ErrorIs(t, err, queue.ErrRetryStaleLatest)
						assert.Equal(t, before, current)
					} else {
						require.NoError(t, err)
						assert.Equal(t, ir.Queued, current.Status)
						assert.Equal(t, before.Nodes, current.Nodes)
					}
					items, err := queueStore.List(t.Context(), current.Name)
					require.NoError(t, err)
					if tt.wantErr {
						assert.Empty(t, items)
					} else {
						require.Len(t, items, 1)
						ref, err := items[0].Data()
						require.NoError(t, err)
						assert.Equal(t, approved.DAGRun(), *ref)
					}
				})
			}
		})
	}
}

// Failed queue admission must leave the approved checkpoint available to retry.
func TestApprovalResumeFailure(t *testing.T) {
	for _, humanTask := range []bool{false, true} {
		t.Run(fmt.Sprintf("HumanTask=%t", humanTask), func(t *testing.T) {
			status := &ir.DAGRunStatus{
				Name: "manual-dag", DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Waiting,
				FinishedAt: time.Now().Format(time.RFC3339),
				Nodes: []*ir.Node{
					{Step: ir.Step{Name: "gate", Approval: &ir.ApprovalConfig{Input: []string{"VERSION"}, Required: []string{"VERSION"}}}, Status: ir.NodeWaiting},
					{Step: ir.Step{Name: "after", Depends: []string{"gate"}}, Status: ir.NodeNotStarted},
					{Step: ir.Step{ID: "human", Name: "human", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}}, Status: ir.NodeWaiting},
				},
			}
			if !humanTask {
				status.Nodes = status.Nodes[:2]
			}
			backend := &manualCASStore{status: status, attempt: &manualStepAttempt{
				dag: &ir.DAG{Name: status.Name}, statuses: []*ir.DAGRunStatus{status},
			}}
			repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
			cfg := &config.Config{Queues: config.Queues{Enabled: true, Config: []config.QueueConfig{{Name: status.Name, MaxActiveRuns: 1}}}}
			cfg.Server.Permissions = map[config.Permission]bool{config.PermissionRunDAGs: true}
			queueStore := &testutil.MockQueueStore{}
			queueStore.On("Enqueue", mock.Anything, status.Name, queue.QueuePriorityLow, status.DAGRun()).Return(errors.New("temporary queue failure")).Twice()
			a := &API{config: cfg, dagRunRepository: repository, procRepository: &manualStepProcRepository{},
				dagRunMgr: runtimepkg.NewManager(repository, nil, cfg), queueStore: queueStore}
			ctx := auth.WithUser(t.Context(), &auth.User{ID: "reviewer-id", Username: "reviewer"})
			response, err := a.ApproveDAGRunStep(ctx, openapiv1.ApproveDAGRunStepRequestObject{
				Name: status.Name, DagRunId: status.DAGRunID, StepName: "gate", Body: &openapiv1.ApproveStepRequest{Inputs: ptrOf(map[string]string{"VERSION": "v1.2"})},
			})
			require.NoError(t, err)
			failure, ok := response.(*openapiv1.ApproveDAGRunStep503JSONResponse)
			require.True(t, ok)
			require.NotNil(t, failure.Details)
			assert.Equal(t, true, (*failure.Details)["approvalStored"])
			assert.Equal(t, true, (*failure.Details)["resumePending"])
			assert.Equal(t, ir.NodeSucceeded, status.Nodes[0].Status)
			assert.Equal(t, "reviewer-id", status.Nodes[0].ApprovedByID)
			assert.Equal(t, map[string]string{"version": "v1.2"}, status.Nodes[0].ApprovalInputs)
			assert.Equal(t, ir.Waiting, status.Status)
			assert.True(t, approvalResumePending(status))
			assert.Equal(t, ptrOf(true), ToDAGRunDetails(*status).ApprovalResumePending)
			approved, err := cloneManualStatus(status)
			require.NoError(t, err)
			resume := openapiv1.ResumeDAGRunRequestObject{Name: status.Name, DagRunId: status.DAGRunID}
			failedResume, err := a.ResumeDAGRun(ctx, resume)
			require.NoError(t, err)
			require.IsType(t, &openapiv1.ResumeDAGRun503JSONResponse{}, failedResume)
			assert.Equal(t, ToDAGRunDetails(*approved), ToDAGRunDetails(*status))
			queueStore.AssertExpectations(t)

			a.queueStore = store.NewQueueStore(file.NewCollection(t.TempDir()))
			for range 2 {
				resumed, err := a.ResumeDAGRun(ctx, resume)
				require.NoError(t, err)
				require.IsType(t, &openapiv1.ResumeDAGRun200JSONResponse{}, resumed)
				assert.True(t, resumed.(*openapiv1.ResumeDAGRun200JSONResponse).Resumed)
			}
			assert.Equal(t, ir.Queued, status.Status)
			assert.Equal(t, ToDAGRunDetails(*approved).Nodes, ToDAGRunDetails(*status).Nodes)
			assert.False(t, approvalResumePending(status))
			items, err := a.queueStore.List(ctx, status.Name)
			require.NoError(t, err)
			assert.Len(t, items, 1)
		})
	}
}

func TestResumeApprovalStates(t *testing.T) {
	for _, tt := range []struct {
		name            string
		change          func(*ir.DAGRunStatus)
		accepted        bool
		denied          bool
		workspaceDenied bool
	}{
		{name: "Queued", change: func(s *ir.DAGRunStatus) { s.Status = ir.Queued }, accepted: true},
		{name: "Running", change: func(s *ir.DAGRunStatus) { s.Status = ir.Running }, accepted: true},
		{name: "Rejected", change: func(s *ir.DAGRunStatus) { s.Status = ir.Rejected }},
		{name: "Failed", change: func(s *ir.DAGRunStatus) { s.Status = ir.Failed }},
		{name: "Finished", change: func(s *ir.DAGRunStatus) { s.Status = ir.Succeeded }},
		{name: "Unapproved", change: func(s *ir.DAGRunStatus) { s.Nodes[0].ApprovedAt = "" }},
		{name: "Child", change: func(s *ir.DAGRunStatus) { s.Parent = ir.NewDAGRunRef("parent", "parent-run") }},
		{name: "Blocked", change: func(s *ir.DAGRunStatus) {
			s.Nodes = append(s.Nodes, &ir.Node{Step: ir.Step{Name: "gate-2", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeWaiting})
		}},
		{name: "Permission", change: func(*ir.DAGRunStatus) {}, denied: true},
		{name: "Workspace", change: func(*ir.DAGRunStatus) {}, workspaceDenied: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status := &ir.DAGRunStatus{Name: "manual", DAGRunID: "run", AttemptID: "attempt", Status: ir.Waiting,
				FinishedAt: time.Now().Format(time.RFC3339),
				Nodes:      []*ir.Node{{Step: ir.Step{Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded, ApprovedAt: time.Now().Format(time.RFC3339)}},
			}
			tt.change(status)
			before, err := cloneManualStatus(status)
			require.NoError(t, err)
			backend := &manualCASStore{status: status, attempt: &manualStepAttempt{dag: &ir.DAG{Name: status.Name}, statuses: []*ir.DAGRunStatus{status}}}
			cfg := &config.Config{}
			cfg.Server.Permissions = map[config.Permission]bool{config.PermissionRunDAGs: !tt.denied}
			a := &API{config: cfg, procRepository: &manualStepProcRepository{},
				dagRunRepository: persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{}),
			}
			ctx := t.Context()
			if tt.workspaceDenied {
				a.authService = struct{ AuthService }{}
				ctx = auth.WithUser(ctx, &auth.User{Role: auth.RoleViewer})
			}
			response, err := a.ResumeDAGRun(ctx, openapiv1.ResumeDAGRunRequestObject{Name: status.Name, DagRunId: status.DAGRunID})
			if tt.denied {
				require.ErrorIs(t, err, errPermissionDenied)
			} else if tt.workspaceDenied {
				require.ErrorIs(t, err, errInsufficientPermissions)
			} else {
				require.NoError(t, err)
				if tt.accepted {
					require.IsType(t, &openapiv1.ResumeDAGRun200JSONResponse{}, response)
				} else {
					require.IsType(t, &openapiv1.ResumeDAGRun409JSONResponse{}, response)
				}
			}
			assert.Equal(t, before, status)
		})
	}
}

func TestManualResumeLaunchFailure(t *testing.T) {
	goExecutable, err := exec.LookPath("go")
	require.NoError(t, err)
	for _, earlyExit := range []bool{false, true} {
		t.Run(fmt.Sprintf("EarlyExit=%t", earlyExit), func(t *testing.T) {
			repository := testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{})
			dag := &ir.DAG{Name: "manual", BaseConfigWorkspace: new("")}
			attempt, err := repository.CreateAttempt(t.Context(), dag, time.Now(), "run", persis.DAGRunCreateAttemptOptions{})
			require.NoError(t, err)
			status := ir.DAGRunStatus{Name: dag.Name, DAGRunID: "run", AttemptID: attempt.ID(), Status: ir.Waiting,
				Nodes: []*ir.Node{{Step: ir.Step{Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded, ApprovedAt: time.Now().Format(time.RFC3339)}},
			}
			require.NoError(t, attempt.Open(t.Context()))
			require.NoError(t, attempt.Write(t.Context(), status))
			require.NoError(t, attempt.Close(t.Context()))
			cfg := &config.Config{Paths: config.PathsConfig{Executable: filepath.Join(t.TempDir(), "missing-dagu")}}
			if earlyExit {
				// The Go executable rejects "retry" before a Dagu attempt can start.
				cfg.Paths.Executable = goExecutable
			}
			a := &API{config: cfg, dagRunRepository: repository, subCmdBuilder: launcher.NewSubCmdBuilder(cfg)}
			err = a.resumeWaitingDAGRun(t.Context(), status.DAGRun(), &status)
			if earlyExit {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Eventually(t, func() bool {
				latest, readErr := attempt.ReadStatusUncached(t.Context())
				return readErr == nil && latest.Status == ir.Waiting && approvalResumePending(latest)
			}, 5*time.Second, 10*time.Millisecond)
			latest, err := attempt.ReadStatusUncached(t.Context())
			require.NoError(t, err)
			assert.Equal(t, status.Nodes, latest.Nodes)
			// A failed launch must leave the checkpoint available for admission.
			admission, err := queue.PrepareRetry(t.Context(), repository, dag, latest, queue.EnqueueRetryOptions{})
			require.NoError(t, err)
			require.NotNil(t, admission)
			require.NoError(t, admission.Rollback(t.Context()))
		})
	}
}

func TestManualResumeDispatch(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("Failure=%t", fail), func(t *testing.T) {
			status := &ir.DAGRunStatus{Name: "manual", DAGRunID: "run", AttemptID: "attempt", Status: ir.Waiting,
				Nodes: []*ir.Node{
					{Step: ir.Step{Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded, ApprovedAt: time.Now().Format(time.RFC3339)},
					{Step: ir.Step{Name: "agent"}, Status: ir.NodeNotStarted, AgentSession: &ir.AgentSession{Provider: "opencode", OwnerWorkerID: "worker-1"}},
				},
			}
			before, err := cloneManualStatus(status)
			require.NoError(t, err)
			backend := &manualCASStore{status: status, attempt: &manualStepAttempt{
				dag: &ir.DAG{Name: status.Name, WorkerSelector: map[string]string{"region": "apac"}}, statuses: []*ir.DAGRunStatus{status},
			}}
			recorder := &retryCoordinatorRecorder{}
			if fail {
				recorder.dispatchErr = errors.New("coordinator unavailable")
			}
			a := &API{config: &config.Config{}, coordinatorCli: recorder,
				dagRunRepository: persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{}),
			}
			err = a.resumeWaitingDAGRun(t.Context(), status.DAGRun(), before)
			require.Len(t, recorder.dispatched, 1)
			assert.Equal(t, "worker-1", recorder.dispatched[0].TargetWorkerID)
			assert.Equal(t, before.Nodes, recorder.dispatched[0].PreviousStatus.Nodes)
			if fail {
				require.ErrorIs(t, err, recorder.dispatchErr)
				assert.Equal(t, before, status)
			} else {
				require.NoError(t, err)
				assert.Equal(t, ir.Queued, status.Status)
				require.NoError(t, a.resumeWaitingDAGRun(t.Context(), status.DAGRun(), before))
				assert.Len(t, recorder.dispatched, 1)
			}
		})
	}
}

func TestResumeQueueFailure(t *testing.T) {
	queueErr := errors.New("queue unavailable")
	for _, missing := range []bool{false, true} {
		name := "Unavailable"
		if missing {
			name = "Missing"
		}
		t.Run(name, func(t *testing.T) {
			approved := &ir.DAGRunStatus{
				Name: "manual-dag", DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Waiting,
				Nodes: []*ir.Node{{Step: ir.Step{Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded}},
			}
			current, err := cloneManualStatus(approved)
			require.NoError(t, err)
			backend := &manualCASStore{
				status: current,
				attempt: &manualStepAttempt{
					dag:      &ir.DAG{Name: current.Name, WorkerSelector: map[string]string{"region": "apac"}},
					statuses: []*ir.DAGRunStatus{current},
				},
			}
			recorder := &retryCoordinatorRecorder{}
			a := &API{
				config: &config.Config{Queues: config.Queues{Enabled: true, Config: []config.QueueConfig{{Name: current.Name, MaxActiveRuns: 1}}}}, coordinatorCli: recorder,
				dagRunRepository: persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{}),
			}
			if !missing {
				queueStore := &testutil.MockQueueStore{}
				queueStore.On("Enqueue", mock.Anything, approved.Name, queue.QueuePriorityLow, approved.DAGRun()).Return(queueErr).Once()
				t.Cleanup(func() { queueStore.AssertExpectations(t) })
				a.queueStore = queueStore
			}

			err = a.resumeWaitingDAGRun(t.Context(), approved.DAGRun(), approved)

			if missing {
				require.ErrorContains(t, err, "queue store is not configured")
			} else {
				require.ErrorIs(t, err, queueErr)
			}
			assert.Equal(t, approved, current)
			assert.Empty(t, recorder.dispatched)
		})
	}
}

type manualCASStore struct {
	testutil.DAGRunStoreStub
	status  *ir.DAGRunStatus
	attempt dagrun.Attempt
}

func (s *manualCASStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	return s.attempt, nil
}

func (s *manualCASStore) FindSubAttempt(context.Context, ir.DAGRunRef, string) (dagrun.Attempt, error) {
	return s.attempt, nil
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

func (a *manualStepAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}

type manualStepProcRepository struct {
	alive bool
	err   error
}

func (s *manualStepProcRepository) WithLock(_ context.Context, _ string, fn func() error) error {
	return fn()
}

func (s *manualStepProcRepository) CountAliveByDAGName(context.Context, string, string) (int, error) {
	return 0, nil
}

func (s *manualStepProcRepository) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return s.alive, s.err
}

func (s *manualStepProcRepository) ListAllAlive(context.Context) (map[string][]ir.DAGRunRef, error) {
	return nil, nil
}

type failingManualCASStore struct {
	testutil.DAGRunStoreStub
	base *persis.DAGRunRepository
	err  error
}

func (s *failingManualCASStore) CompareAndSwapLatestAttemptStatus(
	context.Context,
	persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	return nil, false, s.err
}

func (s *failingManualCASStore) FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	return s.base.FindAttempt(ctx, ref)
}

func (s *manualCASStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
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
	a := &API{procRepository: &manualStepProcRepository{err: livenessErr}}
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
	a := &API{procRepository: &manualStepProcRepository{alive: true}}
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
	a := &API{procRepository: &manualStepProcRepository{}}

	updated, err := a.waitForManualStepMutationReady(t.Context(), attempt, status)

	require.NoError(t, err)
	assert.Same(t, &finalized, updated)
	assert.Equal(t, 2, attempt.reads)
}

func TestApproveDAGRunStepReturnsInternalErrorWhenStatusWriteFails(t *testing.T) {
	ctx := t.Context()
	persistedRepository := testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	dag := &ir.DAG{
		Name: "approval-write-failure",
		Steps: []ir.Step{{
			Name:     "approve",
			Approval: &ir.ApprovalConfig{Prompt: "Approve"},
		}},
	}
	attempt, err := persistedRepository.CreateAttempt(ctx, dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
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
	repository := persis.NewDAGRunRepository(failingStore, nil, persis.DAGRunRepositoryOptions{})
	cfg := &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
		config.PermissionRunDAGs: true,
	}}}
	a := &API{
		dagRunRepository: repository,
		dagRunMgr:        runtimepkg.NewManager(repository, nil, cfg),
		procRepository:   &manualStepProcRepository{},
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

// Push-back inputs become environment variables of every rewound step, so an
// oversized value is rejected before the push-back is stored. Undeclared keys
// that the allowlist drops do not count.
func TestValidatePushBackInputsRejectsOversizedInputs(t *testing.T) {
	t.Parallel()

	step := ir.Step{Name: "review", Approval: &ir.ApprovalConfig{Input: []string{"FEEDBACK"}}}
	large := strings.Repeat("x", dagrun.MaxPushBackInputsSize)

	err := validatePushBackInputs(step, &openapiv1.PushBackStepRequest{Inputs: &map[string]string{"FEEDBACK": large}})
	require.ErrorContains(t, err, "maximum size")
	require.NoError(t, validatePushBackInputs(step, &openapiv1.PushBackStepRequest{
		Inputs: &map[string]string{"FEEDBACK": "tighten", "IGNORED": large},
	}))
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

func TestApplyInlineEnqueueLabels_PreservesDocAfterEmptyDocument(t *testing.T) {
	t.Parallel()

	data := []byte(`name: main
steps:
  - name: s1
    run: echo hi
---
---
name: child
steps:
  - name: s2
    run: echo bye
`)

	patched, err := applyInlineEnqueueLabels(data, "env=prod")
	require.NoError(t, err)

	content := string(patched)
	assert.Contains(t, content, "name: child")
	assert.Contains(t, content, "echo bye")
}

func TestApplyInlineEnqueueLabels_LeadingEmptyDocument(t *testing.T) {
	t.Parallel()

	// A leading empty document must not hide the first real document: the
	// label merge reads the same stream the parser sees.
	data := []byte(`---
---
name: main
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
	assert.Contains(t, string(patched), "name: main")
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

func TestDAGRunListOptionsFromQueryStringRejectsInvalidFilters(t *testing.T) {
	t.Parallel()

	api := &API{}
	cases := []struct {
		name        string
		queryString string
		wantMessage string
	}{
		{name: "fromDate", queryString: "fromDate=not-a-timestamp", wantMessage: "invalid fromDate parameter"},
		{name: "toDate", queryString: "toDate=not-a-timestamp", wantMessage: "invalid toDate parameter"},
		{name: "limit", queryString: "limit=many", wantMessage: "invalid limit parameter"},
		{name: "malformed query", queryString: "fromDate=%zz", wantMessage: "invalid query parameters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := api.dagRunListOptionsFromQueryString(context.Background(), tc.queryString)
			require.Error(t, err)

			apiErr, ok := err.(*Error)
			require.True(t, ok)
			require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
			require.Equal(t, openapiv1.ErrorCodeBadRequest, apiErr.Code)
			require.Contains(t, apiErr.Message, tc.wantMessage)
		})
	}
}

func TestBuildDAGListOptionsRejectsMalformedQuery(t *testing.T) {
	t.Parallel()

	api := &API{}
	_, err := api.buildDAGListOptions(context.Background(), "name=%zz")
	require.Error(t, err)

	apiErr, ok := err.(*Error)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	require.Equal(t, openapiv1.ErrorCodeBadRequest, apiErr.Code)
	require.Contains(t, apiErr.Message, "invalid query parameters")
}

func TestGetDAGRunsListDataReturnsBadRequestForInvalidParams(t *testing.T) {
	t.Parallel()

	api := &API{}
	_, err := api.GetDAGRunsListData(context.Background(), "fromDate=not-a-timestamp")
	require.Error(t, err)

	apiErr, ok := err.(*Error)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	require.Equal(t, openapiv1.ErrorCodeBadRequest, apiErr.Code)
}

func TestDAGRunListOptionsFromQueryStringParsesDateRange(t *testing.T) {
	t.Parallel()

	api := &API{}
	opts, err := api.dagRunListOptionsFromQueryString(
		context.Background(),
		"fromDate=1700000000&toDate=1700086400&limit=25",
	)
	require.NoError(t, err)

	applied := statusQueryFromOptions(t, opts.query)

	require.Equal(t, time.Unix(1700000000, 0).UTC(), applied.From.Time)
	require.Equal(t, time.Unix(1700086400, 0).UTC(), applied.To.Time)
	require.Equal(t, 25, applied.Limit)
}

type blockingDAGRunStore struct {
	testutil.DAGRunStoreStub
}

type queryCapturingDAGRunStore struct {
	testutil.DAGRunStoreStub
	query persis.DAGRunStatusQuery
}

func (b *queryCapturingDAGRunStore) QueryStatuses(
	_ context.Context,
	query persis.DAGRunStatusQuery,
) (persis.DAGRunStatusPage, error) {
	b.query = query
	return persis.DAGRunStatusPage{}, nil
}

func statusQueryFromOptions(t *testing.T, options persis.DAGRunListOptions) persis.DAGRunStatusQuery {
	t.Helper()
	backend := &queryCapturingDAGRunStore{}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
	_, err := repository.ListStatuses(context.Background(), options)
	require.NoError(t, err)
	return backend.query
}

func (blockingDAGRunStore) QueryStatuses(ctx context.Context, _ persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	<-ctx.Done()
	return persis.DAGRunStatusPage{}, ctx.Err()
}

func TestAPIListDAGRunsReturnsGatewayTimeoutWhenReadDeadlineExpires(t *testing.T) {
	t.Parallel()

	api := &API{
		dagRunRepository: persis.NewDAGRunRepository(blockingDAGRunStore{}, nil, persis.DAGRunRepositoryOptions{}),
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
	testutil.DAGRunStoreStub
}

func (blockingLatestAttemptStore) LatestAttempt(ctx context.Context, _ persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
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
		dagRunRepository: persis.NewDAGRunRepository(blockingLatestAttemptStore{}, nil, persis.DAGRunRepositoryOptions{}),
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

func TestSubDAGRunDataRequiresRootAndChildWorkspaceAccess(t *testing.T) {
	ctx := t.Context()
	repository := testutil.NewFileDAGRunRepository(t.TempDir(), persis.DAGRunRepositoryOptions{})
	rootDAG := &ir.DAG{Name: "root", Labels: ir.NewLabels([]string{"workspace=ops"})}
	rootRef := ir.NewDAGRunRef(rootDAG.Name, "root-run")
	rootAttempt, err := repository.CreateAttempt(ctx, rootDAG, time.Now(), rootRef.ID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, rootAttempt.Open(ctx))
	rootStatus := ir.InitialStatus(rootDAG)
	rootStatus.DAGRunID = rootRef.ID
	rootStatus.AttemptID = rootAttempt.ID()
	require.NoError(t, rootAttempt.Write(ctx, rootStatus))
	require.NoError(t, rootAttempt.Close(ctx))

	childDAG := &ir.DAG{Name: "child", Labels: ir.NewLabels([]string{"workspace=secret"})}
	childAttempt, err := repository.CreateAttempt(ctx, childDAG, time.Now(), "child-run", persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
	})
	require.NoError(t, err)
	require.NoError(t, childAttempt.Open(ctx))
	childStatus := ir.InitialStatus(childDAG)
	childStatus.Root = rootRef
	childStatus.DAGRunID = "child-run"
	childStatus.AttemptID = childAttempt.ID()
	childStatus.Nodes = []*ir.Node{{Step: ir.Step{Name: "main"}}}
	require.NoError(t, childAttempt.Write(ctx, childStatus))
	require.NoError(t, childAttempt.Close(ctx))

	cfg := &config.Config{}
	a := &API{
		authService:      struct{ AuthService }{},
		dagRunRepository: repository,
		dagRunMgr:        runtimepkg.NewManager(repository, nil, cfg),
		config:           cfg,
	}
	ctx = auth.WithUser(ctx, &auth.User{
		Role: auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "ops", Role: auth.RoleViewer},
		}},
	})

	_, err = a.GetSubDAGRunDetailsData(ctx, "root/root-run/child-run")

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusNotFound, apiErr.HTTPStatus)

	ctx = auth.WithUser(t.Context(), &auth.User{
		Role: auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "secret", Role: auth.RoleViewer},
		}},
	})

	_, err = a.GetSubStepLogDataByRef(ctx, rootRef, "child-run", "main", StepLogReadOptions{})

	apiErr = nil
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusNotFound, apiErr.HTTPStatus)
}

func TestStepLogLimitWithoutOffsetReadsFromBeginning(t *testing.T) {
	stdoutPath := filepath.Join(t.TempDir(), "stdout.log")
	require.NoError(t, os.WriteFile(stdoutPath, []byte("one\ntwo\nthree\n"), 0o600))
	status := &ir.DAGRunStatus{
		Name:  "test",
		Nodes: []*ir.Node{{Step: ir.Step{Name: "main"}, Stdout: stdoutPath}},
	}

	result, err := (&API{}).stepLogFromStatus(t.Context(), status, "main", StepLogReadOptions{Limit: 2})

	require.NoError(t, err)
	require.Equal(t, "one\ntwo", result.StdoutContent)
	require.Equal(t, 2, result.LineCount)
	require.True(t, result.HasMore)
}

// agentResumeFixture is a saved run whose agent step waits on a question while
// an independent approval gate is unresolved.
type agentResumeFixture struct {
	api        *API
	root       ir.DAGRunRef
	subRunID   string
	status     *ir.DAGRunStatus
	queueStore *store.QueueStore
	recorder   *retryCoordinatorRecorder
}

func newAgentResumeFixture(t *testing.T, child, queued bool) *agentResumeFixture {
	t.Helper()
	root := ir.NewDAGRunRef("manual-dag", "run-1")
	status := &ir.DAGRunStatus{
		Name: root.Name, DAGRunID: root.ID, AttemptID: "attempt-1", Status: ir.Waiting,
		FinishedAt: time.Now().Format(time.RFC3339),
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "agent"}, Status: ir.NodeWaiting, AgentSession: &ir.AgentSession{
				Provider: computerhost.AgentProvider, Generation: 1, State: ir.AgentSessionWaiting,
				Interactions: []ir.AgentInteraction{{ID: "ask-1", Kind: ir.AgentInteractionQuestion, Status: ir.AgentInteractionPending,
					Questions: []ir.AgentQuestion{{Question: "Continue?", Custom: true}},
				}},
			}},
			{Step: ir.Step{Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeWaiting},
		},
	}
	subRunID := ""
	if child {
		subRunID = "child-1"
		status.Name, status.DAGRunID = "child", subRunID
		status.Root, status.Parent = root, root
	}
	backend := &manualCASStore{status: status, attempt: &manualStepAttempt{
		dag:      &ir.DAG{Name: status.Name, BaseConfigWorkspace: new(""), WorkerSelector: map[string]string{"region": "apac"}},
		statuses: []*ir.DAGRunStatus{status},
	}}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}}
	if queued {
		cfg.Queues = config.Queues{Enabled: true, Config: []config.QueueConfig{{Name: status.Name, MaxActiveRuns: 1}}}
	}
	queueStore := store.NewQueueStore(file.NewCollection(t.TempDir()))
	recorder := &retryCoordinatorRecorder{}
	a := &API{
		config: cfg, dagRunRepository: repository, procRepository: &manualStepProcRepository{}, queueStore: queueStore,
		dagRunMgr: runtimepkg.NewManager(repository, nil, cfg), coordinatorCli: recorder,
	}
	require.NoError(t, computerhost.NewStore(filepath.Join(cfg.Paths.DataDir, computerhost.DataDirName)).Save(computerhost.Record{
		DAGRunID: root.ID, StepName: "agent", Deadline: time.Now().Add(time.Hour),
	}))
	return &agentResumeFixture{api: a, root: root, subRunID: subRunID, status: status, queueStore: queueStore, recorder: recorder}
}

// Root interaction responses and restarts resume their saved session state;
// child responses keep waiting while an independent approval is unresolved.
func TestAgentResumeQueue(t *testing.T) {
	for _, tt := range []struct {
		name    string
		child   bool
		restart bool
		direct  bool
	}{
		{name: "Response"},
		{name: "Restart", restart: true},
		{name: "ChildResponse", child: true},
		{name: "DirectResponse", direct: true},
		{name: "DirectRestart", direct: true, restart: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAgentResumeFixture(t, tt.child, !tt.direct)
			status := f.status

			if tt.restart {
				response, err := f.api.restartAgentSession(t.Context(), f.root, "", "agent")
				require.NoError(t, err)
				require.True(t, response.Resumed)
				assert.Equal(t, 2, response.Generation)
			} else {
				answers := [][]string{{"yes"}}
				response, err := f.api.respondAgentInteraction(t.Context(), f.root, f.subRunID, "agent", "ask-1", &openapiv1.AgentInteractionResponseRequest{Answers: &answers})
				require.NoError(t, err)
				assert.Equal(t, !tt.child, response.Resumed)
				assert.Equal(t, answers, status.Nodes[0].AgentSession.Interactions[0].Answers)
			}
			assert.Equal(t, ir.NodeNotStarted, status.Nodes[0].Status)
			assert.Equal(t, ir.NodeWaiting, status.Nodes[1].Status)
			items, err := f.queueStore.List(t.Context(), status.Name)
			require.NoError(t, err)
			if tt.child {
				assert.Equal(t, ir.Waiting, status.Status)
				assert.Empty(t, items)
				assert.Empty(t, f.recorder.dispatched)
			} else if tt.direct {
				assert.Equal(t, ir.Queued, status.Status)
				assert.Empty(t, items)
				require.Len(t, f.recorder.dispatched, 1)
				assert.Equal(t, ir.Queued, f.recorder.dispatched[0].PreviousStatus.Status)
			} else {
				assert.Empty(t, f.recorder.dispatched)
				assert.Equal(t, ir.Queued, status.Status)
				require.Len(t, items, 1)
				ref, err := items[0].Data()
				require.NoError(t, err)
				assert.Equal(t, f.root, *ref)
			}
		})
	}
}

// Agent actions on a run that is still executing must leave it untouched: a
// resume would start a second execution beside the running sibling step.
func TestAgentActionWhileRunning(t *testing.T) {
	for _, tt := range []struct {
		name    string
		child   bool
		restart bool
	}{
		{name: "Response"},
		{name: "Restart", restart: true},
		{name: "ChildResponse", child: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAgentResumeFixture(t, tt.child, false)
			f.status.Status, f.status.FinishedAt = ir.Running, ""
			f.status.Nodes = append(f.status.Nodes, &ir.Node{Step: ir.Step{Name: "sibling"}, Status: ir.NodeRunning})

			var err error
			if tt.restart {
				_, err = f.api.restartAgentSession(t.Context(), f.root, "", "agent")
			} else {
				answers := [][]string{{"yes"}}
				_, err = f.api.respondAgentInteraction(t.Context(), f.root, f.subRunID, "agent", "ask-1", &openapiv1.AgentInteractionResponseRequest{Answers: &answers})
			}

			require.Error(t, err)
			assert.Equal(t, agentSessionActionConflict, classifyAgentSessionAction(err))
			assert.Equal(t, ir.Running, f.status.Status)
			assert.Equal(t, ir.NodeWaiting, f.status.Nodes[0].Status)
			assert.Equal(t, ir.AgentInteractionPending, f.status.Nodes[0].AgentSession.Interactions[0].Status)
			assert.Empty(t, f.recorder.dispatched)
		})
	}
}

func TestApplyAgentInteractionResponse(t *testing.T) {
	t.Parallel()

	node := &ir.Node{
		Status: ir.NodeWaiting,
		AgentSession: &ir.AgentSession{
			Provider: "opencode",
			State:    ir.AgentSessionWaiting,
			Interactions: []ir.AgentInteraction{{
				ID: "permission-1", Kind: ir.AgentInteractionPermission, Status: ir.AgentInteractionPending,
				AllowForSessionPatterns: []string{"git status *"},
			}},
		},
	}
	decision := openapiv1.AgentInteractionResponseRequestDecision("session")
	ctx := auth.WithUser(t.Context(), &auth.User{ID: "user-1", Username: "alice"})

	err := applyAgentInteractionResponse(ctx, node, "permission-1", &openapiv1.AgentInteractionResponseRequest{Decision: &decision})

	require.NoError(t, err)
	assert.Equal(t, ir.NodeNotStarted, node.Status)
	interaction := node.AgentSession.Interactions[0]
	assert.Equal(t, ir.AgentInteractionAnswered, interaction.Status)
	assert.Equal(t, "session", interaction.Decision)
	assert.Equal(t, "alice", interaction.RespondedBy)
	assert.Equal(t, "user-1", interaction.RespondedByID)
}

func TestApplyAgentQuestionResponseTrimsAnswers(t *testing.T) {
	t.Parallel()

	node := &ir.Node{
		Status: ir.NodeWaiting,
		AgentSession: &ir.AgentSession{State: ir.AgentSessionWaiting, Interactions: []ir.AgentInteraction{{
			ID: "question-1", Kind: ir.AgentInteractionQuestion, Status: ir.AgentInteractionPending,
			Questions: []ir.AgentQuestion{{Options: []ir.AgentQuestionOption{{Label: "A"}}}},
		}}},
	}
	answers := [][]string{{" A "}}
	require.NoError(t, applyAgentInteractionResponse(t.Context(), node, "question-1", &openapiv1.AgentInteractionResponseRequest{Answers: &answers}))
	assert.Equal(t, [][]string{{"A"}}, node.AgentSession.Interactions[0].Answers)
}

func TestApplyAgentSessionRestart(t *testing.T) {
	t.Parallel()

	node := &ir.Node{
		Status:       ir.NodeWaiting,
		ChatMessages: []ir.LLMMessage{{Role: ir.LLMRoleAssistant, Content: "old"}},
		AgentSession: &ir.AgentSession{
			Provider: "opencode", SessionID: "session-old", Generation: 2,
			OwnerWorkerID: "worker-a", State: ir.AgentSessionUnavailable, PromptSent: true, SessionOwned: true,
			PromptMessageID: "message-old", Usage: ir.AgentUsage{TotalTokens: 100},
			Interactions:     []ir.AgentInteraction{{ID: "old"}},
			PermissionGrants: []ir.AgentPermissionGrant{{Permission: "bash", Patterns: []string{"git *"}}},
		},
	}

	err := applyAgentSessionRestart(node)

	require.NoError(t, err)
	assert.Equal(t, ir.NodeNotStarted, node.Status)
	assert.Equal(t, 3, node.AgentSession.Generation)
	assert.Empty(t, node.AgentSession.SessionID)
	assert.Equal(t, "session-old", node.AgentSession.DiscardedSessionID)
	assert.True(t, node.AgentSession.DiscardedOwned)
	assert.Empty(t, node.AgentSession.OwnerWorkerID)
	assert.False(t, node.AgentSession.PromptSent)
	assert.Empty(t, node.AgentSession.PromptMessageID)
	assert.True(t, node.AgentSession.RestartPending)
	assert.Empty(t, node.AgentSession.Interactions)
	assert.Empty(t, node.AgentSession.PermissionGrants)
	assert.Zero(t, node.AgentSession.Usage)
	assert.Empty(t, node.ChatMessages)
}

func TestApplyAgentSessionRestartAfterCompletion(t *testing.T) {
	t.Parallel()

	node := &ir.Node{
		Status: ir.NodeSucceeded,
		AgentSession: &ir.AgentSession{
			Provider: "opencode", SessionID: "session-old", Generation: 1,
			State: ir.AgentSessionSucceeded, SessionOwned: true,
		},
	}

	require.NoError(t, applyAgentSessionRestart(node))
	assert.Equal(t, ir.NodeNotStarted, node.Status)
	assert.True(t, node.AgentSession.RestartPending)
	assert.Equal(t, "session-old", node.AgentSession.DiscardedSessionID)
}

func TestValidateAgentQuestionResponse(t *testing.T) {
	t.Parallel()

	interaction := ir.AgentInteraction{
		Kind: ir.AgentInteractionQuestion,
		Questions: []ir.AgentQuestion{
			{Multiple: true, Custom: true, Options: []ir.AgentQuestionOption{{Label: "A"}, {Label: "B"}}},
			{Options: []ir.AgentQuestionOption{{Label: "Only"}}},
		},
	}
	valid := [][]string{{"A", "custom"}, {"Only"}}
	require.NoError(t, validateAgentInteractionResponse(interaction, &openapiv1.AgentInteractionResponseRequest{Answers: &valid}))

	invalid := [][]string{{"A"}, {"Other"}}
	err := validateAgentInteractionResponse(interaction, &openapiv1.AgentInteractionResponseRequest{Answers: &invalid})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an offered option")

	tests := []struct {
		name        string
		interaction ir.AgentInteraction
		body        *openapiv1.AgentInteractionResponseRequest
	}{
		{name: "nil body", interaction: interaction},
		{
			name:        "session permission without scope",
			interaction: ir.AgentInteraction{Kind: ir.AgentInteractionPermission},
			body: func() *openapiv1.AgentInteractionResponseRequest {
				decision := openapiv1.AgentInteractionResponseRequestDecisionSession
				return &openapiv1.AgentInteractionResponseRequest{Decision: &decision}
			}(),
		},
		{
			name:        "unsupported permission decision",
			interaction: ir.AgentInteraction{Kind: ir.AgentInteractionPermission},
			body: func() *openapiv1.AgentInteractionResponseRequest {
				decision := openapiv1.AgentInteractionResponseRequestDecision("always")
				return &openapiv1.AgentInteractionResponseRequest{Decision: &decision}
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateAgentInteractionResponse(test.interaction, test.body))
		})
	}
}

func TestApplyAgentInteractionRejectsPermission(t *testing.T) {
	t.Parallel()

	node := &ir.Node{Status: ir.NodeWaiting, AgentSession: &ir.AgentSession{
		State: ir.AgentSessionWaiting,
		Interactions: []ir.AgentInteraction{{
			ID: "permission-1", Kind: ir.AgentInteractionPermission, Status: ir.AgentInteractionPending,
		}},
	}}
	decision := openapiv1.AgentInteractionResponseRequestDecisionReject
	require.NoError(t, applyAgentInteractionResponse(t.Context(), node, "permission-1", &openapiv1.AgentInteractionResponseRequest{Decision: &decision}))
	assert.Equal(t, ir.AgentInteractionRejected, node.AgentSession.Interactions[0].Status)
}

func TestApplyAgentSessionRestartBrowser(t *testing.T) {
	t.Parallel()

	node := &ir.Node{
		Status: ir.NodeWaiting,
		AgentSession: &ir.AgentSession{
			Provider: browserhost.AgentProvider, Generation: 1, State: ir.AgentSessionUnavailable,
			PromptSent:   true,
			Interactions: []ir.AgentInteraction{{ID: "ask-1-1"}},
		},
	}

	require.NoError(t, applyAgentSessionRestart(node))
	assert.Equal(t, ir.NodeNotStarted, node.Status)
	assert.Equal(t, 2, node.AgentSession.Generation)
	assert.Empty(t, node.AgentSession.Interactions)
	last := node.AgentSession.Events[len(node.AgentSession.Events)-1]
	assert.Equal(t, "Starting a clean browser session", last.Content)
}

// A waiting browser step can be answered only while its browser is open,
// unexpired, and reachable. A browser that answers with an error is neither
// open nor known to be gone.
func TestBrowserSessionWaiting(t *testing.T) {
	t.Parallel()

	devtools := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1/devtools/browser/x"}`)
	}))
	t.Cleanup(devtools.Close)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(failing.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	now := time.Now()
	for _, test := range []struct {
		name    string
		record  *browserhost.Record
		want    bool
		wantErr bool
	}{
		{name: "open", record: &browserhost.Record{State: browserhost.StateDetached, Deadline: now.Add(time.Hour), CDPURL: devtools.URL}, want: true},
		{name: "no record"},
		{name: "expired", record: &browserhost.Record{State: browserhost.StateDetached, Deadline: now.Add(-time.Minute), CDPURL: devtools.URL}},
		{name: "unreachable", record: &browserhost.Record{State: browserhost.StateDetached, Deadline: now.Add(time.Hour), CDPURL: closed.URL}},
		{name: "running", record: &browserhost.Record{State: browserhost.StateRunning, Deadline: now.Add(time.Hour), CDPURL: devtools.URL}},
		{name: "unknown", record: &browserhost.Record{State: browserhost.StateDetached, Deadline: now.Add(time.Hour), CDPURL: failing.URL}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			if test.record != nil {
				record := *test.record
				record.ID = browserhost.RecordID("run-1", "login")
				require.NoError(t, browserhost.NewStore(filepath.Join(dataDir, browserhost.DataDirName)).Save(record))
			}
			waiting, err := browserSessionWaiting(t.Context(), dataDir, "run-1", "login", now)
			assert.Equal(t, test.want, waiting)
			assert.Equal(t, test.wantErr, err != nil, "error: %v", err)
		})
	}
}

func TestApplyAgentSessionRestartComputer(t *testing.T) {
	t.Parallel()

	node := &ir.Node{
		Status: ir.NodeWaiting,
		AgentSession: &ir.AgentSession{
			Provider: computerhost.AgentProvider, Generation: 1, State: ir.AgentSessionUnavailable,
			Interactions: []ir.AgentInteraction{{ID: "ask-1-1"}},
		},
	}

	require.NoError(t, applyAgentSessionRestart(node))
	assert.Equal(t, ir.NodeNotStarted, node.Status)
	assert.Equal(t, 2, node.AgentSession.Generation)
	assert.Empty(t, node.AgentSession.Interactions)
}

// A paused computer step can be answered until its ask deadline.
func TestComputerSessionWaiting(t *testing.T) {
	t.Parallel()

	now := time.Now()
	for _, test := range []struct {
		name     string
		deadline *time.Time
		want     bool
	}{
		{name: "waiting", deadline: new(now.Add(time.Hour)), want: true},
		{name: "no record"},
		{name: "expired", deadline: new(now.Add(-time.Minute))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			if test.deadline != nil {
				record := computerhost.Record{DAGRunID: "run-1", StepName: "post", Deadline: *test.deadline}
				require.NoError(t, computerhost.NewStore(filepath.Join(dataDir, computerhost.DataDirName)).Save(record))
			}
			waiting, err := computerSessionWaiting(dataDir, "run-1", "post", now)
			require.NoError(t, err)
			assert.Equal(t, test.want, waiting)
		})
	}
}

func TestApplyAgentSessionRestartGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		node *ir.Node
	}{
		{
			name: "running step",
			node: &ir.Node{Status: ir.NodeRunning, AgentSession: &ir.AgentSession{Provider: "opencode"}},
		},
		{
			name: "unsupported provider",
			node: &ir.Node{Status: ir.NodeWaiting, AgentSession: &ir.AgentSession{Provider: "other"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, applyAgentSessionRestart(test.node))
		})
	}
}

func TestAppendAgentAPIEventCapsHistory(t *testing.T) {
	t.Parallel()

	session := &ir.AgentSession{Generation: 1}
	for range maxAgentSessionAPIEvents + 1 {
		appendAgentAPIEvent(session, "lifecycle", "running", "Working")
	}

	require.Len(t, session.Events, maxAgentSessionAPIEvents)
	assert.Equal(t, int64(2), session.Events[0].Sequence)
	assert.Equal(t, int64(maxAgentSessionAPIEvents+1), session.Events[len(session.Events)-1].Sequence)
}
