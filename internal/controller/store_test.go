// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileStoresUseDedicatedPathsAndOwnerOnlyFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	stores := NewFileStores(dataDir)
	require.NoError(t, stores.Definitions.Create(ctx, testControllerID, validPersistedYAML(testControllerID)))

	runtime := validRunningRuntime(testControllerID, time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC))
	require.NoError(t, stores.Runtimes.Put(ctx, runtime))

	definitionPath := filepath.Join(dataDir, definitionDirectoryName, testControllerID+".yaml")
	runtimePath := filepath.Join(dataDir, runtimeDirectoryName, testControllerID+".json")
	for _, path := range []string{definitionPath, runtimePath} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	definitionIDs, err := stores.Definitions.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{testControllerID}, definitionIDs)
	runtimeIDs, err := stores.Runtimes.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{testControllerID}, runtimeIDs)
}

func TestFileStoresRejectCanceledMutations(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	stores := NewFileStores(dataDir)
	ctx := context.Background()
	originalDefinition := validPersistedYAML(testControllerID)
	require.NoError(t, stores.Definitions.Create(ctx, testControllerID, originalDefinition))
	originalRuntime := validRunningRuntime(testControllerID, time.Now().UTC())
	require.NoError(t, stores.Runtimes.Put(ctx, originalRuntime))

	lockErr := errors.New("lock ownership lost")
	lockedCtx, cancel := context.WithCancelCause(ctx)
	cancel(lockErr)
	otherID := "ctrl_bbbbbbbbbbbbbbbb"
	updatedDefinition := []byte(strings.Replace(string(originalDefinition), "Incident flow", "Updated flow", 1))
	updatedRuntime := cloneRuntime(originalRuntime)
	updatedRuntime.TurnCount++

	assert.ErrorIs(t, stores.Definitions.Create(lockedCtx, otherID, validPersistedYAML(otherID)), lockErr)
	assert.ErrorIs(t, stores.Definitions.Update(lockedCtx, testControllerID, updatedDefinition), lockErr)
	assert.ErrorIs(t, stores.Definitions.Delete(lockedCtx, testControllerID), lockErr)
	assert.ErrorIs(t, stores.Runtimes.Put(lockedCtx, updatedRuntime), lockErr)
	assert.ErrorIs(t, stores.Runtimes.Delete(lockedCtx, testControllerID), lockErr)

	_, err := stores.Definitions.Get(ctx, otherID)
	assert.ErrorIs(t, err, ErrNotFound)
	storedDefinition, err := stores.Definitions.Get(ctx, testControllerID)
	require.NoError(t, err)
	assert.Equal(t, originalDefinition, storedDefinition)
	storedRuntime, err := stores.Runtimes.Get(ctx, testControllerID)
	require.NoError(t, err)
	assert.Zero(t, storedRuntime.TurnCount)
}

func TestFileRuntimeStoreFailsClosedOnCorruptRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	store := NewFileStores(dataDir).Runtimes
	dir := filepath.Join(dataDir, runtimeDirectoryName)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, testControllerID+".json"), []byte(`{"runtimeVersion":1`), 0o600))

	_, err := store.Get(ctx, testControllerID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRuntimeCorrupt)
}

