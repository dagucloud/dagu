// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileproc "github.com/dagucloud/dagu/v2/internal/persis/file/proc"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanTaskCommandStructure(t *testing.T) {
	command := HumanTask()
	complete, _, err := command.Find([]string{"complete"})
	require.NoError(t, err)
	assert.Equal(t, "complete", complete.Name())
	assert.Equal(t, commandScopeLocalOnly, scopeForCommand(complete.Name()))
	assert.NotNil(t, complete.Flags().Lookup(humanTaskFlagInput))
	assert.NotNil(t, complete.Flags().Lookup(humanTaskFlagInputsJSON))
	assert.NotNil(t, complete.Flags().Lookup(humanTaskRunIDFlag.name))
	assert.NotNil(t, complete.Flags().Lookup(humanTaskStepFlag.name))
}

func TestParseHumanTaskCompletionInput(t *testing.T) {
	t.Run("RepeatablePairsPreserveEquals", func(t *testing.T) {
		command := humanTaskCompleteCommand()
		require.NoError(t, command.Flags().Set(humanTaskFlagInput, "token=prefix=suffix"))
		require.NoError(t, command.Flags().Set(humanTaskFlagInput, "note="))

		input, err := parseHumanTaskCompletionInput(command)
		require.NoError(t, err)
		assert.True(t, input.CoerceStrings)
		assert.Equal(t, map[string]any{"token": "prefix=suffix", "note": ""}, input.Values)
	})

	t.Run("JSONPreservesTypedValues", func(t *testing.T) {
		command := humanTaskCompleteCommand()
		require.NoError(t, command.Flags().Set(humanTaskFlagInputsJSON, `{"approved":true,"count":3}`))

		input, err := parseHumanTaskCompletionInput(command)
		require.NoError(t, err)
		assert.False(t, input.CoerceStrings)
		assert.Equal(t, true, input.Values["approved"])
		assert.Equal(t, json.Number("3"), input.Values["count"])
	})

	for _, tc := range []struct {
		name      string
		configure func(*cobra.Command)
		contains  string
	}{
		{
			name: "DuplicatePair",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInput, "choice=a"))
				require.NoError(t, command.Flags().Set(humanTaskFlagInput, "choice=b"))
			},
			contains: `--input contains duplicate key "choice"`,
		},
		{
			name: "MissingPairSeparator",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInput, "choice"))
			},
			contains: "--input must use key=value form",
		},
		{
			name: "EmptyPairName",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInput, " =approved"))
			},
			contains: "--input must use key=value form",
		},
		{
			name: "MutuallyExclusive",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInput, "choice=a"))
				require.NoError(t, command.Flags().Set(humanTaskFlagInputsJSON, `{"choice":"a"}`))
			},
			contains: "cannot be used together",
		},
		{
			name: "NonObjectJSON",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInputsJSON, `["a"]`))
			},
			contains: "must be a JSON object",
		},
		{
			name: "MalformedJSON",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInputsJSON, `{"choice":`))
			},
			contains: "invalid --inputs-json JSON value",
		},
		{
			name: "NestedDuplicateJSONMember",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInputsJSON, `{"nested":{"choice":"a","choice":"b"}}`))
			},
			contains: `duplicate JSON member "choice"`,
		},
		{
			name: "TrailingJSON",
			configure: func(command *cobra.Command) {
				require.NoError(t, command.Flags().Set(humanTaskFlagInputsJSON, `{} {}`))
			},
			contains: "exactly one JSON object",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := humanTaskCompleteCommand()
			tc.configure(command)
			_, err := parseHumanTaskCompletionInput(command)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.contains)
		})
	}
}

