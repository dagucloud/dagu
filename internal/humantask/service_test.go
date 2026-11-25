// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package humantask

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
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
	assert.True(t, result.ResumeRequested)
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
	assert.True(t, result.ResumeRequested)
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

func TestInterruptedAdmission(t *testing.T) {
	for _, phase := range []string{"before create", "after create"} {
		for _, operation := range []string{"complete", "resume"} {
			t.Run(phase+"/"+operation, func(t *testing.T) {
				fixture, history, queueDir := newFileServiceFixture(t)
				fixture.service.QueueStore = store.NewQueueStore(&interruptedQueueCollection{
					Collection: file.NewCollection(queueDir), afterCreate: phase == "after create",
				})
				request := CompleteRequest{DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID,
					StepID: "review", CompletedBy: "alice", CompletedByID: "user-1", Input: Input{Values: map[string]any{}}}
				// Interrupt the real queue write before it can return to rollback.
				require.PanicsWithValue(t, "interrupted queue admission", func() {
					_, _ = fixture.service.Complete(t.Context(), request)
				})
				attempt, err := fixture.service.DAGRunRepository.FindAttempt(t.Context(), fixture.status.DAGRun())
				require.NoError(t, err)
				stored, err := attempt.ReadStatusUncached(t.Context())
				require.NoError(t, err)
				require.True(t, queue.ResumeAdmissionPending(stored))
				require.Equal(t, ir.NodeSucceeded, stored.Nodes[0].Status)
				completedAt, admissionID := stored.Nodes[0].FinishedAt, stored.ResumeAdmission.ID

				// Reopen both stores; recovery cannot rely on process-local memory.
				fixture.service.DAGRunRepository = testutil.NewFileDAGRunRepository(history, persis.DAGRunRepositoryOptions{})
				fixture.service.QueueStore = store.NewQueueStore(file.NewCollection(queueDir))
				var result Result
				if operation == "resume" {
					result, err = fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
				} else {
					request.CompletedBy, request.CompletedByID = "bob", "user-2"
					result, err = fixture.service.Complete(t.Context(), request)
				}
				require.NoError(t, err)
				assert.True(t, result.AlreadyCompleted)
				assert.Equal(t, phase == "before create", result.Queued)
				items, err := fixture.service.QueueStore.List(t.Context(), fixture.dag.ProcGroup())
				require.NoError(t, err)
				require.Len(t, items, 1)
				stored, err = attempt.ReadStatusUncached(t.Context())
				require.NoError(t, err)
				assert.Equal(t, admissionID, stored.ResumeAdmission.ID)
				assert.True(t, stored.ResumeAdmission.Admitted)
				assert.Equal(t, completedAt, stored.Nodes[0].FinishedAt)
				assert.Equal(t, "alice", stored.Nodes[0].HumanTaskCompletedBy)
				assert.Equal(t, "user-1", stored.Nodes[0].HumanTaskCompletedByID)
				assert.JSONEq(t, `{}`, string(stored.Nodes[0].HumanTaskInput))
				_, err = fixture.completeReview(t)
				require.NoError(t, err)
				repeated, err := fixture.service.QueueStore.List(t.Context(), fixture.dag.ProcGroup())
				require.NoError(t, err)
				require.Len(t, repeated, 1)
				assert.Equal(t, items[0].ID(), repeated[0].ID())
			})
		}
	}
}

