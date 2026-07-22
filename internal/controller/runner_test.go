// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerTestGateway struct {
	observation ChildRunObservation
	ensured     []ChildRunRequest
	stopped     []ChildRunRequest
	ensureErr   error
	observeErr  error
	ensureStart chan struct{}
	ensureAllow chan struct{}
}

var runnerFixtureNow = time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)

func setTestActiveDAGRun(runtime *Runtime, active ActiveDAGRun) {
	params := active.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	arguments, _ := json.Marshal(map[string]any{
		"action": "run", "next_state": runtime.CurrentState, "reason": "Test route.",
		"dag": active.DAG, "params": json.RawMessage(params),
	})
	runtime.Context = append(runtime.Context, exec.LLMMessage{
		Role: exec.RoleAssistant,
		ToolCalls: []exec.ToolCall{{
			ID: active.ToolCallID, Type: "function",
			Function: exec.ToolCallFunction{Name: routeToolName, Arguments: string(arguments)},
		}},
	})
	active.Params = params
	runtime.ActiveDAGRun = &active
}

func (g *runnerTestGateway) EnsureEnqueued(_ context.Context, request ChildRunRequest) error {
	if g.ensureStart != nil {
		close(g.ensureStart)
		<-g.ensureAllow
	}
	g.ensured = append(g.ensured, request)
	if g.ensureErr == nil {
		g.observation = ChildRunObservation{Exists: true, Status: core.Queued}
	}
	return g.ensureErr
}

func (g *runnerTestGateway) Observe(context.Context, ChildRunRequest) (ChildRunObservation, error) {
	return g.observation, g.observeErr
}

func (g *runnerTestGateway) Stop(_ context.Context, request ChildRunRequest) error {
	g.stopped = append(g.stopped, request)
	g.observation = ChildRunObservation{Exists: true, Status: core.Aborted}
	return nil
}

func TestRunnerRoutesAndReconcilesSuccessfulChild(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"run","next_state":"default","reason":"The alert can be classified.","dag":"classify","params":{"alert":"A-123"}}`)}
	gateway := &runnerTestGateway{}
	runner, stores, definition := newRunnerFixture(t, provider, gateway)
	now := runnerFixtureNow

	runner.reconcile(context.Background(), definition.ID)
	runtime := readRunnerRuntime(t, stores, definition.ID)
	require.NotNil(t, runtime.ActiveDAGRun)
	assert.Equal(t, "run-1", runtime.ActiveDAGRun.DAGRunID)
	assert.Equal(t, 1, runtime.TurnCount)
	require.Len(t, gateway.ensured, 1)
	assert.JSONEq(t, `{"alert":"A-123"}`, string(gateway.ensured[0].Params))

	runner.reconcile(context.Background(), definition.ID)
	runtime = readRunnerRuntime(t, stores, definition.ID)
	require.Len(t, runtime.DAGRunRefs, 1)
	assert.Equal(t, core.Running, runtime.Status)

	gateway.observation = ChildRunObservation{
		Exists: true, Status: core.Succeeded,
		Outputs: map[string]string{"classification": "actionable"},
	}
	runner.reconcile(context.Background(), definition.ID)
	runtime = readRunnerRuntime(t, stores, definition.ID)
	assert.Nil(t, runtime.ActiveDAGRun)
	assert.Equal(t, core.Running, runtime.Status)
	require.Len(t, runtime.DAGRunRefs, 1)
	require.Len(t, runtime.Context, 3)
	assert.Equal(t, exec.RoleTool, runtime.Context[2].Role)
	assert.Equal(t, now, runtime.UpdatedAt)
}

func TestRunnerAdoptsWaitAndComplete(t *testing.T) {
	t.Parallel()

	t.Run("wait", func(t *testing.T) {
		provider := &routerTestProvider{response: routeResponse(`{"action":"wait","next_state":"default","reason":"The region is missing.","question":"Which region should be used?"}`)}
		runner, stores, definition := newRunnerFixture(t, provider, &runnerTestGateway{})

		runner.reconcile(context.Background(), definition.ID)
		runtime := readRunnerRuntime(t, stores, definition.ID)
		assert.Equal(t, core.Waiting, runtime.Status)
		require.NotNil(t, runtime.WaitingQuestion)
		assert.Equal(t, "Which region should be used?", *runtime.WaitingQuestion)
		require.Len(t, runtime.Context, 3)
		assert.Equal(t, exec.RoleTool, runtime.Context[2].Role)
	})

	t.Run("complete", func(t *testing.T) {
		provider := &routerTestProvider{response: routeResponse(`{"action":"complete","next_state":"done","reason":"The configured completion condition is satisfied."}`)}
		runner, stores, definition := newRunnerFixture(t, provider, &runnerTestGateway{})

		runner.reconcile(context.Background(), definition.ID)
		runtime := readRunnerRuntime(t, stores, definition.ID)
		assert.Equal(t, core.Succeeded, runtime.Status)
		assert.Equal(t, "done", runtime.CurrentState)
		require.NotNil(t, runtime.FinishedAt)
		require.Len(t, runtime.Context, 3)
	})
}

func TestRunnerStopsAtMaximumTurnsBeforeCallingProvider(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`)}
	runner, stores, definition := newRunnerFixture(t, provider, &runnerTestGateway{})
	runtime := readRunnerRuntime(t, stores, definition.ID)
	runtime.TurnCount = definition.MaxTurns
	require.NoError(t, stores.Runtimes.Put(context.Background(), runtime))

	runner.reconcile(context.Background(), definition.ID)
	runtime = readRunnerRuntime(t, stores, definition.ID)
	assert.Equal(t, core.Failed, runtime.Status)
	require.NotNil(t, runtime.LastError)
	assert.Equal(t, "max_turns_exceeded", *runtime.LastError)
	assert.Zero(t, provider.calls)
}

