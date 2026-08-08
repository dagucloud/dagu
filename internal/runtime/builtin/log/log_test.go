// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package log

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestExecutorRunWritesMessageToStdout(t *testing.T) {
	t.Parallel()

	exec, err := newLog(context.Background(), ir.Step{
		ExecutorConfig: ir.ExecutorConfig{
			Type: "log",
			Config: map[string]any{
				"message": "Deploying ${ENVIRONMENT}",
			},
		},
	})
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	require.NoError(t, exec.Run(context.Background()))
	require.Equal(t, "Deploying ${ENVIRONMENT}\n", stdout.String())
}

func TestExecutorRunDoesNotDuplicateTrailingNewline(t *testing.T) {
	t.Parallel()

	exec, err := newLog(context.Background(), ir.Step{
		ExecutorConfig: ir.ExecutorConfig{
			Type: "log",
			Config: map[string]any{
				"message": "line one\nline two\n",
			},
		},
	})
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	require.NoError(t, exec.Run(context.Background()))
	require.Equal(t, "line one\nline two\n", stdout.String())
}

func TestExecutorRunStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	exec, err := newLog(context.Background(), ir.Step{
		ExecutorConfig: ir.ExecutorConfig{
			Type: "log",
			Config: map[string]any{
				"message": "Deploying ${ENVIRONMENT}",
			},
		},
	})
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = exec.Run(ctx)
	require.True(t, errors.Is(err, context.Canceled))
	require.Empty(t, stdout.String())
}

func TestNewLogRequiresMessage(t *testing.T) {
	t.Parallel()

	_, err := newLog(context.Background(), ir.Step{
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "log",
			Config: map[string]any{},
		},
	})
	require.ErrorContains(t, err, "message is required")
}
