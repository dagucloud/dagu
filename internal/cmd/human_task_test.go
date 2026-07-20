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
			contains: "invalid --inputs-json value",
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

func TestFindHumanTaskNodeByIDDoesNotMatchDisplayName(t *testing.T) {
	nodes := []*exec.Node{{
		Step: core.Step{
			ID:        "review-id",
			Name:      "Review",
			HumanTask: &core.HumanTaskConfig{Prompt: "Review?"},
		},
	}}

	_, err := findHumanTaskNodeByID(nodes, "Review")
	require.Error(t, err)
	assert.ErrorContains(t, err, "was not found")
}

func TestRunHumanTaskCompletePersistsCanonicalInputAndLaunchesRetry(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, humanTaskTestForm(), false)
	require.NoError(t, fixture.command.Flags().Set(humanTaskFlagInput, "count=3"))

	var launched bool
	deps := humanTaskCompleteDeps{
		now:   func() time.Time { return time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC) },
		actor: func() string { return "operator" },
		launch: func(_ *Context, _ *core.DAG, status *exec.DAGRunStatus, root, owning exec.DAGRunRef) error {
			launched = true
			assert.Same(t, fixture.status, status)
			assert.Equal(t, exec.NewDAGRunRef("human-task-test", "run-1"), root)
			assert.Equal(t, root, owning)
			return nil
		},
	}

	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, deps)
	require.NoError(t, err)
	assert.True(t, launched)
	assert.Equal(t, 1, fixture.store.compareAndSwapCalls)
	assert.Equal(t, core.Waiting, fixture.store.expectedStatus)
	assert.Equal(t, "attempt-1", fixture.store.expectedAttemptID)
	assert.Equal(t, "attempt-key-1", fixture.store.options.ExpectedAttemptKey)
	assert.Equal(t, exec.NewDAGRunRef("human-task-test", "run-1"), fixture.store.options.RootDAGRun)
	assert.Nil(t, fixture.store.options.ExpectedRootStatus)

	node := fixture.status.Nodes[0]
	assert.Equal(t, core.NodeSucceeded, node.Status)
	assert.Equal(t, "Deploy the release?", node.HumanTaskPrompt)
	assert.Equal(t, "2026-07-20T01:02:03Z", node.HumanTaskCompletedAt)
	assert.Equal(t, "operator", node.HumanTaskCompletedBy)
	assert.JSONEq(t, `{"count":3,"region":"us"}`, string(node.HumanTaskInput))
	require.NotNil(t, node.StepOutputsValue)
	assert.JSONEq(t, `{"count":"3","region":"us"}`, *node.StepOutputsValue)
	assert.Contains(t, fixture.output.String(), "DAG-run resume started")
}

func TestRunHumanTaskCompleteLeavesRunWaitingForAnotherStep(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, true)
	launchCalls := 0
	deps := fixture.deps(func(*Context, *core.DAG, *exec.DAGRunStatus, exec.DAGRunRef, exec.DAGRunRef) error {
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
	fixture.status.Nodes[0].HumanTaskCompletedAt = "2026-07-20T01:02:03Z"

	launchCalls := 0
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(*Context, *core.DAG, *exec.DAGRunStatus, exec.DAGRunRef, exec.DAGRunRef) error {
			launchCalls++
			return nil
		},
	))
	require.NoError(t, err)
	assert.Zero(t, fixture.store.compareAndSwapCalls)
	assert.Zero(t, launchCalls)
	assert.Contains(t, fixture.output.String(), "already completed")
	assert.Contains(t, fixture.output.String(), "dagu retry --run-id=run-1 human-task-test")
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
		fixture.status.Nodes[0].HumanTaskCompletedAt = "2026-07-20T01:02:03Z"
	}

	launchCalls := 0
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(*Context, *core.DAG, *exec.DAGRunStatus, exec.DAGRunRef, exec.DAGRunRef) error {
			launchCalls++
			return nil
		},
	))
	require.NoError(t, err)
	assert.Equal(t, 1, fixture.store.compareAndSwapCalls)
	assert.Zero(t, fixture.store.writes)
	assert.Zero(t, launchCalls)
	assert.Contains(t, fixture.output.String(), "already completed")
}

func TestRunHumanTaskCompleteKeepsCompletionWhenRetryLaunchFails(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(*Context, *core.DAG, *exec.DAGRunStatus, exec.DAGRunRef, exec.DAGRunRef) error {
			return errors.New("executable unavailable")
		},
	))
	require.Error(t, err)
	assert.ErrorContains(t, err, "was completed")
	assert.ErrorContains(t, err, "dagu retry --run-id=run-1 human-task-test")
	assert.Equal(t, core.NodeSucceeded, fixture.status.Nodes[0].Status)
	assert.JSONEq(t, `{}`, string(fixture.status.Nodes[0].HumanTaskInput))
	assert.Nil(t, fixture.status.Nodes[0].StepOutputsValue)
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

