// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
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
		assert.True(t, input.coerceStrings)
		assert.Equal(t, map[string]any{"token": "prefix=suffix", "note": ""}, input.values)
	})

	t.Run("JSONPreservesTypedValues", func(t *testing.T) {
		command := humanTaskCompleteCommand()
		require.NoError(t, command.Flags().Set(humanTaskFlagInputsJSON, `{"approved":true,"count":3}`))

		input, err := parseHumanTaskCompletionInput(command)
		require.NoError(t, err)
		assert.False(t, input.coerceStrings)
		assert.Equal(t, true, input.values["approved"])
		assert.Equal(t, json.Number("3"), input.values["count"])
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
			contains: "duplicate key",
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

func TestRunHumanTaskCompletePersistsCanonicalInputAndLaunchesRetry(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, humanTaskTestForm(), false)
	require.NoError(t, fixture.command.Flags().Set(humanTaskFlagInput, "count=3"))

	var launched bool
	deps := humanTaskCompleteDeps{
		now: func() time.Time { return time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC) },
		resume: func(_ *Context, _ *core.DAG, status *exec.DAGRunStatus) error {
			launched = true
			assert.Same(t, fixture.status, status)
			return nil
		},
	}

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, deps)
	require.NoError(t, err)
	assert.True(t, launched)
	assert.Equal(t, 2, fixture.store.compareAndSwapCalls)
	assert.Equal(t, core.Waiting, fixture.store.expectedStatus)
	assert.Equal(t, "attempt-1", fixture.store.expectedAttemptID)
	assert.Equal(t, "attempt-key-1", fixture.store.options.ExpectedAttemptKey)
	assert.True(t, fixture.store.options.RootDAGRun.Zero())

	node := fixture.status.Nodes[0]
	assert.Equal(t, core.NodeSucceeded, node.Status)
	assert.Equal(t, "Deploy the release?", node.Step.HumanTask.Prompt)
	assert.Equal(t, "2026-07-20T01:02:03Z", node.FinishedAt)
	assert.JSONEq(t, `{"count":3,"region":"us"}`, string(node.HumanTaskInput))
	require.NotNil(t, node.StepOutputsValue)
	assert.JSONEq(t, `{"count":"3","region":"us"}`, *node.StepOutputsValue)
	assert.Contains(t, fixture.output.String(), "DAG-run resume requested")
}

func TestRunHumanTaskCompleteLeavesRunWaitingForAnotherStep(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, true)
	launchCalls := 0
	deps := fixture.deps(func(*Context, *core.DAG, *exec.DAGRunStatus) error {
		launchCalls++
		return nil
	})

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, deps)
	require.NoError(t, err)
	assert.Zero(t, launchCalls)
	assert.Equal(t, core.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.Equal(t, core.NodeWaiting, fixture.status.Nodes[1].Status)
	assert.Contains(t, fixture.output.String(), "remains waiting")
}

func TestRunHumanTaskCompleteIsIdempotentForSameCanonicalInput(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	fixture.status.Nodes[0].Status = core.NodeSucceeded
	fixture.status.Nodes[0].HumanTaskInput = json.RawMessage(`{}`)

	launchCalls := 0
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(*Context, *core.DAG, *exec.DAGRunStatus) error {
			launchCalls++
			return nil
		},
	))
	require.NoError(t, err)
	assert.Equal(t, 2, fixture.store.compareAndSwapCalls)
	assert.Equal(t, 1, launchCalls)
	assert.Contains(t, fixture.output.String(), "already completed")
	assert.Contains(t, fixture.output.String(), "resume requested")
}

func TestRunHumanTaskCompleteRejectsDifferentInputAfterCompletion(t *testing.T) {
	form := json.RawMessage(`{"type":"object","properties":{"choice":{"type":"string"}},"required":["choice"],"additionalProperties":false}`)
	fixture := newHumanTaskCompleteFixture(t, form, false)
	fixture.status.Nodes[0].Status = core.NodeSucceeded
	fixture.status.Nodes[0].HumanTaskInput = json.RawMessage(`{"choice":"a"}`)
	require.NoError(t, fixture.command.Flags().Set(humanTaskFlagInput, "choice=b"))

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(nil))
	require.Error(t, err)
	assert.ErrorContains(t, err, "different input")
	assert.Zero(t, fixture.store.compareAndSwapCalls)
}