func TestRunHumanTaskCompletePersistsCanonicalInputAndQueuesRetry(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, humanTaskTestForm(), false)
	require.NoError(t, fixture.command.Flags().Set(humanTaskFlagInput, "count=3"))

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Equal(t, []ir.DAGRunRef{fixture.status.DAGRun()}, fixture.queue.enqueued)

	node := fixture.status.Nodes[0]
	assert.Equal(t, ir.NodeSucceeded, node.Status)
	assert.Equal(t, "Deploy the release?", node.Step.HumanTask.Prompt)
	assert.Equal(t, "2026-07-20T01:02:03Z", node.FinishedAt)
	assert.JSONEq(t, `{"count":3,"region":"us"}`, string(node.HumanTaskInput))
	assert.Equal(t, "local-operator", node.HumanTaskCompletedBy)
	assert.Equal(t, "os:501", node.HumanTaskCompletedByID)
	require.NotNil(t, node.StepOutputsValue)
	assert.JSONEq(t, `{"count":"3","region":"us"}`, *node.StepOutputsValue)
	assert.Contains(t, fixture.output.String(), "DAG-run queued for resume")
}

func TestRunHumanTaskCompleteReportsConcurrentQueue(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	compareAndSwapCalls := 0
	fixture.store.beforeMutate = func() {
		compareAndSwapCalls++
		if compareAndSwapCalls == 2 {
			fixture.status.Status = ir.Queued
		}
	}

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())

	require.NoError(t, err)
	assert.Contains(t, fixture.output.String(), "Completed human task review; DAG-run was already queued for resume.")
	assert.NotContains(t, fixture.output.String(), "was already completed")
}

func TestRunHumanTaskCompleteLeavesRunWaitingForAnotherStep(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, true)
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())
	require.NoError(t, err)
	assert.Empty(t, fixture.queue.enqueued)
	assert.Equal(t, ir.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.Equal(t, ir.NodeWaiting, fixture.status.Nodes[1].Status)
	assert.Contains(t, fixture.output.String(), "remains waiting")
}

func TestRunHumanTaskCompleteIsIdempotentForSameCanonicalInput(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	node := fixture.status.Nodes[0]
	node.Status = ir.NodeSucceeded
	node.HumanTaskInput = json.RawMessage(`{}`)
	node.HumanTaskCompletedBy = "first-operator"
	node.HumanTaskCompletedByID = "os:100"
	node.FinishedAt = "2026-07-20T00:59:00Z"

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Len(t, fixture.queue.enqueued, 1)
	assert.JSONEq(t, `{}`, string(node.HumanTaskInput))
	assert.Equal(t, "first-operator", node.HumanTaskCompletedBy)
	assert.Equal(t, "os:100", node.HumanTaskCompletedByID)
	assert.Equal(t, "2026-07-20T00:59:00Z", node.FinishedAt)
	assert.Contains(t, fixture.output.String(), "already completed")
	assert.Contains(t, fixture.output.String(), "queued for resume")
}

func TestRunHumanTaskCompleteRejectsDifferentInputAfterCompletion(t *testing.T) {
	form := json.RawMessage(`{"type":"object","properties":{"choice":{"type":"string"}},"required":["choice"],"additionalProperties":false}`)
	fixture := newHumanTaskCompleteFixture(t, form, false)
	fixture.status.Nodes[0].Status = ir.NodeSucceeded
	fixture.status.Nodes[0].HumanTaskInput = json.RawMessage(`{"choice":"a"}`)
	require.NoError(t, fixture.command.Flags().Set(humanTaskFlagInput, "choice=b"))

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())
	require.Error(t, err)
	assert.ErrorContains(t, err, "different input")
	assert.Equal(t, ir.Waiting, fixture.status.Status)
	assert.Equal(t, ir.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.JSONEq(t, `{"choice":"a"}`, string(fixture.status.Nodes[0].HumanTaskInput))
	assert.Empty(t, fixture.queue.enqueued)
}

