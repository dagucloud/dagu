// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveWorkerSelector verifies runtime resolution of worker_selector
// keys and values, including extra ITEM entries and error cases.
func TestResolveWorkerSelector(t *testing.T) {
	t.Parallel()

	envCtx := func(vars map[string]string) context.Context {
		scope := cmnvalue.NewEnvScope(nil, false)
		if len(vars) > 0 {
			scope = scope.WithEntries(vars, cmnvalue.EnvSourceDAGEnv)
		}
		return runtime.WithEnv(context.Background(), runtime.Env{Scope: scope})
	}

	t.Run("NilSelector", func(t *testing.T) {
		t.Parallel()
		resolved, err := resolveWorkerSelector(envCtx(nil), nil, nil)
		require.NoError(t, err)
		assert.Nil(t, resolved)
	})

	t.Run("ResolvesFromEnvScope", func(t *testing.T) {
		t.Parallel()
		ctx := envCtx(map[string]string{"WORKLOAD": "intraday"})
		resolved, err := resolveWorkerSelector(ctx, map[string]string{"workload": "${WORKLOAD}"}, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"workload": "intraday"}, resolved)
	})

	t.Run("ResolvesKeys", func(t *testing.T) {
		t.Parallel()
		ctx := envCtx(map[string]string{"LABEL_KEY": "region"})
		resolved, err := resolveWorkerSelector(ctx, map[string]string{"${LABEL_KEY}": "eu-west"}, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"region": "eu-west"}, resolved)
	})

	t.Run("ExtraEntriesWin", func(t *testing.T) {
		t.Parallel()
		ctx := envCtx(map[string]string{"ITEM": "from-env"})
		resolved, err := resolveWorkerSelector(ctx,
			map[string]string{"workload": "${ITEM}"},
			map[string]string{"ITEM": "item-1"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"workload": "item-1"}, resolved)
	})

	t.Run("UndefinedVariableStaysLiteral", func(t *testing.T) {
		t.Parallel()
		resolved, err := resolveWorkerSelector(envCtx(nil),
			map[string]string{"workload": "${UNDEFINED_WORKER_SELECTOR_VAR}"}, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"workload": "${UNDEFINED_WORKER_SELECTOR_VAR}"}, resolved)
	})

	t.Run("LiteralValuesUnchanged", func(t *testing.T) {
		t.Parallel()
		selector := map[string]string{"gpu": "true", "memory": "64G"}
		resolved, err := resolveWorkerSelector(envCtx(nil), selector, nil)
		require.NoError(t, err)
		assert.Equal(t, selector, resolved)
	})

	t.Run("RejectsEmptyResolvedKey", func(t *testing.T) {
		t.Parallel()
		ctx := envCtx(map[string]string{"LABEL_KEY": "  "})
		_, err := resolveWorkerSelector(ctx, map[string]string{"${LABEL_KEY}": "value"}, nil)
		require.ErrorContains(t, err, "resolved to an empty key")
	})

	t.Run("RejectsDuplicateResolvedKeys", func(t *testing.T) {
		t.Parallel()
		ctx := envCtx(map[string]string{"LABEL_KEY": "region"})
		_, err := resolveWorkerSelector(ctx, map[string]string{
			"${LABEL_KEY}": "eu-west",
			"region":       "us-east",
		}, nil)
		require.ErrorContains(t, err, `duplicate key "region"`)
	})
}

func TestEffectiveWorkerSelector(t *testing.T) {
	t.Parallel()

	t.Run("StepSelectorWins", func(t *testing.T) {
		t.Parallel()
		childDAG := &core.DAG{Name: "child", YamlData: []byte("::not yaml::"), WorkerSelector: map[string]string{"host": "${FACILITY}"}}
		sel, err := effectiveWorkerSelector(context.Background(),
			core.Step{WorkerSelector: map[string]string{"role": "gpu"}}, childDAG, executor.RunParams{})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"role": "gpu"}, sel)
	})

	t.Run("NilChildReturnsNil", func(t *testing.T) {
		t.Parallel()
		sel, err := effectiveWorkerSelector(context.Background(), core.Step{}, nil, executor.RunParams{})
		require.NoError(t, err)
		assert.Nil(t, sel)
	})

	t.Run("ChildWithoutSelectorReturnsNil", func(t *testing.T) {
		t.Parallel()
		childDAG := &core.DAG{Name: "child", YamlData: []byte("::not yaml::")}
		sel, err := effectiveWorkerSelector(context.Background(), core.Step{}, childDAG, executor.RunParams{Params: "FACILITY=serverB"})
		require.NoError(t, err)
		assert.Nil(t, sel)
	})

	t.Run("FallbackReturnsChildSelectorWithoutReload", func(t *testing.T) {
		t.Parallel()
		childDAG := &core.DAG{
			Name:           "child",
			YamlData:       []byte("::not yaml::"),
			WorkerSelector: map[string]string{"host": "serverA"},
		}
		sel, err := effectiveWorkerSelector(
			context.Background(),
			core.Step{},
			childDAG,
			executor.RunParams{Params: "FACILITY=serverB"},
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"host": "serverA"}, sel)
	})

	t.Run("DynamicFallbackRequiresStepSelector", func(t *testing.T) {
		t.Parallel()
		childDAG := &core.DAG{WorkerSelector: map[string]string{"host": "${FACILITY}"}}
		_, err := effectiveWorkerSelector(
			context.Background(),
			core.Step{},
			childDAG,
			executor.RunParams{Params: "FACILITY=serverB"},
		)
		require.ErrorContains(t, err, "sub-DAG worker_selector must be literal")
	})
}