func TestFileRuntimeStoreBoundsSnapshotsAndPreservesTerminalReserve(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	store := NewFileStores(dataDir).Runtimes
	dir := filepath.Join(dataDir, runtimeDirectoryName)
	path := filepath.Join(dir, testControllerID+".json")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)

	encodeAtSize := func(runtime *Runtime, size int) []byte {
		t.Helper()
		data, err := json.Marshal(runtime)
		require.NoError(t, err)
		require.Less(t, len(data), size)
		return append(data, bytes.Repeat([]byte{' '}, size-len(data))...)
	}

	unfinished := validRunningRuntime(testControllerID, now)
	require.NoError(t, os.WriteFile(path, encodeAtSize(unfinished, MaxRuntimeBytes+1), 0o600))
	_, err := store.Get(ctx, testControllerID)
	assert.ErrorIs(t, err, ErrRuntimeCorrupt)

	reserveBoundary := MaxRuntimeBytes - runtimeTerminalReserve + 1
	require.NoError(t, os.WriteFile(path, encodeAtSize(unfinished, reserveBoundary), 0o600))
	_, err = store.Get(ctx, testControllerID)
	assert.ErrorIs(t, err, ErrRuntimeCorrupt)

	terminal := validRunningRuntime(testControllerID, now)
	terminal.Status = core.Failed
	terminal.FinishedAt = &now
	errorCode := "router_error"
	terminal.LastError = &errorCode
	require.NoError(t, os.WriteFile(path, encodeAtSize(terminal, reserveBoundary), 0o600))
	stored, err := store.Get(ctx, testControllerID)
	require.NoError(t, err)
	assert.Equal(t, core.Failed, stored.Status)
}

func TestRuntimePublicProjectionRedactsRouteParams(t *testing.T) {
	t.Parallel()

	runtime := validRunningRuntime(testControllerID, time.Now().UTC())
	setTestActiveDAGRun(runtime, ActiveDAGRun{
		ToolCallID: "call-1",
		DAG:        "inspect-alert",
		Params:     json.RawMessage(`{"token":"do-not-expose"}`),
		DAGRunID:   "run-1",
	})
	runtime.Context[1].ToolCalls[0].Function.Arguments = `{"Action":"run","Next_State":"default","DAG":"inspect-alert","Reason":"Test route.","Params":{"token":"do-not-expose"}}`

	view := runtime.Public()
	require.NotNil(t, view.ActiveDAGRun)
	assert.Equal(t, "inspect-alert", view.ActiveDAGRun.DAG)
	assert.Equal(t, "run-1", view.ActiveDAGRun.DAGRunID)
	require.Len(t, view.Context, 2)
	assert.JSONEq(t,
		`{"Action":"run","Next_State":"default","DAG":"inspect-alert","Reason":"Test route."}`,
		view.Context[1].ToolCalls[0].Function.Arguments,
	)
}

func TestValidateRuntimeEnforcesLifecycleInvariants(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	runtime := validRunningRuntime(testControllerID, now)
	appendTestWait(t, runtime, RouteDecision{
		Action: "wait", NextState: DefaultStateName, Reason: "Approval is required.", Question: "Need approval",
	})
	require.NoError(t, validateRuntime(runtime))

	invalid := cloneRuntime(runtime)
	setTestActiveDAGRun(invalid, ActiveDAGRun{
		ToolCallID: "call-1",
		DAG:        "inspect-alert",
		Params:     json.RawMessage(`{}`),
		DAGRunID:   "run-1",
	})
	err := validateRuntime(invalid)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRuntimeCorrupt)

	childWait := validRunningRuntime(testControllerID, now)
	setTestActiveDAGRun(childWait, ActiveDAGRun{
		ToolCallID: "call-1",
		DAG:        "inspect-alert",
		Params:     json.RawMessage(`{}`),
		DAGRunID:   "run-1",
	})
	childWait.Status = core.Waiting
	require.NoError(t, validateRuntime(childWait))
	childWait.FinishedAt = &now
	err = validateRuntime(childWait)
	assert.ErrorIs(t, err, ErrRuntimeCorrupt)
}