func TestConcurrentAdmissionRecovery(t *testing.T) {
	fixture, history, queueDir := newFileServiceFixture(t)
	fixture.service.QueueStore = store.NewQueueStore(&interruptedQueueCollection{Collection: file.NewCollection(queueDir)})
	require.Panics(t, func() { _, _ = fixture.completeReview(t) })
	var wg sync.WaitGroup
	results := make(chan Result, 8)
	for i := range 8 {
		wg.Go(func() {
			service := &Service{
				DAGRunRepository: testutil.NewFileDAGRunRepository(history, persis.DAGRunRepositoryOptions{}),
				QueueStore:       store.NewQueueStore(file.NewCollection(queueDir)), ProcRepository: serviceProcRepository{},
			}
			var result Result
			var err error
			if i%2 == 0 {
				result, err = service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
			} else {
				result, err = service.Complete(t.Context(), CompleteRequest{
					DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
				})
			}
			assert.NoError(t, err)
			results <- result
		})
	}
	wg.Wait()
	close(results)
	created := 0
	for result := range results {
		if result.Queued {
			created++
		}
	}
	assert.Equal(t, 1, created)
	items, err := store.NewQueueStore(file.NewCollection(queueDir)).List(t.Context(), fixture.dag.ProcGroup())
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestAdmissionIndexFailure(t *testing.T) {
	fixture, _, queueDir := newFileServiceFixture(t)
	fixture.service.QueueStore = store.NewQueueStore(&interruptedQueueCollection{Collection: file.NewCollection(queueDir)})
	require.Panics(t, func() { _, _ = fixture.completeReview(t) })
	queueStore := store.NewQueueStore(file.NewCollection(queueDir))
	// Keep a valid older index that does not contain this admission. Failure
	// to update and invalidate it must not acknowledge an undiscoverable item.
	require.NoError(t, queueStore.Enqueue(t.Context(), fixture.dag.ProcGroup(), queue.QueuePriorityLow, ir.NewDAGRunRef("other", "other-run")))
	fixture.service.QueueStore = store.NewQueueStore(&failedQueueIndexCollection{Collection: file.NewCollection(queueDir)})
	_, err := fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
	var resumeError *ResumeError
	require.ErrorAs(t, err, &resumeError)
	require.ErrorContains(t, err, "queue index unavailable")
	attempt, err := fixture.service.DAGRunRepository.FindAttempt(t.Context(), fixture.status.DAGRun())
	require.NoError(t, err)
	status, err := attempt.ReadStatus(t.Context())
	require.NoError(t, err)
	assert.True(t, queue.ResumeAdmissionPending(status))
	page, err := queueStore.ListCursor(t.Context(), fixture.dag.ProcGroup(), "", 10)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	fixture.service.QueueStore = store.NewQueueStore(file.NewCollection(queueDir))
	result, err := fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
	require.NoError(t, err)
	assert.False(t, result.Queued, "reuse the already written item")
	page, err = fixture.service.QueueStore.ListCursor(t.Context(), fixture.dag.ProcGroup(), "", 10)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	matches := 0
	for _, item := range page.Items {
		ref, err := item.Data()
		require.NoError(t, err)
		if *ref == fixture.status.DAGRun() {
			matches++
		}
	}
	assert.Equal(t, 1, matches)
	status, err = attempt.ReadStatusUncached(t.Context())
	require.NoError(t, err)
	assert.True(t, status.ResumeAdmission.Admitted)
}

type failedQueueIndexCollection struct{ persis.Collection }

func (c *failedQueueIndexCollection) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	if strings.HasSuffix(id, "/.queue-index") {
		return errors.New("queue index unavailable")
	}
	return c.Collection.CompareAndSwap(ctx, id, expected, next)
}

func (c *failedQueueIndexCollection) CompareAndDelete(ctx context.Context, record *persis.Record) error {
	if strings.HasSuffix(record.ID, "/.queue-index") {
		return errors.New("queue index unavailable")
	}
	return c.Collection.CompareAndDelete(ctx, record)
}