func TestRunnerFailsWhenActiveChildCannotBeObserved(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		observation  ChildRunObservation
		expectedRefs int
	}{
		{name: "attempt existence unknown"},
		{name: "attempt exists", observation: ChildRunObservation{Exists: true}, expectedRefs: 1},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gateway := &runnerTestGateway{observation: test.observation, observeErr: assert.AnError}
			runner, stores, definition := newRunnerFixture(t, &routerTestProvider{}, gateway)
			runtime := readRunnerRuntime(t, stores, definition.ID)
			setTestActiveDAGRun(runtime, ActiveDAGRun{
				ToolCallID: "call-1",
				DAG:        "classify",
				DAGRunID:   "run-1",
				Params:     json.RawMessage(`{}`),
			})
			require.NoError(t, stores.Runtimes.Put(context.Background(), runtime))

			runner.reconcile(context.Background(), definition.ID)
			runtime = readRunnerRuntime(t, stores, definition.ID)
			assert.Equal(t, core.Failed, runtime.Status)
			assert.Nil(t, runtime.ActiveDAGRun)
			assert.Len(t, runtime.DAGRunRefs, test.expectedRefs)
			require.NotNil(t, runtime.LastError)
			assert.Equal(t, "child_observation_failed", *runtime.LastError)
		})
	}
}

func TestRunnerSettlesStoppedChildThatWasNeverCreated(t *testing.T) {
	t.Parallel()

	runner, stores, definition := newRunnerFixture(t, &routerTestProvider{}, &runnerTestGateway{})
	runtime := readRunnerRuntime(t, stores, definition.ID)
	runtime.Status = core.Aborted
	setTestActiveDAGRun(runtime, ActiveDAGRun{ToolCallID: "call-1", DAG: "classify", DAGRunID: "run-1", Params: json.RawMessage(`{}`)})
	require.NoError(t, stores.Runtimes.Put(context.Background(), runtime))

	runner.reconcile(context.Background(), definition.ID)
	runtime = readRunnerRuntime(t, stores, definition.ID)
	assert.Equal(t, core.Aborted, runtime.Status)
	assert.Nil(t, runtime.ActiveDAGRun)
	require.NotNil(t, runtime.FinishedAt)
	assert.Empty(t, runtime.DAGRunRefs)
}

