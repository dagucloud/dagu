// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
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
	assert.Equal(t, core.Queued, fixture.status.Status)
	assert.Equal(t, []exec.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)
	assert.Equal(t, core.NodeSucceeded, fixture.status.Nodes[0].Status)
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

func TestCompleteTreatsEquivalentAdditionalNumbersAsIdentical(t *testing.T) {
	fixture := newServiceFixture(t, json.RawMessage(`{
  "type":"object",
  "properties":{},
  "additionalProperties":true
}`))
	firstInput, err := ParseJSONInput([]byte(`{"score":1}`))
	require.NoError(t, err)
	secondInput, err := ParseJSONInput([]byte(`{"score":1.0}`))
	require.NoError(t, err)

	_, err = fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: firstInput,
	})
	require.NoError(t, err)
	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: secondInput,
	})

	require.NoError(t, err)
	assert.True(t, result.AlreadyCompleted)
	assert.Len(t, fixture.queue.enqueued, 1)
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
	assert.Equal(t, core.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.Equal(t, core.Waiting, fixture.status.Status)
	assert.True(t, ResumePending(fixture.status))

	result, err = fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
	require.NoError(t, err)
	assert.True(t, result.Queued)
	assert.Equal(t, core.Queued, fixture.status.Status)
	assert.Equal(t, []exec.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)
}

func TestCompleteDoesNotReportRetryableWhenQueueRollbackFails(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.queue.enqueueErrors = []error{errors.New("queue unavailable")}
	fixture.store.compareAndSwapErrors = []error{nil, nil, errors.New("rollback unavailable")}

	_, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})

	require.Error(t, err)
	var resumeErr *ResumeError
	assert.NotErrorAs(t, err, &resumeErr)
	assert.Equal(t, ErrorInternal, KindOf(err))
	assert.Equal(t, core.Queued, fixture.status.Status)
}

func TestCompleteAcceptsConcurrentRetryDispatch(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	compareAndSwapCalls := 0
	fixture.store.beforeCompareAndSwap = func() {
		compareAndSwapCalls++
		if compareAndSwapCalls == 2 {
			fixture.status.Status = core.Running
			fixture.status.AttemptID = "attempt-2"
			fixture.status.AttemptKey = "key-2"
		}
	}

	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})

	require.NoError(t, err)
	assert.False(t, result.Queued)
	assert.Equal(t, core.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.Empty(t, fixture.queue.enqueued)
}

func TestCompleteClassifiesDAGRunLookupErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind ErrorKind
	}{
		{name: "missing run", err: exec.ErrDAGRunIDNotFound, kind: ErrorNotFound},
		{name: "storage failure", err: errors.New("storage unavailable"), kind: ErrorInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			fixture.store.findErr = tc.err

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
	fixture.status.Status = core.Succeeded
	fixture.status.Nodes[0].Status = core.NodeSucceeded

	_, err := fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)

	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))
	assert.ErrorContains(t, err, "has no completed human-task checkpoint")
}

func TestCompleteWaitsForEveryManualStepBeforeResuming(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.Nodes = append(fixture.status.Nodes, &exec.Node{
		Step:   core.Step{ID: "approval", Name: "Approval", Approval: &core.ApprovalConfig{}},
		Status: core.NodeWaiting,
	})
	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.RemainingWaitingSteps)
	assert.False(t, result.Queued)
	assert.Empty(t, fixture.queue.enqueued)
}

func TestCompleteDoesNotMutateReplacementAttemptWithSameID(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.store.beforeCompareAndSwap = func() {
		fixture.status.AttemptKey = "key-2"
	}

	_, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName:  fixture.dag.Name,
		DAGRunID: fixture.status.DAGRunID,
		StepID:   "review",
		Input:    Input{Values: map[string]any{}},
	})

	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))
	assert.Equal(t, core.NodeWaiting, fixture.status.Nodes[0].Status)
	assert.Empty(t, fixture.status.Nodes[0].HumanTaskInput)
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
	assert.Equal(t, core.Queued, fixture.status.Status)
	assert.Equal(t, []exec.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)
}

func TestCompleteWaitsForRemoteDispatchBeforeEnqueue(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.WorkerID = "worker-a"
	fixture.status.ProcGroup = "distributed"
	ref := fixture.status.DAGRun()
	fixture.queue.listResults = [][]exec.QueuedItemData{{serviceQueuedItem{ref: &ref}}, nil}
	fixture.service.PollInterval = time.Millisecond

	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})

	require.NoError(t, err)
	assert.True(t, result.Queued)
	assert.Equal(t, 2, fixture.queue.listCalls)
	assert.Equal(t, []exec.DAGRunRef{ref}, fixture.queue.enqueued)
}

