// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
}
