// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseJSONInputPreservesNumbersAndRejectsDuplicateMembers(t *testing.T) {
	input, err := ParseJSONInput([]byte(`{"count":9007199254740993,"nested":{"enabled":true}}`))
	require.NoError(t, err)
	assert.Equal(t, json.Number("9007199254740993"), input.Values["count"])

	_, err = ParseJSONInput([]byte(`{"nested":{"enabled":true,"enabled":false}}`))
	require.Error(t, err)
	assert.Equal(t, ErrorInvalid, KindOf(err))
	assert.ErrorContains(t, err, `duplicate JSON member "enabled"`)
}

func TestParseJSONInputLimitsNestingDepth(t *testing.T) {
	nestedInput := func(depth int) []byte {
		arrays := depth - 1
		return []byte(`{"value":` + strings.Repeat(`[`, arrays) + `null` +
			strings.Repeat(`]`, arrays) + `}`)
	}

	_, err := ParseJSONInput(nestedInput(maxJSONNestingDepth))
	require.NoError(t, err)

	_, err = ParseJSONInput(nestedInput(maxJSONNestingDepth + 1))
	require.Error(t, err)
	assert.Equal(t, ErrorInvalid, KindOf(err))
	assert.ErrorContains(t, err, "JSON nesting depth exceeds")
}

func TestCompletePersistsTypedInputAndQueuesResume(t *testing.T) {
	fixture := newServiceFixture(t, json.RawMessage(`{
  "type":"object",
  "properties":{
    "count":{"type":"integer"},
    "region":{"type":"string","default":"us"}
  },
  "required":["count"],
  "additionalProperties":false
}`))
	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName:       fixture.dag.Name,
		DAGRunID:      fixture.status.DAGRunID,
		StepID:        "review",
		CompletedBy:   "alice",
		CompletedByID: "user-1",
		Input: Input{Values: map[string]any{
			"count": json.Number("3"),
		}},
	})
	require.NoError(t, err)
	assert.True(t, result.Queued)
	assert.False(t, result.AlreadyCompleted)
	assert.Zero(t, result.RemainingWaitingSteps)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Equal(t, []ir.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)
	assert.Equal(t, ir.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.JSONEq(t, `{"count":3,"region":"us"}`, string(fixture.status.Nodes[0].HumanTaskInput))
	assert.Equal(t, "alice", fixture.status.Nodes[0].HumanTaskCompletedBy)
	assert.Equal(t, "user-1", fixture.status.Nodes[0].HumanTaskCompletedByID)
	require.NotNil(t, fixture.status.Nodes[0].StepOutputsValue)
	assert.JSONEq(t, `{"count":"3","region":"us"}`, *fixture.status.Nodes[0].StepOutputsValue)
	result, err = fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName:       fixture.dag.Name,
		DAGRunID:      fixture.status.DAGRunID,
		StepID:        "review",
		CompletedBy:   "bob",
		CompletedByID: "user-2",
		Input: Input{Values: map[string]any{
			"count": json.Number("3"),
		}},
	})
	require.NoError(t, err)
	assert.True(t, result.AlreadyCompleted)
	assert.False(t, result.Queued)
	assert.Len(t, fixture.queue.enqueued, 1)
	assert.Equal(t, "alice", fixture.status.Nodes[0].HumanTaskCompletedBy)
	assert.Equal(t, "user-1", fixture.status.Nodes[0].HumanTaskCompletedByID)
}

func TestCompleteKeepsCheckpointRecoverableWhenEnqueueFails(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	queueErr := errors.New("queue unavailable")
	fixture.queue.enqueueErrors = []error{queueErr}

	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})
	require.Error(t, err)
	var resumeErr *ResumeError
	require.ErrorAs(t, err, &resumeErr)
	assert.ErrorIs(t, err, queueErr)
	assert.Equal(t, result, resumeErr.Result)
	assert.False(t, result.Queued)
	assert.Equal(t, ir.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.Equal(t, ir.Waiting, fixture.status.Status)
	assert.True(t, ResumePending(fixture.status))

	result, err = fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
	require.NoError(t, err)
	assert.True(t, result.Queued)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Equal(t, []ir.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)
}