func TestCompleteAcceptsConcurrentRemoteResume(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.WorkerID = "worker-a"
	fixture.status.ProcGroup = "distributed"
	ref := fixture.status.DAGRun()
	fixture.queue.listResults = [][]exec.QueuedItemData{{serviceQueuedItem{ref: &ref}}}
	fixture.queue.beforeList = func() {
		fixture.status.Status = core.Queued
	}

	result, err := fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})

	require.NoError(t, err)
	assert.False(t, result.Queued)
	assert.Equal(t, core.Queued, fixture.status.Status)
	assert.Empty(t, fixture.queue.enqueued)
}

func TestValidateRetryProtectsHumanTaskCheckpoints(t *testing.T) {
	status := &exec.DAGRunStatus{
		Status: core.Waiting,
		Nodes: []*exec.Node{{
			Step:   core.Step{ID: "review", Name: "Review", HumanTask: &core.HumanTaskConfig{Prompt: "Review"}},
			Status: core.NodeWaiting,
		}},
	}

	err := ValidateRetry(status, "review")
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))

	err = ValidateRetry(status, "")
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))

	status.Status = core.Queued
	assert.NoError(t, ValidateRetry(status, ""))

	status.Status = core.Waiting
	status.Nodes[0].Status = core.NodeSucceeded
	status.Nodes[0].HumanTaskInput = json.RawMessage(`{}`)
	assert.Error(t, ValidateRetry(status, ""))
}

func TestValidateRetryAllowsRunRetryWhileWaitingForApprovalAfterCompletedHumanTask(t *testing.T) {
	status := &exec.DAGRunStatus{
		Status: core.Waiting,
		Nodes: []*exec.Node{
			{
				Step:           core.Step{ID: "review", Name: "Review", HumanTask: &core.HumanTaskConfig{Prompt: "Review"}},
				Status:         core.NodeSucceeded,
				HumanTaskInput: json.RawMessage(`{}`),
			},
			{
				Step:   core.Step{ID: "approve", Name: "Approve", Approval: &core.ApprovalConfig{Prompt: "Approve"}},
				Status: core.NodeWaiting,
			},
		},
	}

	assert.NoError(t, ValidateRetry(status, ""))
}

func TestWaitForCompletionReadyWaitsForLocalAttemptExit(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	procStore := &sequenceProcStore{alive: []bool{true, false}}
	fixture.service.ProcStore = procStore
	fixture.service.PollInterval = time.Millisecond
	fixture.service.SettleTimeout = time.Second

	status, err := fixture.service.waitForCompletionReady(
		t.Context(),
		fixture.store.attempt,
		fixture.dag,
		fixture.status,
		"review",
	)
	require.NoError(t, err)
	assert.Same(t, fixture.status, status)
	assert.Equal(t, 2, procStore.calls)
	assert.Equal(t, fixture.dag.ProcGroup(), procStore.groupName)
	assert.Equal(t, fixture.status.DAGRun(), procStore.dagRun)
	assert.Equal(t, fixture.status.AttemptID, procStore.attemptID)
}

func TestWaitForCompletionReadyWaitsForRemoteCheckpointPersistence(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.status.WorkerID = "worker-a"
	fixture.status.FinishedAt = ""
	finalStatus := *fixture.status
	finalStatus.FinishedAt = "2026-07-21T01:02:03Z"
	attempt := &sequenceAttempt{statuses: []*exec.DAGRunStatus{&finalStatus}}
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
	dag     *core.DAG
	status  *exec.DAGRunStatus
	store   *serviceDAGRunStore
	queue   *serviceQueueStore
	service *Service
}