func TestValidateRuntimeRejectsMalformedPersistentState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*Runtime)
	}{
		{
			name: "unknown status",
			mutate: func(runtime *Runtime) {
				runtime.Status = core.Status(99)
			},
		},
		{
			name: "unsafe DAG run ID",
			mutate: func(runtime *Runtime) {
				runtime.DAGRunRefs = []DAGRunRef{{State: "default", DAG: "inspect-alert", DAGRunID: "../run"}}
			},
		},
		{
			name: "unmatched tool result",
			mutate: func(runtime *Runtime) {
				runtime.Context = append(runtime.Context, exec.LLMMessage{Role: exec.RoleTool, ToolCallID: "missing", Content: `{}`})
			},
		},
		{
			name: "blank user prompt",
			mutate: func(runtime *Runtime) {
				runtime.Context[0].Content = " \t\n"
			},
		},
		{
			name: "oversized waiting question",
			mutate: func(runtime *Runtime) {
				appendTestWait(t, runtime, RouteDecision{
					Action: "wait", NextState: DefaultStateName, Reason: "Input is required.", Question: "Which value?",
				})
				question := strings.Repeat("q", maxQuestionRunes+1)
				runtime.WaitingQuestion = &question
			},
		},
		{
			name: "last error on active runtime",
			mutate: func(runtime *Runtime) {
				code := "router_error"
				runtime.LastError = &code
			},
		},
		{
			name: "succeeded without completion outcome",
			mutate: func(runtime *Runtime) {
				runtime.Status = core.Succeeded
				runtime.FinishedAt = &now
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := validRunningRuntime(testControllerID, now)
			test.mutate(runtime)
			err := validateRuntime(runtime)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRuntimeCorrupt)
		})
	}
}

func TestValidateRuntimeCrossChecksPendingRoute(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	base := validRunningRuntime(testControllerID, now)
	setTestActiveDAGRun(base, ActiveDAGRun{
		ToolCallID: "call-1",
		DAG:        "inspect-alert",
		Params:     json.RawMessage(`{"region":"apac"}`),
		DAGRunID:   "run-1",
	})
	require.NoError(t, validateRuntime(base))

	tests := []struct {
		name   string
		mutate func(*Runtime)
	}{
		{name: "tool call ID", mutate: func(runtime *Runtime) { runtime.ActiveDAGRun.ToolCallID = "other" }},
		{name: "state", mutate: func(runtime *Runtime) { runtime.CurrentState = "other" }},
		{name: "DAG", mutate: func(runtime *Runtime) { runtime.ActiveDAGRun.DAG = "other" }},
		{name: "params", mutate: func(runtime *Runtime) { runtime.ActiveDAGRun.Params = json.RawMessage(`{"region":"emea"}`) }},
		{name: "noncanonical params", mutate: func(runtime *Runtime) { runtime.ActiveDAGRun.Params = json.RawMessage(`{ "region": "apac" }`) }},
		{name: "noncanonical route", mutate: func(runtime *Runtime) {
			runtime.Context[1].ToolCalls[0].Function.Arguments += " "
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := cloneRuntime(base)
			test.mutate(runtime)
			assert.ErrorIs(t, validateRuntime(runtime), ErrRuntimeCorrupt)
		})
	}
}