func TestRunnerSettlesOrFailsExistingChildWithInvalidStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []core.Status{core.NotStarted, core.Status(99)} {
		status := status
		t.Run("active-"+status.String(), func(t *testing.T) {
			gateway := &runnerTestGateway{observation: ChildRunObservation{Exists: true, Status: status}}
			runner, stores, definition := newRunnerFixture(t, &routerTestProvider{}, gateway)
			runtime := readRunnerRuntime(t, stores, definition.ID)
			setTestActiveDAGRun(runtime, ActiveDAGRun{ToolCallID: "call-1", DAG: "classify", DAGRunID: "run-1", Params: json.RawMessage(`{}`)})
			require.NoError(t, stores.Runtimes.Put(context.Background(), runtime))

			runner.reconcile(context.Background(), definition.ID)

			runtime = readRunnerRuntime(t, stores, definition.ID)
			assert.Equal(t, core.Failed, runtime.Status)
			assert.Nil(t, runtime.ActiveDAGRun)
			require.Len(t, runtime.DAGRunRefs, 1)
			assert.Equal(t, "run-1", runtime.DAGRunRefs[0].DAGRunID)
			require.NotNil(t, runtime.LastError)
			assert.Equal(t, "child_status_invalid", *runtime.LastError)
		})

		t.Run("aborted-"+status.String(), func(t *testing.T) {
			gateway := &runnerTestGateway{observation: ChildRunObservation{Exists: true, Status: status}}
			runner, stores, definition := newRunnerFixture(t, &routerTestProvider{}, gateway)
			runtime := readRunnerRuntime(t, stores, definition.ID)
			runtime.Status = core.Aborted
			setTestActiveDAGRun(runtime, ActiveDAGRun{ToolCallID: "call-1", DAG: "classify", DAGRunID: "run-1", Params: json.RawMessage(`{}`)})
			require.NoError(t, stores.Runtimes.Put(context.Background(), runtime))

			runner.reconcile(context.Background(), definition.ID)

			runtime = readRunnerRuntime(t, stores, definition.ID)
			assert.Equal(t, core.Aborted, runtime.Status)
			assert.Nil(t, runtime.ActiveDAGRun)
			require.NotNil(t, runtime.FinishedAt)
			require.Len(t, runtime.DAGRunRefs, 1)
			assert.Empty(t, gateway.stopped)
		})
	}
}