func TestRunHumanTaskCompleteUsesSubRunRootGuards(t *testing.T) {
	fixture := newHumanTaskCompleteFixture(t, nil, false)
	fixture.status.Name = "child-task"
	fixture.status.DAGRunID = "sub-1"
	fixture.status.Root = exec.NewDAGRunRef("human-task-test", "run-1")
	fixture.store.subRunID = "sub-1"
	require.NoError(t, fixture.command.Flags().Set(humanTaskSubRunIDFlag.name, "sub-1"))

	var launchedRoot, launchedOwning exec.DAGRunRef
	err := runHumanTaskCompleteWith(fixture.ctx, []string{"human-task-test"}, fixture.deps(
		func(_ *Context, _ *core.DAG, _ *exec.DAGRunStatus, root, owning exec.DAGRunRef) error {
			launchedRoot = root
			launchedOwning = owning
			return nil
		},
	))
	require.NoError(t, err)
	assert.Equal(t, exec.NewDAGRunRef("human-task-test", "run-1"), launchedRoot)
	assert.Equal(t, exec.NewDAGRunRef("child-task", "sub-1"), launchedOwning)
	require.NotNil(t, fixture.store.options.ExpectedRootStatus)
	assert.Equal(t, core.Waiting, *fixture.store.options.ExpectedRootStatus)
	assert.Equal(t, "dagu retry --run-id=sub-1 --root=human-task-test:run-1 child-task",
		humanTaskRetryCommand(fixture.ctx, "child-task", launchedRoot, launchedOwning))
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
	dag := &core.DAG{Name: "child-task"}
	rootRef := exec.NewDAGRunRef("root-task", "run-1")
	owningRef := exec.NewDAGRunRef(dag.Name, "sub-1")

	retrySpec := humanTaskRetrySpec(ctx, dag, rootRef, owningRef)
	assert.Contains(t, retrySpec.Args, "--dagu-home="+daguHome)
	assert.Contains(t, retrySpec.Args, configFile)
	assert.Equal(t,
		"dagu retry --run-id=sub-1 --root=root-task:run-1 --config '"+configFile+"' --dagu-home '"+daguHome+"' child-task",
		humanTaskRetryCommand(ctx, dag.Name, rootRef, owningRef),
	)
}

func TestWaitForHumanTaskCompletionReadyWaitsForAttemptToSettle(t *testing.T) {
	dag := &core.DAG{Name: "human-task-test"}
	status := &exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Waiting,
	}
	attempt := &humanTaskCompletionAttempt{dag: dag, status: status}
	procStore := &humanTaskCompletionProcStore{alive: []bool{true, false}}
	ctx := &Context{Context: t.Context(), ProcStore: procStore}

	latest, err := waitForHumanTaskCompletionReady(ctx, attempt, dag, status)
	require.NoError(t, err)
	assert.Same(t, status, latest)
	assert.Equal(t, 2, procStore.calls)
	assert.Equal(t, dag.ProcGroup(), procStore.groupName)
	assert.Equal(t, status.DAGRun(), procStore.dagRun)
	assert.Equal(t, status.AttemptID, procStore.attemptID)
}

type humanTaskCompleteFixture struct {
	command *cobra.Command
	ctx     *Context
	dag     *core.DAG
	status  *exec.DAGRunStatus
	store   *humanTaskCompletionStore
	output  *bytes.Buffer
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
		Nodes: []*exec.Node{{
			Step:            step,
			Status:          core.NodeWaiting,
			HumanTaskPrompt: "Deploy the release?",
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
	command.SetOut(output)
	return &humanTaskCompleteFixture{
		command: command,
		ctx: &Context{
			Context:     t.Context(),
			Command:     command,
			DAGRunStore: store,
		},
		dag:    dag,
		status: status,
		store:  store,
		output: output,
	}
}

func (f *humanTaskCompleteFixture) deps(
	launch func(*Context, *core.DAG, *exec.DAGRunStatus, exec.DAGRunRef, exec.DAGRunRef) error,
) humanTaskCompleteDeps {
	if launch == nil {
		launch = func(*Context, *core.DAG, *exec.DAGRunStatus, exec.DAGRunRef, exec.DAGRunRef) error {
			return nil
		}
	}
	return humanTaskCompleteDeps{
		now:    func() time.Time { return time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC) },
		actor:  func() string { return "operator" },
		launch: launch,
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

type humanTaskCompletionStore struct {
	exec.DAGRunStore
	attempt             *humanTaskCompletionAttempt
	status              *exec.DAGRunStatus
	subRunID            string
	compareAndSwapCalls int
	expectedAttemptID   string
	expectedStatus      core.Status
	options             exec.CompareAndSwapStatusOptions
	beforeMutate        func()
	writes              int
}

type humanTaskCompletionProcStore struct {
	exec.ProcStore
	alive     []bool
	calls     int
	groupName string
	dagRun    exec.DAGRunRef
	attemptID string
}

func (s *humanTaskCompletionProcStore) IsAttemptAlive(
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

func (s *humanTaskCompletionStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return s.attempt, nil
}

func (s *humanTaskCompletionStore) FindSubAttempt(_ context.Context, _ exec.DAGRunRef, subRunID string) (exec.DAGRunAttempt, error) {
	if subRunID != s.subRunID {
		return nil, exec.ErrDAGRunIDNotFound
	}
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