func TestValidateRuntimeMatchesPersistedToolResults(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	t.Run("routing outcome", func(t *testing.T) {
		runtime := validRunningRuntime(testControllerID, now)
		decision := appendTestRoute(t, runtime, RouteDecision{
			Action: "wait", NextState: DefaultStateName, Reason: "Approval is required.", Question: "Continue?",
		})
		outcome, err := RoutingOutcomeMessage(decision, "")
		require.NoError(t, err)
		runtime.Context = append(runtime.Context, outcome)
		runtime.Status = core.Waiting
		runtime.WaitingQuestion = cloneStringPointer(&decision.Question)
		require.NoError(t, validateRuntime(runtime))

		mismatch := cloneRuntime(runtime)
		other := decision
		other.NextState = "other"
		mismatchedOutcome, err := RoutingOutcomeMessage(other, "")
		require.NoError(t, err)
		mismatch.Context[len(mismatch.Context)-1] = mismatchedOutcome
		assert.ErrorIs(t, validateRuntime(mismatch), ErrRuntimeCorrupt)

		mismatchedQuestion := cloneRuntime(runtime)
		question := "Use a different question?"
		mismatchedQuestion.WaitingQuestion = &question
		assert.ErrorIs(t, validateRuntime(mismatchedQuestion), ErrRuntimeCorrupt)

		mismatchedState := cloneRuntime(runtime)
		mismatchedState.CurrentState = "other"
		assert.ErrorIs(t, validateRuntime(mismatchedState), ErrRuntimeCorrupt)

		resumed := cloneRuntime(runtime)
		resumed.Context = append(resumed.Context, exec.LLMMessage{Role: exec.RoleUser, Content: "Continue."})
		resumed.Status = core.Running
		resumed.WaitingQuestion = nil
		require.NoError(t, validateRuntime(resumed))

		stopped := cloneRuntime(runtime)
		stopped.Status = core.Aborted
		stopped.WaitingQuestion = nil
		require.NoError(t, validateRuntime(stopped))

		overflowed := cloneRuntime(runtime)
		overflowed.Status = core.Failed
		overflowed.WaitingQuestion = nil
		overflowed.FinishedAt = &now
		overflowCode := "runtime_snapshot_limit"
		overflowed.LastError = &overflowCode
		require.NoError(t, validateRuntime(overflowed))

		unresumed := cloneRuntime(runtime)
		unresumed.Context = append(unresumed.Context, exec.LLMMessage{Role: exec.RoleUser, Content: "Continue."})
		assert.ErrorIs(t, validateRuntime(unresumed), ErrRuntimeCorrupt)
	})

	t.Run("completion lifecycle", func(t *testing.T) {
		runtime := validRunningRuntime(testControllerID, now)
		decision := appendTestRoute(t, runtime, RouteDecision{
			Action: "complete", NextState: "done", Reason: "Work is complete.",
		})
		outcome, err := RoutingOutcomeMessage(decision, "succeeded")
		require.NoError(t, err)
		runtime.Context = append(runtime.Context, outcome)
		runtime.CurrentState = "done"
		runtime.Status = core.Succeeded
		runtime.FinishedAt = &now
		require.NoError(t, validateRuntime(runtime))

		mismatchedStatus := cloneRuntime(runtime)
		mismatchedStatus.Status = core.Failed
		assert.ErrorIs(t, validateRuntime(mismatchedStatus), ErrRuntimeCorrupt)

		mismatchedOutcome := cloneRuntime(runtime)
		failedOutcome, err := RoutingOutcomeMessage(decision, "failed")
		require.NoError(t, err)
		mismatchedOutcome.Context[len(mismatchedOutcome.Context)-1] = failedOutcome
		assert.ErrorIs(t, validateRuntime(mismatchedOutcome), ErrRuntimeCorrupt)

		mismatchedState := cloneRuntime(runtime)
		mismatchedState.CurrentState = "other"
		assert.ErrorIs(t, validateRuntime(mismatchedState), ErrRuntimeCorrupt)

		continued := cloneRuntime(runtime)
		continued.Context = append(continued.Context, exec.LLMMessage{Role: exec.RoleUser, Content: "Continue."})
		assert.ErrorIs(t, validateRuntime(continued), ErrRuntimeCorrupt)

		failed := validRunningRuntime(testControllerID, now)
		failedDecision := appendTestRoute(t, failed, RouteDecision{
			Action: "complete", NextState: "done", Reason: "The failure condition is satisfied.",
		})
		failedOutcome, err = RoutingOutcomeMessage(failedDecision, "failed")
		require.NoError(t, err)
		failed.Context = append(failed.Context, failedOutcome)
		failed.CurrentState = "done"
		failed.Status = core.Failed
		failed.FinishedAt = &now
		require.NoError(t, validateRuntime(failed))
		code := "router_error"
		failed.LastError = &code
		assert.ErrorIs(t, validateRuntime(failed), ErrRuntimeCorrupt)
	})

	t.Run("execution evidence", func(t *testing.T) {
		runtime := validRunningRuntime(testControllerID, now)
		setTestActiveDAGRun(runtime, ActiveDAGRun{
			ToolCallID: "call-1", DAG: "inspect-alert", Params: json.RawMessage(`{}`), DAGRunID: "run-1",
		})
		active := *runtime.ActiveDAGRun
		evidence, err := ExecutionEvidenceMessage(active, ChildRunObservation{
			Status: core.Succeeded, Outputs: map[string]string{"result": "ready"},
		})
		require.NoError(t, err)
		runtime.Context = append(runtime.Context, evidence)
		runtime.ActiveDAGRun = nil
		require.NoError(t, validateRuntime(runtime))

		mismatch := cloneRuntime(runtime)
		active.DAG = "other"
		mismatchedEvidence, err := ExecutionEvidenceMessage(active, ChildRunObservation{
			Status: core.Succeeded, Outputs: map[string]string{"result": "ready"},
		})
		require.NoError(t, err)
		mismatch.Context[len(mismatch.Context)-1] = mismatchedEvidence
		assert.ErrorIs(t, validateRuntime(mismatch), ErrRuntimeCorrupt)

		mismatchedState := cloneRuntime(runtime)
		mismatchedState.CurrentState = "other"
		assert.ErrorIs(t, validateRuntime(mismatchedState), ErrRuntimeCorrupt)
	})

	t.Run("failed execution evidence", func(t *testing.T) {
		runtime := validRunningRuntime(testControllerID, now)
		setTestActiveDAGRun(runtime, ActiveDAGRun{
			ToolCallID: "call-1", DAG: "inspect-alert", Params: json.RawMessage(`{}`), DAGRunID: "run-1",
		})
		evidence, err := ExecutionEvidenceMessage(*runtime.ActiveDAGRun, ChildRunObservation{
			Status: core.Failed, Outputs: map[string]string{},
		})
		require.NoError(t, err)
		runtime.Context = append(runtime.Context, evidence)
		runtime.DAGRunRefs = append(runtime.DAGRunRefs, DAGRunRef{
			State: DefaultStateName, DAG: "inspect-alert", DAGRunID: "run-1",
		})
		runtime.ActiveDAGRun = nil
		runtime.Status = core.Failed
		runtime.FinishedAt = &now
		code := "child_dag_failed"
		runtime.LastError = &code
		require.NoError(t, validateRuntime(runtime))

		continued := cloneRuntime(runtime)
		continued.Context = append(continued.Context, exec.LLMMessage{Role: exec.RoleUser, Content: "Continue."})
		assert.ErrorIs(t, validateRuntime(continued), ErrRuntimeCorrupt)

		running := cloneRuntime(runtime)
		running.Status = core.Running
		running.FinishedAt = nil
		running.LastError = nil
		assert.ErrorIs(t, validateRuntime(running), ErrRuntimeCorrupt)

		wrongCode := cloneRuntime(runtime)
		otherCode := "child_status_invalid"
		wrongCode.LastError = &otherCode
		assert.ErrorIs(t, validateRuntime(wrongCode), ErrRuntimeCorrupt)
	})

	t.Run("active run ref", func(t *testing.T) {
		runtime := validRunningRuntime(testControllerID, now)
		setTestActiveDAGRun(runtime, ActiveDAGRun{
			ToolCallID: "call-1", DAG: "inspect-alert", Params: json.RawMessage(`{}`), DAGRunID: "run-1",
		})
		runtime.DAGRunRefs = []DAGRunRef{{State: DefaultStateName, DAG: "inspect-alert", DAGRunID: "run-1"}}
		require.NoError(t, validateRuntime(runtime))

		wrongState := cloneRuntime(runtime)
		wrongState.DAGRunRefs[0].State = "other"
		assert.ErrorIs(t, validateRuntime(wrongState), ErrRuntimeCorrupt)

		wrongDAG := cloneRuntime(runtime)
		wrongDAG.DAGRunRefs[0].DAG = "other"
		assert.ErrorIs(t, validateRuntime(wrongDAG), ErrRuntimeCorrupt)
	})

	t.Run("unresolved route", func(t *testing.T) {
		waitRuntime := validRunningRuntime(testControllerID, now)
		appendTestRoute(t, waitRuntime, RouteDecision{
			Action: "wait", NextState: DefaultStateName, Reason: "Approval is required.", Question: "Continue?",
		})
		waitRuntime.Status = core.Failed
		waitRuntime.FinishedAt = &now
		assert.ErrorIs(t, validateRuntime(waitRuntime), ErrRuntimeCorrupt)

		runRuntime := validRunningRuntime(testControllerID, now)
		setTestActiveDAGRun(runRuntime, ActiveDAGRun{
			ToolCallID: "call-1", DAG: "inspect-alert", Params: json.RawMessage(`{}`), DAGRunID: "run-1",
		})
		runRuntime.ActiveDAGRun = nil
		runRuntime.Status = core.Failed
		runRuntime.FinishedAt = &now
		code := "child_enqueue_failed"
		runRuntime.LastError = &code
		require.NoError(t, validateRuntime(runRuntime))
	})
}