func TestRunHumanTaskCompleteConcurrentSameInputPreservesFirstCompletion(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	fixture.store.beforeMutate = func() {
		node := fixture.status.Nodes[0]
		node.Status = ir.NodeSucceeded
		node.HumanTaskInput = json.RawMessage(`{}`)
		node.HumanTaskCompletedBy = "concurrent-operator"
		node.HumanTaskCompletedByID = "os:200"
		node.FinishedAt = "2026-07-20T01:01:00Z"
	}

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, fixture.status.Status)
	assert.Len(t, fixture.queue.enqueued, 1)
	node := fixture.status.Nodes[0]
	assert.JSONEq(t, `{}`, string(node.HumanTaskInput))
	assert.Equal(t, "concurrent-operator", node.HumanTaskCompletedBy)
	assert.Equal(t, "os:200", node.HumanTaskCompletedByID)
	assert.Equal(t, "2026-07-20T01:01:00Z", node.FinishedAt)
	assert.Contains(t, fixture.output.String(), "already completed")
}

func TestRunHumanTaskCompleteKeepsCompletionWhenEnqueueFails(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	queueErr := errors.New("queue unavailable")
	fixture.queue.enqueueErrors = []error{queueErr}
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())
	require.Error(t, err)
	var resumeErr *humantask.ResumeError
	require.ErrorAs(t, err, &resumeErr)
	assert.ErrorIs(t, err, queueErr)
	assert.Equal(t, "review", resumeErr.Result.StepID)
	assert.Equal(t, "run-1", resumeErr.Result.DAGRunID)
	assert.ErrorContains(t, err, "was completed")
	assert.ErrorContains(t, err, "could not be queued for resume")
	assert.ErrorContains(t, err, "same completion command again")
	assert.Equal(t, ir.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.JSONEq(t, `{}`, string(fixture.status.Nodes[0].HumanTaskInput))
	assert.Nil(t, fixture.status.Nodes[0].StepOutputsValue)
	assert.Equal(t, ir.Waiting, fixture.status.Status)
	assert.True(t, humantask.ResumePending(fixture.status))
	assert.Equal(t, "2026-07-20T01:00:00Z", fixture.status.FinishedAt)
	assert.Empty(t, fixture.errorOutput.String())
}

func TestRunHumanTaskCompleteContinuesWhenOSUserLookupFails(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, true)
	deps := fixture.deps()
	deps.currentUser = func() (*user.User, error) {
		return nil, errors.New("user lookup unavailable")
	}

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, deps)
	require.NoError(t, err)
	assert.Empty(t, fixture.status.Nodes[0].HumanTaskCompletedBy)
	assert.Empty(t, fixture.status.Nodes[0].HumanTaskCompletedByID)
}