func newServiceFixture(t *testing.T, form json.RawMessage) *serviceFixture {
	t.Helper()
	step := core.Step{
		ID: "review", Name: "Review",
		HumanTask: &core.HumanTaskConfig{Prompt: "Approve the release?", Form: form},
	}
	dag := &core.DAG{Name: "deploy", Steps: []core.Step{step}, MaxOutputSize: 1 << 20}
	status := &exec.DAGRunStatus{
		Name: dag.Name, DAGRunID: "run-1", AttemptID: "attempt-1", AttemptKey: "key-1",
		Status: core.Waiting, FinishedAt: "2026-07-21T00:00:00Z",
		Nodes: []*exec.Node{{Step: step, Status: core.NodeWaiting}},
	}
	attempt := &serviceAttempt{dag: dag, status: status}
	store := &serviceDAGRunStore{attempt: attempt, status: status}
	queue := &serviceQueueStore{}
	now := time.Date(2026, 7, 21, 1, 2, 3, 0, time.UTC)
	return &serviceFixture{
		dag: dag, status: status, store: store, queue: queue,
		service: &Service{
			DAGRunStore: store,
			QueueStore:  queue,
			ProcStore:   serviceProcStore{},
			Now:         func() time.Time { return now },
		},
	}
}

type serviceAttempt struct {
	exec.DAGRunAttempt
	dag    *core.DAG
	status *exec.DAGRunStatus
}

type sequenceAttempt struct {
	exec.DAGRunAttempt
	statuses []*exec.DAGRunStatus
	calls    int
}

func (a *sequenceAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	a.calls++
	if len(a.statuses) == 0 {
		return nil, exec.ErrNoStatusData
	}
	status := a.statuses[0]
	a.statuses = a.statuses[1:]
	return status, nil
}

func (a *serviceAttempt) ID() string                                 { return a.status.AttemptID }
func (a *serviceAttempt) ReadDAG(context.Context) (*core.DAG, error) { return a.dag, nil }
func (a *serviceAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	return a.status, nil
}

type serviceDAGRunStore struct {
	exec.DAGRunStore
	attempt              *serviceAttempt
	status               *exec.DAGRunStatus
	findErr              error
	beforeCompareAndSwap func()
	compareAndSwapErrors []error
}

func (s *serviceDAGRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.attempt, nil
}

func (s *serviceDAGRunStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	_ exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	opts ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	if len(s.compareAndSwapErrors) > 0 {
		err := s.compareAndSwapErrors[0]
		s.compareAndSwapErrors = s.compareAndSwapErrors[1:]
		if err != nil {
			return nil, false, err
		}
	}
	options := exec.NewCompareAndSwapStatusOptions(opts...)
	if s.beforeCompareAndSwap != nil {
		s.beforeCompareAndSwap()
	}
	if s.status.AttemptID != expectedAttemptID || s.status.Status != expectedStatus {
		return s.status, false, nil
	}
	if options.ExpectedAttemptKey != "" && s.status.AttemptKey != options.ExpectedAttemptKey {
		return s.status, false, nil
	}
	if err := mutate(s.status); err != nil {
		return nil, false, err
	}
	return s.status, true, nil
}

type serviceProcStore struct{ exec.ProcStore }

func (serviceProcStore) IsRunAlive(context.Context, string, exec.DAGRunRef) (bool, error) {
	return false, nil
}

func (serviceProcStore) IsAttemptAlive(context.Context, string, exec.DAGRunRef, string) (bool, error) {
	return false, nil
}

type sequenceProcStore struct {
	exec.ProcStore
	alive     []bool
	calls     int
	groupName string
	dagRun    exec.DAGRunRef
	attemptID string
}

func (s *sequenceProcStore) IsAttemptAlive(
	_ context.Context,
	groupName string,
	dagRun exec.DAGRunRef,
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
	exec.QueueStore
	enqueued      []exec.DAGRunRef
	enqueueErrors []error
	listResults   [][]exec.QueuedItemData
	listCalls     int
	beforeList    func()
}

func (s *serviceQueueStore) Enqueue(_ context.Context, _ string, _ exec.QueuePriority, ref exec.DAGRunRef) error {
	if len(s.enqueueErrors) > 0 {
		err := s.enqueueErrors[0]
		s.enqueueErrors = s.enqueueErrors[1:]
		return err
	}
	s.enqueued = append(s.enqueued, ref)
	return nil
}

func (s *serviceQueueStore) ListByDAGName(context.Context, string, string) ([]exec.QueuedItemData, error) {
	s.listCalls++
	if s.beforeList != nil {
		s.beforeList()
		s.beforeList = nil
	}
	if len(s.listResults) == 0 {
		return nil, nil
	}
	items := s.listResults[0]
	s.listResults = s.listResults[1:]
	return items, nil
}

type serviceQueuedItem struct {
	ref *exec.DAGRunRef
}

func (serviceQueuedItem) ID() string { return "item-1" }

func (item serviceQueuedItem) Data() (*exec.DAGRunRef, error) { return item.ref, nil }