func TestFileResourceLockerSerializesIndependentInstances(t *testing.T) {
	t.Parallel()

	runtimeDir := filepath.Join(t.TempDir(), runtimeDirectoryName)
	const (
		staleThreshold    = 300 * time.Millisecond
		heartbeatInterval = 20 * time.Millisecond
	)
	first := &fileResourceLocker{dir: runtimeDir, staleThreshold: staleThreshold, heartbeatInterval: heartbeatInterval}
	second := &fileResourceLocker{dir: runtimeDir, staleThreshold: staleThreshold, heartbeatInterval: heartbeatInterval}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithLock(ctx, testControllerID, func(lockedCtx context.Context) error {
			close(locked)
			select {
			case <-release:
				return nil
			case <-lockedCtx.Done():
				return context.Cause(lockedCtx)
			}
		})
	}()
	<-locked

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.WithLock(ctx, testControllerID, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()
	enteredWhileHeld := false
	select {
	case <-secondEntered:
		enteredWhileHeld = true
	case <-time.After(2 * staleThreshold):
	}
	close(release)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.False(t, enteredWhileHeld)
}

func validRunningRuntime(id string, now time.Time) *Runtime {
	return &Runtime{
		RuntimeVersion: RuntimeVersion,
		ID:             id,
		Workspace:      "ops",
		Status:         core.Running,
		CurrentState:   DefaultStateName,
		DAGRunRefs:     []DAGRunRef{},
		Context: []exec.LLMMessage{{
			Role:    exec.RoleUser,
			Content: "prompt",
		}},
		StartedAt: now,
		UpdatedAt: now,
	}
}