func TestAdmissionProcessExit(t *testing.T) {
	const rootEnv = "DAGU_TEST_ADMISSION_ROOT"
	if root := os.Getenv(rootEnv); root != "" {
		service := &Service{
			DAGRunRepository: testutil.NewFileDAGRunRepository(filepath.Join(root, "history"), persis.DAGRunRepositoryOptions{}),
			QueueStore:       store.NewQueueStore(&exitingQueueCollection{Collection: file.NewCollection(filepath.Join(root, "queue"))}),
		}
		_, err := service.Complete(t.Context(), CompleteRequest{
			DAGName: "deploy", DAGRunID: "run-1", StepID: "review", Input: Input{Values: map[string]any{}},
		})
		t.Fatalf("queue fault did not terminate the process: %v", err)
	}
	fixture, history, queueDir := newFileServiceFixture(t)
	executable, err := os.Executable()
	require.NoError(t, err)
	// Execute this test's isolated helper process, then stop inside the actual
	// Create boundary without unwinding the service or repository locks.
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestAdmissionProcessExit$") //nolint:gosec // current test executable
	command.Env = append(os.Environ(), rootEnv+"="+filepath.Dir(history))
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	require.ErrorAs(t, err, &exitError, "%s", output)
	require.Equal(t, 73, exitError.ExitCode(), "%s", output)

	fixture.service.DAGRunRepository = testutil.NewFileDAGRunRepository(history, persis.DAGRunRepositoryOptions{})
	fixture.service.QueueStore = store.NewQueueStore(file.NewCollection(queueDir))
	fixture.service.EnqueueTimeout = 200 * time.Millisecond
	// The existing file-store lock lease lasts 30 seconds after an abrupt
	// exit. Recovery retries it normally; the test never deletes the lock.
	var result Result
	require.Eventually(t, func() bool {
		result, err = fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
		return err == nil
	}, 40*time.Second, 100*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, result.Queued)
	items, err := fixture.service.QueueStore.List(t.Context(), fixture.dag.ProcGroup())
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

type exitingQueueCollection struct{ persis.Collection }

func (*exitingQueueCollection) Create(context.Context, *persis.Record) error {
	os.Exit(73)
	return nil
}

func TestAdmissionRecoveryDoesNotReviveRun(t *testing.T) {
	for _, state := range []ir.Status{ir.Aborted, ir.Rejected, ir.Running, ir.Succeeded} {
		t.Run(state.String(), func(t *testing.T) {
			fixture, _, queueDir := newFileServiceFixture(t)
			fixture.service.QueueStore = store.NewQueueStore(&interruptedQueueCollection{Collection: file.NewCollection(queueDir)})
			require.Panics(t, func() { _, _ = fixture.completeReview(t) })
			_, swapped, err := fixture.service.DAGRunRepository.CompareAndSwapLatestAttemptStatus(
				t.Context(), fixture.status.DAGRun(), fixture.status.AttemptID, ir.Queued,
				func(status *ir.DAGRunStatus) error { status.Status = state; return nil }, persis.DAGRunCompareAndSwapOptions{},
			)
			require.NoError(t, err)
			require.True(t, swapped)
			fixture.service.QueueStore = store.NewQueueStore(file.NewCollection(queueDir))
			result, err := fixture.completeReview(t)
			require.NoError(t, err)
			assert.False(t, result.Queued)
			result, err = fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
			require.NoError(t, err)
			assert.False(t, result.Queued)
			items, err := fixture.service.QueueStore.List(t.Context(), fixture.dag.ProcGroup())
			require.NoError(t, err)
			assert.Empty(t, items)
		})
	}
}

func newFileServiceFixture(t *testing.T) (*serviceFixture, string, string) {
	t.Helper()
	return fileServiceFixture(t, newServiceFixture(t, nil))
}

func fileServiceFixture(t *testing.T, fixture *serviceFixture) (*serviceFixture, string, string) {
	t.Helper()
	root := t.TempDir()
	history, queueDir := filepath.Join(root, "history"), filepath.Join(root, "queue")
	repository := testutil.NewFileDAGRunRepository(history, persis.DAGRunRepositoryOptions{})
	attempt, err := repository.CreateAttempt(t.Context(), fixture.dag, time.Now(), fixture.status.DAGRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	fixture.status.AttemptID = attempt.ID()
	require.NoError(t, attempt.Open(t.Context()))
	require.NoError(t, attempt.Write(t.Context(), *fixture.status))
	require.NoError(t, attempt.Close(t.Context()))
	fixture.service.DAGRunRepository = repository
	fixture.service.QueueStore = store.NewQueueStore(file.NewCollection(queueDir))
	return fixture, history, queueDir
}

type interruptedQueueCollection struct {
	persis.Collection
	afterCreate bool
}

func (c *interruptedQueueCollection) Create(ctx context.Context, record *persis.Record) error {
	if c.afterCreate {
		if err := c.Collection.Create(ctx, record); err != nil {
			return err
		}
	}
	panic("interrupted queue admission")
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

// A completion resumes the run as soon as it unblocks a step, even while
// manual steps on independent branches keep waiting.
func TestCompleteResumesUnblockedBranch(t *testing.T) {
	continuedSkip := stepNode("side", ir.NodeSkipped)
	continuedSkip.Step.ContinueOn.Skipped = true
	retrySkip := stepNode("side", ir.NodeSkipped)
	retrySkip.SkippedByRetry = true
	tests := []struct {
		name  string
		nodes []*ir.Node
	}{
		{name: "human task waits", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			stepNode("after", ir.NodeNotStarted, "Review"),
		}},
		{name: "approval waits", nodes: []*ir.Node{
			waitingApprovalNode(),
			stepNode("after", ir.NodeNotStarted, "Review"),
		}},
		// Router targets continue past a skipped route.
		{name: "skipped dependency continues", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			continuedSkip,
			stepNode("after", ir.NodeNotStarted, "Review", "side"),
		}},
		{name: "dependency skipped by retry", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			retrySkip,
			stepNode("after", ir.NodeNotStarted, "Review", "side"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			fixture.status.Nodes = append(fixture.status.Nodes, tt.nodes...)

			result, err := fixture.completeReview(t)
			require.NoError(t, err)
			assert.True(t, result.Queued)
			assert.Equal(t, 1, result.RemainingWaitingSteps)
			assert.Equal(t, ir.Queued, fixture.status.Status)
			assert.Equal(t, []ir.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)
		})
	}
}

func TestCompleteKeepsWaitingWhenNoStepIsReady(t *testing.T) {
	buildConsumer := stepNode("package", ir.NodeNotStarted, "Review")
	buildConsumer.Step.Inputs = []ir.StepInputDeclaration{{Name: "binary", Path: "bin/app"}}
	tests := []struct {
		name  string
		nodes []*ir.Node
	}{
		{name: "approval waits", nodes: []*ir.Node{waitingApprovalNode()}},
		{name: "join", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			stepNode("deploy", ir.NodeNotStarted, "Review", "Other"),
		}},
		// Build inputs can add producer edges that the stored status does not record.
		{name: "build inputs", nodes: []*ir.Node{waitingHumanTaskNode("Other"), buildConsumer}},
		// Agent DAG steps declare no dependencies.
		{name: "no dependencies", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			stepNode("next", ir.NodeNotStarted),
		}},
		// The resumed attempt would only mark the step skipped.
		{name: "skipped dependency", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			stepNode("side", ir.NodeSkipped),
			stepNode("after", ir.NodeNotStarted, "Review", "side"),
		}},
		// Every resume re-runs failed steps, so they run once after the last task.
		{name: "failed step elsewhere", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			stepNode("lint", ir.NodeFailed),
			stepNode("after", ir.NodeNotStarted, "Review"),
		}},
		{name: "aborted step elsewhere", nodes: []*ir.Node{
			waitingHumanTaskNode("Other"),
			stepNode("lint", ir.NodeAborted),
			stepNode("after", ir.NodeNotStarted, "Review"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newServiceFixture(t, nil)
			fixture.status.Nodes = append(fixture.status.Nodes, tt.nodes...)

			result, err := fixture.completeReview(t)
			require.NoError(t, err)
			assert.Equal(t, 1, result.RemainingWaitingSteps)
			assert.False(t, result.ResumeRequested)
			assert.False(t, result.Queued)
			assert.Equal(t, ir.Waiting, fixture.status.Status)
			assert.Empty(t, fixture.queue.enqueued)
			assert.False(t, ResumePending(fixture.status))
		})
	}
}

