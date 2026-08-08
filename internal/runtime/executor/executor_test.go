// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestUnregisterExecutorRemovesRegistration(t *testing.T) {
	const executorType = "unregister-test"
	validationErr := errors.New("validation failed")

	RegisterExecutor(
		executorType,
		func(context.Context, ir.Step) (Executor, error) { return nil, nil },
		func(ir.Step) error { return validationErr },
		registry.ExecutorCapabilities{Command: true},
	)
	t.Cleanup(func() { UnregisterExecutor(executorType) })

	step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: executorType}}
	require.True(t, registry.IsExecutorRegistered(executorType))
	require.ErrorIs(t, registry.ValidateStep(step), validationErr)

	UnregisterExecutor(executorType)

	require.False(t, registry.IsExecutorRegistered(executorType))
	require.NoError(t, registry.ValidateStep(step))
	_, err := NewExecutor(context.Background(), step)
	require.ErrorContains(t, err, `action "unregister-test" is not registered`)
}