func appendTestRoute(t *testing.T, runtime *Runtime, decision RouteDecision) RouteDecision {
	t.Helper()
	if decision.ToolCallID == "" {
		decision.ToolCallID = "call-1"
	}
	runtime.Context = append(runtime.Context, testRouteMessage(t, decision))
	return decision
}

func testRouteMessage(t *testing.T, decision RouteDecision) exec.LLMMessage {
	t.Helper()
	arguments, err := json.Marshal(routeArgumentsFromDecision(decision))
	require.NoError(t, err)
	return exec.LLMMessage{
		Role: exec.RoleAssistant,
		ToolCalls: []exec.ToolCall{{
			ID: decision.ToolCallID, Type: "function",
			Function: exec.ToolCallFunction{Name: routeToolName, Arguments: string(arguments)},
		}},
	}
}

func appendTestWait(t *testing.T, runtime *Runtime, decision RouteDecision) RouteDecision {
	t.Helper()
	decision = appendTestRoute(t, runtime, decision)
	outcome, err := RoutingOutcomeMessage(decision, "")
	require.NoError(t, err)
	runtime.Context = append(runtime.Context, outcome)
	runtime.CurrentState = decision.NextState
	runtime.Status = core.Waiting
	runtime.WaitingQuestion = cloneStringPointer(&decision.Question)
	runtime.TurnCount++
	return decision
}