func TestRunHumanTaskCompleteConcurrentSameInputDoesNotWriteAgain(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	fixture.store.beforeMutate = func() {
		fixture.status.Nodes[0].Status = core.NodeSucceeded
		fixture.status.Nodes[0].HumanTaskInput = json.RawMessage(`{}`)
	}

	launchCalls := 0
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(*Context, *core.DAG, *exec.DAGRunStatus) error {
			launchCalls++
			return nil
		},
	))
	require.NoError(t, err)
	assert.Equal(t, 3, fixture.store.compareAndSwapCalls)
	assert.Equal(t, 2, fixture.store.writes)
	assert.Equal(t, 1, launchCalls)
	assert.Contains(t, fixture.output.String(), "already completed")
}

func TestRunHumanTaskCompleteKeepsCompletionWhenRetryLaunchFails(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(*Context, *core.DAG, *exec.DAGRunStatus) error {
			return errors.New("executable unavailable")
		},
	))
	require.Error(t, err)
	assert.ErrorContains(t, err, "was completed")
	assert.ErrorContains(t, err, "same completion command again")
	assert.Equal(t, core.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.JSONEq(t, `{}`, string(fixture.status.Nodes[0].HumanTaskInput))
	assert.Nil(t, fixture.status.Nodes[0].StepOutputsValue)
	assert.Equal(t, "2026-07-20T01:00:00Z", fixture.status.FinishedAt)
	assert.Empty(t, fixture.errorOutput.String())
}

func TestRunHumanTaskCompleteReportsResumeClaimRollbackFailure(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	fixture.store.failCompareAndSwapCall = 3
	fixture.store.compareAndSwapErr = errors.New("status write failed")

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(*Context, *core.DAG, *exec.DAGRunStatus) error {
			return errors.New("executable unavailable")
		},
	))
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to release resume claim")
	assert.ErrorContains(t, err, "status write failed")
	assert.Empty(t, fixture.errorOutput.String())
}