// A partially succeeded branch may make intermediate progress while another
// manual task waits, then the final completion queues the remaining work.
func TestCompletePartialRetry(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	other := waitingHumanTaskNode("Other")
	fixture.status.Nodes = append(fixture.status.Nodes,
		other,
		stepNode("side", ir.NodePartiallySucceeded),
		stepNode("after", ir.NodeNotStarted, "Review", "side"),
	)

	result, err := fixture.completeReview(t)
	require.NoError(t, err)
	assert.True(t, result.ResumeRequested)
	assert.True(t, result.Queued)
	assert.Equal(t, 1, result.RemainingWaitingSteps)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Len(t, fixture.queue.enqueued, 1)

	// The checkpoint resume runs the newly unblocked branch while Other waits.
	fixture.status.Status = ir.Waiting
	fixture.status.Nodes[0].Status = ir.NodeSucceeded
	fixture.status.Nodes[0].HumanTaskInput = []byte(`{}`)

	result, err = fixture.service.Complete(t.Context(), CompleteRequest{
		DAGName: fixture.dag.Name, DAGRunID: fixture.status.DAGRunID, StepID: other.Step.ID, Input: Input{Values: map[string]any{}},
	})
	require.NoError(t, err)
	assert.False(t, result.AlreadyCompleted)
	assert.True(t, result.ResumeRequested)
	assert.True(t, result.Queued)
	assert.Zero(t, result.RemainingWaitingSteps)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Len(t, fixture.queue.enqueued, 2)
}