func TestRunHumanTaskCompleteEnforcesSavedDAGOutputSize(t *testing.T) {
	form := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`)
	fixture := newHumanTaskCompleteFixture(t, form, false)
	fixture.dag.MaxOutputSize = 12
	require.NoError(t, fixture.command.Flags().Set(humanTaskFlagInput, "count=3"))

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps())
	require.Error(t, err)
	assert.ErrorContains(t, err, "step outputs exceeded maximum size limit of 12 bytes")
	assert.Equal(t, ir.Waiting, fixture.status.Status)
	assert.Equal(t, ir.NodeWaiting, fixture.status.Nodes[0].Status)
	assert.Empty(t, fixture.queue.enqueued)
}

type humanTaskCompleteFixture struct {
	command     *cobra.Command
	ctx         *Context
	dag         *ir.DAG
	status      *ir.DAGRunStatus
	store       *humanTaskCompletionStore
	queue       *humanTaskCompletionQueueStore
	output      *bytes.Buffer
	errorOutput *bytes.Buffer
}

func newHumanTaskCompleteFixture(t *testing.T, form json.RawMessage, anotherWaiting bool) *humanTaskCompleteFixture {
	t.Helper()
	step := ir.Step{
		ID:   "review",
		Name: "Review",
		HumanTask: &ir.HumanTaskConfig{
			Prompt: "Deploy the release?",
			Form:   form,
		},
	}
	dag := &ir.DAG{
		Name:     "human-task-test",
		Location: filepath.Join(t.TempDir(), "human-task-test.yaml"),
		Steps:    []ir.Step{step},
	}
	status := &ir.DAGRunStatus{
		Name:       dag.Name,
		DAGRunID:   "run-1",
		AttemptID:  "attempt-1",
		AttemptKey: "attempt-key-1",
		Status:     ir.Waiting,
		FinishedAt: "2026-07-20T01:00:00Z",
		Nodes: []*ir.Node{{
			Step:   step,
			Status: ir.NodeWaiting,
		}},
	}
	if anotherWaiting {
		status.Nodes = append(status.Nodes, &ir.Node{
			Step:   ir.Step{ID: "approval", Name: "Approval"},
			Status: ir.NodeWaiting,
		})
	}
	attempt := &humanTaskCompletionAttempt{dag: dag, status: status}
	store := &humanTaskCompletionStore{attempt: attempt, status: status}
	queue := &humanTaskCompletionQueueStore{}
	command := humanTaskCompleteCommand()
	require.NoError(t, command.Flags().Set(humanTaskRunIDFlag.name, "run-1"))
	require.NoError(t, command.Flags().Set(humanTaskStepFlag.name, "review"))
	output := &bytes.Buffer{}
	errorOutput := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(errorOutput)
	return &humanTaskCompleteFixture{
		command: command,
		ctx: &Context{
			Context: t.Context(),
			Command: command,
			Persistence: Persistence{
				DAGRunRepository: persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{}),
				QueueStore:       queue,
				ProcRepository:   persis.NewProcRepository(fileproc.New(t.TempDir())),
			},
		},
		dag:         dag,
		status:      status,
		store:       store,
		queue:       queue,
		output:      output,
		errorOutput: errorOutput,
	}
}

func (*humanTaskCompleteFixture) deps() humanTaskCompleteDeps {
	return humanTaskCompleteDeps{
		now: func() time.Time { return time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC) },
		currentUser: func() (*user.User, error) {
			return &user.User{Uid: "501", Username: "local-operator"}, nil
		},
	}
}

func humanTaskTestForm() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "count":{"type":"integer"},
    "region":{"type":"string","default":"us"}
  },
  "required":["count"],
  "additionalProperties":false
}`)
}

type humanTaskCompletionAttempt struct {
	dagrun.Attempt
	dag    *ir.DAG
	status *ir.DAGRunStatus
}

func (a *humanTaskCompletionAttempt) ID() string {
	return a.status.AttemptID
}

func (a *humanTaskCompletionAttempt) ReadDAG(context.Context) (*ir.DAG, error) {
	return a.dag, nil
}

func (a *humanTaskCompletionAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	return a.status, nil
}

func (a *humanTaskCompletionAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}

type humanTaskCompletionStore struct {
	testutil.DAGRunStoreStub
	attempt      *humanTaskCompletionAttempt
	status       *ir.DAGRunStatus
	beforeMutate func()
}

func (s *humanTaskCompletionStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	return s.attempt, nil
}

func (s *humanTaskCompletionStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	if s.beforeMutate != nil {
		s.beforeMutate()
	}
	if s.status.AttemptID != req.ExpectedAttemptID || s.status.Status != req.ExpectedStatus {
		return s.status, false, nil
	}
	if req.ExpectedAttemptKey != "" && s.status.AttemptKey != req.ExpectedAttemptKey {
		return s.status, false, nil
	}
	if err := req.Mutate(s.status); err != nil {
		return nil, false, err
	}
	return s.status, true, nil
}

type humanTaskCompletionQueueStore struct {
	queue.QueueStore
	enqueued      []ir.DAGRunRef
	enqueueErrors []error
}

func (s *humanTaskCompletionQueueStore) Enqueue(
	_ context.Context,
	_ string,
	_ queue.QueuePriority,
	ref ir.DAGRunRef,
) error {
	if len(s.enqueueErrors) > 0 {
		err := s.enqueueErrors[0]
		s.enqueueErrors = s.enqueueErrors[1:]
		return err
	}
	s.enqueued = append(s.enqueued, ref)
	return nil
}