func TestResumeBoundsStatusVerificationAfterQueueFailure(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.Nodes[0].Status = ir.NodeSucceeded
	fixture.status.Nodes[0].HumanTaskInput = json.RawMessage(`{}`)
	fixture.queue.enqueueErrors = []error{errors.New("queue unavailable")}
	fixture.service.EnqueueTimeout = 10 * time.Millisecond

	findCalls := 0
	fixture.backend.findAttempt = func(ctx context.Context, _ ir.DAGRunRef) (dagrun.Attempt, error) {
		findCalls++
		if findCalls == 1 {
			return fixture.backend.attempt, nil
		}
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("status verification context has no deadline")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	result, err := fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)

	require.Error(t, err)
	assert.Equal(t, ErrorInternal, KindOf(err))
	assert.ErrorContains(t, err, "failed to verify DAG-run status after queue failure")
	assert.ErrorContains(t, err, context.DeadlineExceeded.Error())
	assert.False(t, result.Queued)
	assert.True(t, ResumePending(fixture.status))
}

func TestCompleteDoesNotReportRetryableWhenQueueRollbackFails(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.queue.enqueueErrors = []error{errors.New("queue unavailable")}
	fixture.backend.compareAndSwapErrors = []error{nil, nil, errors.New("rollback unavailable")}

	_, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})

	require.Error(t, err)
	var resumeErr *ResumeError
	assert.NotErrorAs(t, err, &resumeErr)
	assert.Equal(t, ErrorInternal, KindOf(err))
	assert.Equal(t, ir.Queued, fixture.status.Status)
}

func TestCompleteAcceptsConcurrentRetryDispatch(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	compareAndSwapCalls := 0
	fixture.backend.beforeCompareAndSwap = func() {
		compareAndSwapCalls++
		if compareAndSwapCalls == 2 {
			fixture.status.Status = ir.Running
			fixture.status.AttemptID = "attempt-2"
			fixture.status.AttemptKey = "key-2"
		}
	}

	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})

	require.NoError(t, err)
	assert.False(t, result.Queued)
	assert.Equal(t, ir.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.Empty(t, fixture.queue.enqueued)
}

func TestCompleteClassifiesDAGRunLookupErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind ErrorKind
	}{
		{name: "missing run", err: dagrun.ErrDAGRunIDNotFound, kind: ErrorNotFound},
		{name: "storage failure", err: errors.New("storage unavailable"), kind: ErrorInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			fixture.backend.findErr = tc.err

			_, err := fixture.service.Complete(t.Context(), CompleteRequest{
				DAGName:  fixture.dag.Name,
				DAGRunID: fixture.status.DAGRunID,
				StepID:   "review",
				Input:    Input{Values: map[string]any{}},
			})

			require.Error(t, err)
			assert.Equal(t, tc.kind, KindOf(err))
		})
	}
}

func TestResumeRejectsRunWithoutCompletedCheckpoint(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.Status = ir.Succeeded
	fixture.status.Nodes[0].Status = ir.NodeSucceeded

	_, err := fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)

	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))
	assert.ErrorContains(t, err, "has no completed human-task checkpoint")
}

func TestCompleteWaitsForEveryManualStepBeforeResuming(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.Nodes = append(fixture.status.Nodes, &ir.Node{
		Step:   ir.Step{ID: "approval", Name: "Approval", Approval: &ir.ApprovalConfig{}},
		Status: ir.NodeWaiting,
	})
	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.RemainingWaitingSteps)
	assert.False(t, result.Queued)
	assert.Empty(t, fixture.queue.enqueued)
}

func TestCompleteEnqueuesRemoteResume(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.WorkerID = "worker-a"
	fixture.status.ProcGroup = "distributed"
	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})
	require.NoError(t, err)
	assert.True(t, result.Queued)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Equal(t, []ir.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)
}