type testRuntimeContextEnding uint8

const (
	testContextRouteReady testRuntimeContextEnding = iota
	testContextPromptWait
	testContextPendingRun
)

func fillTestRuntimeContext(t *testing.T, runtime *Runtime, targetSize int, ending testRuntimeContextEnding) {
	t.Helper()
	active := runtime.ActiveDAGRun
	if active != nil {
		copy := *active
		copy.Params = append(json.RawMessage(nil), active.Params...)
		active = &copy
	}

	build := func(prefix []exec.LLMMessage, turns int) *Runtime {
		candidate := cloneRuntime(runtime)
		candidate.Context = append([]exec.LLMMessage(nil), prefix...)
		candidate.Status = core.Running
		candidate.CurrentState = DefaultStateName
		candidate.TurnCount = turns
		candidate.WaitingQuestion = nil
		candidate.ActiveDAGRun = nil
		candidate.LastError = nil
		candidate.FinishedAt = nil

		switch ending {
		case testContextRouteReady:
		case testContextPromptWait:
			appendTestWait(t, candidate, RouteDecision{
				Action: "wait", NextState: DefaultStateName, Reason: "More input is required.",
				Question: "Need more input", ToolCallID: "call-final-wait",
			})
		case testContextPendingRun:
			require.NotNil(t, active)
			candidate.Context = append(candidate.Context, testRouteMessage(t, RouteDecision{
				Action: "run", NextState: DefaultStateName, Reason: "The DAG can run.",
				DAG: active.DAG, Params: active.Params, ToolCallID: active.ToolCallID,
			}))
			candidate.ActiveDAGRun = active
			candidate.TurnCount++
		default:
			t.Fatalf("unknown test context ending %d", ending)
		}
		return candidate
	}

	appendPadding := func(prefix []exec.LLMMessage, prompt string, turn int) []exec.LLMMessage {
		decision := RouteDecision{
			Action: "wait", NextState: DefaultStateName, Reason: "More input is required.",
			Question: "Provide more input", ToolCallID: fmt.Sprintf("call-padding-%d", turn),
		}
		result := append([]exec.LLMMessage(nil), prefix...)
		result = append(result, testRouteMessage(t, decision))
		outcome, err := RoutingOutcomeMessage(decision, "")
		require.NoError(t, err)
		return append(result, outcome, exec.LLMMessage{Role: exec.RoleUser, Content: prompt})
	}

	prefix := []exec.LLMMessage{{Role: exec.RoleUser, Content: "start"}}
	turns := 0
	best := build(prefix, turns)
	for {
		fullPrefix := appendPadding(prefix, strings.Repeat("x", maxPromptBytes), turns+1)
		full := build(fullPrefix, turns+1)
		encoded, err := json.Marshal(full)
		require.NoError(t, err)
		if len(encoded) <= targetSize {
			prefix = fullPrefix
			turns++
			best = full
			continue
		}

		low, high := 1, maxPromptBytes
		for low <= high {
			length := low + (high-low)/2
			candidate := build(appendPadding(prefix, strings.Repeat("x", length), turns+1), turns+1)
			encoded, err := json.Marshal(candidate)
			require.NoError(t, err)
			if len(encoded) <= targetSize {
				best = candidate
				low = length + 1
			} else {
				high = length - 1
			}
		}
		break
	}

	data, err := json.Marshal(best)
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), targetSize)
	require.Greater(t, len(data), targetSize-maxPromptBytes)
	*runtime = *best
}

func TestFileDefinitionStoreCreateIsExclusive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileStores(t.TempDir()).Definitions
	require.NoError(t, store.Create(ctx, testControllerID, validPersistedYAML(testControllerID)))
	err := store.Create(ctx, testControllerID, validPersistedYAML(testControllerID))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyExists))
}
