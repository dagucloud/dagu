// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandExecutorCommandResolutionUsesShellFacts(t *testing.T) {
	ctx := runtime.NewContextForTest(context.Background(), &core.DAG{Name: "test-dag"}, "run-1", "test.log")
	step := core.Step{
		Shell:          "direct",
		ExecutorConfig: core.ExecutorConfig{Type: "command"},
	}
	env := runtime.NewEnv(ctx, step)
	ctx = runtime.WithEnv(ctx, env)
	t.Setenv("DAGU_COMMAND_RESOLUTION_OS", "from-os")

	command := step.CommandResolution(ctx)
	assert.Equal(t, cmnvalue.CommandTargetLocal, command.Target)
	assert.True(t, command.ShellConfigured)
	require.Equal(t, []string{"direct"}, command.Shell)

	got, err := runtime.ResolveString(ctx, "$DAGU_COMMAND_RESOLUTION_OS", cmnvalue.DirectCommandField("command", command))
	require.NoError(t, err)
	assert.Equal(t, "from-os", got)
}