func TestValidateRetryProtectsHumanTaskCheckpoints(t *testing.T) {
	status := &ir.DAGRunStatus{
		Status: ir.Waiting,
		Nodes: []*ir.Node{{
			Step:   ir.Step{ID: "review", Name: "Review", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}},
			Status: ir.NodeWaiting,
		}},
	}

	err := ValidateRetry(status, "review")
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))

	err = ValidateRetry(status, "")
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))

	status.Status = ir.Queued
	assert.NoError(t, ValidateRetry(status, ""))

	status.Status = ir.Waiting
	status.Nodes[0].Status = ir.NodeSucceeded
	status.Nodes[0].HumanTaskInput = json.RawMessage(`{}`)
	assert.Error(t, ValidateRetry(status, ""))
}

func TestValidateRetryAllowsRunRetryWhileWaitingForApprovalAfterCompletedHumanTask(t *testing.T) {
	status := &ir.DAGRunStatus{
		Status: ir.Waiting,
		Nodes: []*ir.Node{
			{
				Step:           ir.Step{ID: "review", Name: "Review", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}},
				Status:         ir.NodeSucceeded,
				HumanTaskInput: json.RawMessage(`{}`),
			},
			{
				Step:   ir.Step{ID: "approve", Name: "Approve", Approval: &ir.ApprovalConfig{Prompt: "Approve"}},
				Status: ir.NodeWaiting,
			},
		},
	}

	assert.NoError(t, ValidateRetry(status, ""))
}

func TestWaitForCompletionReadyWaitsForLocalAttemptExit(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	procRepository := &sequenceProcRepository{alive: []bool{true, false}}
	fixture.service.ProcRepository = procRepository
	fixture.service.PollInterval = time.Millisecond
	fixture.service.SettleTimeout = time.Second

	status, err := fixture.service.waitForCompletionReady(
		t.Context(),
		fixture.backend.attempt,
		fixture.dag,
		fixture.status,
		"review",
	)
	require.NoError(t, err)
	assert.Same(t, fixture.status, status)
	assert.Equal(t, 2, procRepository.calls)
	assert.Equal(t, fixture.dag.ProcGroup(), procRepository.groupName)
	assert.Equal(t, fixture.status.DAGRun(), procRepository.dagRun)
	assert.Equal(t, fixture.status.AttemptID, procRepository.attemptID)
}

func TestWaitForCompletionReadyWaitsForRemoteCheckpointPersistence(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.WorkerID = "worker-a"
	fixture.status.FinishedAt = ""
	finalStatus := *fixture.status
	finalStatus.FinishedAt = "2026-07-21T01:02:03Z"
	attempt := &sequenceAttempt{statuses: []*ir.DAGRunStatus{&finalStatus}}
	fixture.service.PollInterval = time.Millisecond
	fixture.service.SettleTimeout = time.Second

	status, err := fixture.service.waitForCompletionReady(
		t.Context(),
		attempt,
		fixture.dag,
		fixture.status,
		"review",
	)
	require.NoError(t, err)
	assert.Equal(t, finalStatus.FinishedAt, status.FinishedAt)
	assert.Equal(t, 1, attempt.calls)
}

func TestWaitForCompletionReadyRejectsUnknownStep(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.WorkerID = "worker-a"
	fixture.status.FinishedAt = ""
	attempt := &sequenceAttempt{}

	_, err := fixture.service.waitForCompletionReady(
		t.Context(),
		attempt,
		fixture.dag,
		fixture.status,
		"missing",
	)
	require.Error(t, err)
	assert.Equal(t, ErrorNotFound, KindOf(err))
	assert.ErrorContains(t, err, `human task step ID "missing" was not found`)
	assert.Zero(t, attempt.calls)
}

type serviceFixture struct {
	dag     *ir.DAG
	status  *ir.DAGRunStatus
	backend *serviceDAGRunStore
	queue   *serviceQueueStore
	service *Service
}

