// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/runtime"
)

func TestStateExecutorSetGetAndDiff(t *testing.T) {
	t.Parallel()

	stateStore := newStateStoreForTest(t)

	setOut := runStateAction(t, stateStore, opSet, map[string]any{
		"key": "cursors/api",
		"value": map[string]any{
			"last_id": 123,
		},
	})
	assertJSONField(t, setOut, "operation", opSet)
	assertJSONField(t, setOut, "version", float64(1))

	getOut := runStateAction(t, stateStore, opGet, map[string]any{
		"key": "cursors/api",
	})
	var getResult struct {
		Operation string          `json:"operation"`
		Found     bool            `json:"found"`
		Version   int64           `json:"version"`
		Value     json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal(getOut.Bytes(), &getResult))
	assert.Equal(t, opGet, getResult.Operation)
	assert.True(t, getResult.Found)
	assert.Equal(t, int64(1), getResult.Version)
	assert.JSONEq(t, `{"last_id":123}`, string(getResult.Value))

	sameOut := runStateAction(t, stateStore, opDiff, map[string]any{
		"key": "cursors/api",
		"value": map[string]any{
			"last_id": 123,
		},
	})
	var sameResult struct {
		Changed bool  `json:"changed"`
		Version int64 `json:"version"`
	}
	require.NoError(t, json.Unmarshal(sameOut.Bytes(), &sameResult))
	assert.False(t, sameResult.Changed)
	assert.Equal(t, int64(1), sameResult.Version)

	changedOut := runStateAction(t, stateStore, opDiff, map[string]any{
		"key": "cursors/api",
		"value": map[string]any{
			"last_id": 456,
		},
	})
	var changedResult struct {
		Changed         bool            `json:"changed"`
		PreviousVersion int64           `json:"previousVersion"`
		Version         int64           `json:"version"`
		Previous        json.RawMessage `json:"previous"`
		Current         json.RawMessage `json:"current"`
	}
	require.NoError(t, json.Unmarshal(changedOut.Bytes(), &changedResult))
	assert.True(t, changedResult.Changed)
	assert.Equal(t, int64(1), changedResult.PreviousVersion)
	assert.Equal(t, int64(2), changedResult.Version)
	assert.JSONEq(t, `{"last_id":123}`, string(changedResult.Previous))
	assert.JSONEq(t, `{"last_id":456}`, string(changedResult.Current))
}

func TestStateExecutorGetDefaultListAndDelete(t *testing.T) {
	t.Parallel()

	stateStore := newStateStoreForTest(t)

	defaultOut := runStateAction(t, stateStore, opGet, map[string]any{
		"key":     "missing",
		"default": "seed",
	})
	var defaultResult struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal(defaultOut.Bytes(), &defaultResult))
	assert.False(t, defaultResult.Found)
	assert.JSONEq(t, `"seed"`, string(defaultResult.Value))

	runStateAction(t, stateStore, opSet, map[string]any{"key": "cursors/api", "value": "api"})
	runStateAction(t, stateStore, opSet, map[string]any{"key": "cursors/db", "value": "db"})
	runStateAction(t, stateStore, opSet, map[string]any{"key": "tokens/api", "value": "token"})

	listOut := runStateAction(t, stateStore, opList, map[string]any{
		"prefix": "cursors/",
	})
	var listResult struct {
		Entries []struct {
			Key     string `json:"key"`
			Version int64  `json:"version"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(listOut.Bytes(), &listResult))
	require.Len(t, listResult.Entries, 2)
	assert.Equal(t, "cursors/api", listResult.Entries[0].Key)
	assert.Equal(t, "cursors/db", listResult.Entries[1].Key)

	deleteOut := runStateAction(t, stateStore, opDelete, map[string]any{
		"key": "cursors/api",
	})
	assertJSONField(t, deleteOut, "deleted", true)
}

func TestStateExecutorRequiresStateStore(t *testing.T) {
	t.Parallel()

	step := stateStep(opSet, map[string]any{"key": "cursor", "value": "x"})
	dag := &core.DAG{Name: "daily-agent"}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	_, err := newExecutor(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state store")
}

func newStateStoreForTest(t *testing.T) dagstate.Store {
	t.Helper()
	return store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state"))
}

func runStateAction(t *testing.T, stateStore dagstate.Store, op string, cfg map[string]any) *bytes.Buffer {
	t.Helper()

	exec, err := newStateExecutorForTest(t, stateStore, op, cfg)
	require.NoError(t, err)

	out := &bytes.Buffer{}
	exec.SetStdout(out)
	require.NoError(t, exec.Run(context.Background()))
	return out
}

func newStateExecutorForTest(t *testing.T, stateStore dagstate.Store, op string, cfg map[string]any) (*executorImpl, error) {
	t.Helper()

	step := stateStep(op, cfg)
	dag := &core.DAG{Name: "daily-agent"}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "", runtime.WithStateStore(stateStore))
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	exec, err := newExecutor(ctx, step)
	if err != nil {
		return nil, err
	}
	stateExec, ok := exec.(*executorImpl)
	require.True(t, ok)
	return stateExec, nil
}

func stateStep(op string, cfg map[string]any) core.Step {
	return core.Step{
		Name:     "state-step",
		Commands: []core.CommandEntry{{Command: op}},
		ExecutorConfig: core.ExecutorConfig{
			Type:   executorType,
			Config: cfg,
		},
	}
}

func assertJSONField(t *testing.T, out *bytes.Buffer, field string, want any) {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	assert.Equal(t, want, result[field])
}