// The unblocked step marks a resume as pending until an attempt runs it.
func TestResumePendingUntilUnblockedStepRuns(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	after := stepNode("after", ir.NodeNotStarted, "Review")
	fixture.status.Nodes = append(fixture.status.Nodes, waitingHumanTaskNode("Other"), after)
	fixture.queue.enqueueErrors = []error{errors.New("queue unavailable")}

	_, err := fixture.completeReview(t)
	var resumeErr *ResumeError
	require.ErrorAs(t, err, &resumeErr)
	assert.True(t, ResumePending(fixture.status))

	result, err := fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
	require.NoError(t, err)
	assert.True(t, result.Queued)

	// The resumed attempt ran the branch and stopped at the other task again.
	fixture.status.Status = ir.Waiting
	after.Status = ir.NodeSucceeded
	assert.False(t, ResumePending(fixture.status))

	result, err = fixture.completeReview(t)
	require.NoError(t, err)
	assert.True(t, result.AlreadyCompleted)
	assert.False(t, result.Queued)
	assert.Len(t, fixture.queue.enqueued, 1)

	_, err = fixture.service.Resume(t.Context(), fixture.dag.Name, fixture.status.DAGRunID)
	require.Error(t, err)
	assert.Equal(t, ErrorConflict, KindOf(err))
}

// Approving a step resumes the branches it unblocks even while another manual
// step keeps waiting, unless the resume would re-run a retryable step.
func TestUnblockedNodeReady(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*ir.Node
		want  bool
	}{
		{name: "approved gate unblocks dependent", want: true, nodes: []*ir.Node{
			{Step: ir.Step{ID: "gate", Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded},
			waitingApprovalNode(),
			stepNode("after", ir.NodeNotStarted, "gate"),
		}},
		{name: "dependent still blocked", want: false, nodes: []*ir.Node{
			{Step: ir.Step{ID: "gate", Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded},
			waitingApprovalNode(),
			stepNode("after", ir.NodeNotStarted, "Approval"),
		}},
		{name: "nothing to run", want: false, nodes: []*ir.Node{
			{Step: ir.Step{ID: "gate", Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded},
			waitingApprovalNode(),
		}},
		// Every resume re-runs failed steps, so they run once after the last
		// manual step resolves instead of once per approval.
		{name: "failed step elsewhere", want: false, nodes: []*ir.Node{
			{Step: ir.Step{ID: "gate", Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded},
			waitingApprovalNode(),
			stepNode("lint", ir.NodeFailed),
			stepNode("after", ir.NodeNotStarted, "gate"),
		}},
		{name: "partially succeeded step elsewhere", want: true, nodes: []*ir.Node{
			{Step: ir.Step{ID: "gate", Name: "gate", Approval: &ir.ApprovalConfig{}}, Status: ir.NodeSucceeded},
			waitingApprovalNode(),
			stepNode("lint", ir.NodePartiallySucceeded),
			stepNode("after", ir.NodeNotStarted, "gate"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, UnblockedNodeReady(&ir.DAGRunStatus{Status: ir.Waiting, Nodes: tt.nodes}))
		})
	}
	assert.False(t, UnblockedNodeReady(nil))
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

	// A step unblocked by the completed task does not block a plain retry either.
	status.Nodes = append(status.Nodes, stepNode("after", ir.NodeNotStarted, "Review"))
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

func (f *serviceFixture) completeReview(t *testing.T) (Result, error) {
	t.Helper()
	return f.service.Complete(t.Context(), CompleteRequest{
		DAGName: f.dag.Name, DAGRunID: f.status.DAGRunID, StepID: "review", Input: Input{Values: map[string]any{}},
	})
}

func waitingHumanTaskNode(name string) *ir.Node {
	return &ir.Node{
		Step:   ir.Step{ID: strings.ToLower(name), Name: name, HumanTask: &ir.HumanTaskConfig{Prompt: name}},
		Status: ir.NodeWaiting,
	}
}

func waitingApprovalNode() *ir.Node {
	return &ir.Node{
		Step:   ir.Step{ID: "approval", Name: "Approval", Approval: &ir.ApprovalConfig{}},
		Status: ir.NodeWaiting,
	}
}

func stepNode(name string, status ir.NodeStatus, depends ...string) *ir.Node {
	return &ir.Node{Step: ir.Step{ID: name, Name: name, Depends: depends}, Status: status}
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