func newServiceFixture(t *testing.T, form json.RawMessage) *serviceFixture {
	t.Helper()
	step := ir.Step{
		ID: "review", Name: "Review",
		HumanTask: &ir.HumanTaskConfig{Prompt: "Approve the release?", Form: form},
	}
	dag := &ir.DAG{Name: "deploy", Steps: []ir.Step{step}, MaxOutputSize: 1 << 20}
	status := &ir.DAGRunStatus{
		Name: dag.Name, DAGRunID: "run-1", AttemptID: "attempt-1", AttemptKey: "key-1",
		Status: ir.Waiting, FinishedAt: "2026-07-21T00:00:00Z",
		Nodes: []*ir.Node{{Step: step, Status: ir.NodeWaiting}},
	}
	attempt := &serviceAttempt{dag: dag, status: status}
	backend := &serviceDAGRunStore{attempt: attempt, status: status}
	queue := &serviceQueueStore{}
	now := time.Date(2026, 7, 21, 1, 2, 3, 0, time.UTC)
	return &serviceFixture{
		dag: dag, status: status, backend: backend, queue: queue,
		service: &Service{
			DAGRunRepository: persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{}),
			QueueStore:       queue,
			ProcRepository:   serviceProcRepository{},
			Now:              func() time.Time { return now },
		},
	}
}

type serviceAttempt struct {
	dagrun.Attempt
	dag    *ir.DAG
	status *ir.DAGRunStatus
}

type sequenceAttempt struct {
	dagrun.Attempt
	statuses []*ir.DAGRunStatus
	calls    int
}

func (a *sequenceAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	a.calls++
	if len(a.statuses) == 0 {
		return nil, dagrun.ErrNoStatusData
	}
	status := a.statuses[0]
	a.statuses = a.statuses[1:]
	return status, nil
}

func (a *sequenceAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}

func (a *serviceAttempt) ID() string                               { return a.status.AttemptID }
func (a *serviceAttempt) ReadDAG(context.Context) (*ir.DAG, error) { return a.dag, nil }
func (a *serviceAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	return a.status, nil
}

func (a *serviceAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}

type serviceDAGRunStore struct {
	testutil.DAGRunStoreStub
	attempt              *serviceAttempt
	status               *ir.DAGRunStatus
	findErr              error
	findAttempt          func(context.Context, ir.DAGRunRef) (dagrun.Attempt, error)
	beforeCompareAndSwap func()
	compareAndSwapErrors []error
}

func (s *serviceDAGRunStore) FindAttempt(ctx context.Context, ref ir.DAGRunRef) (dagrun.Attempt, error) {
	if s.findAttempt != nil {
		return s.findAttempt(ctx, ref)
	}
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.attempt, nil
}

func (s *serviceDAGRunStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	if len(s.compareAndSwapErrors) > 0 {
		err := s.compareAndSwapErrors[0]
		s.compareAndSwapErrors = s.compareAndSwapErrors[1:]
		if err != nil {
			return nil, false, err
		}
	}
	if s.beforeCompareAndSwap != nil {
		s.beforeCompareAndSwap()
	}
	if s.status.AttemptID != req.ExpectedAttemptID || s.status.Status != req.ExpectedStatus {
		return s.status, false, nil
	}
	if err := req.Mutate(s.status); err != nil {
		return nil, false, err
	}
	return s.status, true, nil
}

type serviceProcRepository struct{}

func (serviceProcRepository) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return false, nil
}

type sequenceProcRepository struct {
	alive     []bool
	calls     int
	groupName string
	dagRun    ir.DAGRunRef
	attemptID string
}

func (s *sequenceProcRepository) IsAttemptAlive(
	_ context.Context,
	groupName string,
	dagRun ir.DAGRunRef,
	attemptID string,
) (bool, error) {
	s.calls++
	s.groupName = groupName
	s.dagRun = dagRun
	s.attemptID = attemptID
	if len(s.alive) == 0 {
		return false, nil
	}
	alive := s.alive[0]
	s.alive = s.alive[1:]
	return alive, nil
}

type serviceQueueStore struct {
	queue.QueueStore
	enqueued      []ir.DAGRunRef
	enqueueErrors []error
}

func (s *serviceQueueStore) Enqueue(_ context.Context, _ string, _ queue.QueuePriority, ref ir.DAGRunRef) error {
	if len(s.enqueueErrors) > 0 {
		err := s.enqueueErrors[0]
		s.enqueueErrors = s.enqueueErrors[1:]
		return err
	}
	s.enqueued = append(s.enqueued, ref)
	return nil
}