func TestRunHumanTaskCompleteEnforcesSavedDAGOutputSize(t *testing.T) {
	form := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`)
	fixture := newHumanTaskCompleteFixture(t, form, false)
	fixture.dag.MaxOutputSize = 12
	require.NoError(t, fixture.command.Flags().Set(humanTaskFlagInput, "count=3"))

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(nil))
	require.Error(t, err)
	assert.ErrorContains(t, err, "step outputs exceeded maximum size limit of 12 bytes")
	assert.Zero(t, fixture.store.compareAndSwapCalls)
	assert.Equal(t, core.NodeWaiting, fixture.status.Nodes[0].Status)
}

func TestHumanTaskRetryPreservesExplicitPaths(t *testing.T) {
	command := humanTaskCompleteCommand()
	daguHome := filepath.Join(t.TempDir(), "custom home")
	configFile := filepath.Join(daguHome, "config file.yaml")
	require.NoError(t, command.Flags().Set(daguHomeFlag.name, daguHome))

	ctx := &Context{
		Context: t.Context(),
		Command: command,
		Config: &config.Config{
			Core: config.Core{BaseEnv: config.NewBaseEnv([]string{"DAGU_HOME=" + daguHome})},
			Paths: config.PathsConfig{
				Executable:     "dagu",
				ConfigFileUsed: configFile,
			},
		},
	}
	dag := &core.DAG{Name: "human-task"}

	retrySpec := humanTaskRetrySpec(ctx, dag, "run-1", "claim-1")
	assert.Contains(t, retrySpec.Args, "--dagu-home="+daguHome)
	assert.Contains(t, retrySpec.Args, configFile)
	assert.Contains(t, retrySpec.Args, "--run-id=run-1")
	assert.Contains(t, retrySpec.Args, "--human-task-resume-token=claim-1")
}

type humanTaskCompleteFixture struct {
	command     *cobra.Command
	ctx         *Context
	dag         *core.DAG
	status      *exec.DAGRunStatus
	store       *humanTaskCompletionStore
	output      *bytes.Buffer
	errorOutput *bytes.Buffer
}

func newHumanTaskCompleteFixture(t *testing.T, form json.RawMessage, anotherWaiting bool) *humanTaskCompleteFixture {
	t.Helper()
	step := core.Step{
		ID:   "review",
		Name: "Review",
		HumanTask: &core.HumanTaskConfig{
			Prompt: "Deploy the release?",
			Form:   form,
		},
	}
	dag := &core.DAG{
		Name:     "human-task-test",
		Location: filepath.Join(t.TempDir(), "human-task-test.yaml"),
		Steps:    []core.Step{step},
	}
	status := &exec.DAGRunStatus{
		Name:       dag.Name,
		DAGRunID:   "run-1",
		AttemptID:  "attempt-1",
		AttemptKey: "attempt-key-1",
		Status:     core.Waiting,
		FinishedAt: "2026-07-20T01:00:00Z",
		Nodes: []*exec.Node{{
			Step:   step,
			Status: core.NodeWaiting,
		}},
	}
	if anotherWaiting {
		status.Nodes = append(status.Nodes, &exec.Node{
			Step:   core.Step{ID: "approval", Name: "Approval"},
			Status: core.NodeWaiting,
		})
	}
	attempt := &humanTaskCompletionAttempt{dag: dag, status: status}
	store := &humanTaskCompletionStore{attempt: attempt, status: status}
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
			Context:     t.Context(),
			Command:     command,
			DAGRunStore: store,
			ProcStore:   humanTaskCompletionProcStore{},
		},
		dag:         dag,
		status:      status,
		store:       store,
		output:      output,
		errorOutput: errorOutput,
	}
}

func (f *humanTaskCompleteFixture) deps(
	resume func(*Context, *core.DAG, *exec.DAGRunStatus) error,
) humanTaskCompleteDeps {
	if resume == nil {
		resume = func(*Context, *core.DAG, *exec.DAGRunStatus) error {
			return nil
		}
	}
	return humanTaskCompleteDeps{
		now:    func() time.Time { return time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC) },
		resume: resume,
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
	exec.DAGRunAttempt
	dag    *core.DAG
	status *exec.DAGRunStatus
}

func (a *humanTaskCompletionAttempt) ID() string {
	return a.status.AttemptID
}

func (a *humanTaskCompletionAttempt) ReadDAG(context.Context) (*core.DAG, error) {
	return a.dag, nil
}

func (a *humanTaskCompletionAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	return a.status, nil
}

type humanTaskCompletionProcStore struct {
	exec.ProcStore
}

func (humanTaskCompletionProcStore) IsRunAlive(context.Context, string, exec.DAGRunRef) (bool, error) {
	return false, nil
}

func (humanTaskCompletionProcStore) IsAttemptAlive(context.Context, string, exec.DAGRunRef, string) (bool, error) {
	return false, nil
}

type humanTaskCompletionStore struct {
	exec.DAGRunStore
	attempt                *humanTaskCompletionAttempt
	status                 *exec.DAGRunStatus
	compareAndSwapCalls    int
	expectedAttemptID      string
	expectedStatus         core.Status
	options                exec.CompareAndSwapStatusOptions
	beforeMutate           func()
	writes                 int
	failCompareAndSwapCall int
	compareAndSwapErr      error
}

func (s *humanTaskCompletionStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return s.attempt, nil
}

func (s *humanTaskCompletionStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	_ exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	opts ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	s.compareAndSwapCalls++
	if s.compareAndSwapCalls == s.failCompareAndSwapCall {
		return s.status, false, s.compareAndSwapErr
	}
	s.expectedAttemptID = expectedAttemptID
	s.expectedStatus = expectedStatus
	s.options = exec.NewCompareAndSwapStatusOptions(opts...)
	if s.status.AttemptID != expectedAttemptID || s.status.Status != expectedStatus {
		return s.status, false, nil
	}
	if s.options.ExpectedAttemptKey != "" && s.status.AttemptKey != s.options.ExpectedAttemptKey {
		return s.status, false, nil
	}
	if s.beforeMutate != nil {
		s.beforeMutate()
	}
	if err := mutate(s.status); err != nil {
		return nil, false, err
	}
	s.writes++
	return s.status, true, nil
}