func TestRunnerRevalidatesParamsImmediatelyBeforeAdoption(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"run","next_state":"default","reason":"The alert can be classified.","dag":"classify","params":{"alert":"A-123"}}`)}
	runner, stores, definition := newRunnerFixture(t, provider, &runnerTestGateway{})
	resolveCount := 0
	runner.router.dags = RoutingDAGResolverFunc(func(_ context.Context, name string) (RoutingDAG, error) {
		resolveCount++
		schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["alert"],"properties":{"alert":{"type":"string"}}}`)
		if resolveCount > 1 {
			schema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["region"],"properties":{"region":{"type":"string"}}}`)
		}
		return RoutingDAG{FileName: name, ParamSchema: schema}, nil
	})

	runner.reconcile(context.Background(), definition.ID)

	runtime := readRunnerRuntime(t, stores, definition.ID)
	assert.Equal(t, core.Failed, runtime.Status)
	assert.Nil(t, runtime.ActiveDAGRun)
	require.NotNil(t, runtime.LastError)
	assert.Equal(t, "router_decision_stale", *runtime.LastError)
	require.Len(t, runtime.Context, 1)
}

func TestRunnerSerializesChildEnqueueWithStop(t *testing.T) {
	t.Parallel()

	gateway := &runnerTestGateway{
		ensureStart: make(chan struct{}),
		ensureAllow: make(chan struct{}),
	}
	runner, stores, definition := newRunnerFixture(t, &routerTestProvider{}, gateway)
	now := runnerFixtureNow
	runtime := readRunnerRuntime(t, stores, definition.ID)
	setTestActiveDAGRun(runtime, ActiveDAGRun{
		ToolCallID: "call-1", DAG: "classify", DAGRunID: "run-1", Params: json.RawMessage(`{"alert":"A-123"}`),
	})
	require.NoError(t, stores.Runtimes.Put(context.Background(), runtime))
	request := childRequest(definition, *runtime)

	enqueueDone := make(chan error, 1)
	go func() {
		enqueueDone <- runner.ensureActiveChildEnqueued(context.Background(), definition.ID, request)
	}()
	<-gateway.ensureStart

	service := NewService(
		stores.Definitions,
		stores.Runtimes,
		stores.Locker,
		NewValidator(nil),
		WithClock(func() time.Time { return now.Add(time.Minute) }),
	)
	stopDone := make(chan error, 1)
	go func() {
		_, err := service.Stop(context.Background(), definition.ID)
		stopDone <- err
	}()
	select {
	case err := <-stopDone:
		t.Fatalf("Stop completed while child enqueue held the Controller lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(gateway.ensureAllow)
	require.NoError(t, <-enqueueDone)
	require.NoError(t, <-stopDone)
	require.Len(t, gateway.ensured, 1)
	assert.Equal(t, "run-1", gateway.ensured[0].DAGRunID)

	runner.reconcile(context.Background(), definition.ID)
	require.Len(t, gateway.stopped, 1)
	assert.Equal(t, "run-1", gateway.stopped[0].DAGRunID)
}

func TestRunnerDoesNotEnqueueAfterStopWins(t *testing.T) {
	t.Parallel()

	gateway := &runnerTestGateway{}
	runner, stores, definition := newRunnerFixture(t, &routerTestProvider{}, gateway)
	runtime := readRunnerRuntime(t, stores, definition.ID)
	runtime.Status = core.Aborted
	setTestActiveDAGRun(runtime, ActiveDAGRun{
		ToolCallID: "call-1", DAG: "classify", DAGRunID: "run-1", Params: json.RawMessage(`{"alert":"A-123"}`),
	})
	require.NoError(t, stores.Runtimes.Put(context.Background(), runtime))

	require.NoError(t, runner.ensureActiveChildEnqueued(context.Background(), definition.ID, childRequest(definition, *runtime)))
	assert.Empty(t, gateway.ensured)
}

func TestExecutionEvidenceOmitsWholeEntriesToStayBounded(t *testing.T) {
	t.Parallel()

	message, err := ExecutionEvidenceMessage(
		ActiveDAGRun{ToolCallID: "call-1", DAG: "work", DAGRunID: "run-1"},
		ChildRunObservation{
			Exists: true, Status: core.Succeeded,
			Outputs: map[string]string{"a-large": strings.Repeat("x", maxEvidenceBytes), "b-small": "kept"},
		},
	)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(message.Content), maxEvidenceBytes)
	assert.True(t, json.Valid([]byte(message.Content)))
	assert.Contains(t, message.Content, `"truncated":true`)
	assert.NotContains(t, message.Content, strings.Repeat("x", 100))
}

func newRunnerFixture(t *testing.T, provider llm.Provider, gateway *runnerTestGateway) (*Runner, FileStores, Definition) {
	t.Helper()
	ctx := context.Background()
	stores := NewFileStores(t.TempDir())
	definition := routerTestDefinition()
	raw, err := MarshalDefinition(&definition)
	require.NoError(t, err)
	require.NoError(t, stores.Definitions.Create(ctx, definition.ID, raw))
	now := runnerFixtureNow
	require.NoError(t, stores.Runtimes.Put(ctx, &Runtime{
		RuntimeVersion: RuntimeVersion,
		ID:             definition.ID,
		Status:         core.Running,
		CurrentState:   DefaultStateName,
		Context:        []exec.LLMMessage{{Role: exec.RoleUser, Content: "Investigate A-123."}},
		StartedAt:      now,
		UpdatedAt:      now,
	}))
	router := NewRouter(RouterProviderFactoryFunc(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return provider, nil
	}), RoutingDAGResolverFunc(func(_ context.Context, name string) (RoutingDAG, error) {
		return RoutingDAG{
			FileName:  name,
			ParamDefs: []core.ParamDef{{Name: "alert", Type: core.ParamDefTypeString, Required: true}},
		}, nil
	}))
	runner := NewRunner(
		stores.Definitions, stores.Runtimes, stores.Locker, NewValidator(nil), router, gateway,
		DAGRunIDGeneratorFunc(func(context.Context) (string, error) { return "run-1", nil }),
		WithRunnerClock(func() time.Time { return now }),
	)
	return runner, stores, definition
}

func readRunnerRuntime(t *testing.T, stores FileStores, id string) *Runtime {
	t.Helper()
	runtime, err := stores.Runtimes.Get(context.Background(), id)
	require.NoError(t, err)
	return runtime
}
