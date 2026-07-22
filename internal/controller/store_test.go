// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestFileRuntimeStoreFailsClosedOnCorruptRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	store := NewFileRuntimeStore(dataDir)
	dir := filepath.Join(dataDir, runtimeDirectoryName)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, testControllerID+".json"), []byte(`{"runtimeVersion":1`), 0o600))

	_, err := store.Get(ctx, testControllerID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRuntimeCorrupt)
}

func TestRuntimePublicProjectionOmitsExecutableParams(t *testing.T) {
	t.Parallel()

	runtime := validRunningRuntime(testControllerID, time.Now().UTC())
	setTestActiveDAGRun(runtime, ActiveDAGRun{
		ToolCallID: "call-1",
		DAG:        "inspect-alert",
		Params:     json.RawMessage(`{"token":"do-not-expose"}`),
		DAGRunID:   "run-1",
	})

	data, err := json.Marshal(runtime.Public())
	require.NoError(t, err)
	assert.NotContains(t, string(data), "do-not-expose")
	assert.NotContains(t, string(data), `"params"`)
	assert.Contains(t, string(data), `"dagRunId":"run-1"`)
	require.Len(t, runtime.Public().Context, 2)
	assert.JSONEq(t,
		`{"action":"run","next_state":"default","dag":"inspect-alert","reason":"Test route."}`,
		runtime.Public().Context[1].ToolCalls[0].Function.Arguments,
	)
}

func TestValidateRuntimeEnforcesLifecycleInvariants(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	runtime := validRunningRuntime(testControllerID, now)
	question := "Need approval"
	runtime.Status = core.Waiting
	runtime.WaitingQuestion = &question
	require.NoError(t, ValidateRuntime(runtime))

	setTestActiveDAGRun(runtime, ActiveDAGRun{
		ToolCallID: "call-1",
		DAG:        "inspect-alert",
		Params:     json.RawMessage(`{}`),
		DAGRunID:   "run-1",
	})
	err := ValidateRuntime(runtime)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRuntimeCorrupt)

	runtime.WaitingQuestion = nil
	require.NoError(t, ValidateRuntime(runtime))
	runtime.FinishedAt = &now
	err = ValidateRuntime(runtime)
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
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			runtime := validRunningRuntime(testControllerID, now)
			test.mutate(runtime)
			err := ValidateRuntime(runtime)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRuntimeCorrupt)
		})
	}
}

func TestFileResourceLockerSerializesIndependentInstances(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	first := NewFileResourceLocker(dataDir)
	second := NewFileResourceLocker(dataDir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithLock(ctx, testControllerID, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.WithLock(ctx, testControllerID, func() error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second lock entered while the first lock was held")
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
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

func TestFileDefinitionStoreCreateIsExclusive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileDefinitionStore(t.TempDir())
	require.NoError(t, store.Create(ctx, testControllerID, validPersistedYAML(testControllerID)))
	err := store.Create(ctx, testControllerID, validPersistedYAML(testControllerID))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyExists))
}
