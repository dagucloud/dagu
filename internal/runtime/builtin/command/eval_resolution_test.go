// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandExecutorCommandResolutionUsesShellFacts(t *testing.T) {
	ctx := runtime.NewContextForTest(context.Background(), &ir.DAG{Name: "test-dag"}, "run-1", "test.log")
	step := ir.Step{
		Shell:          "direct",
		ExecutorConfig: ir.ExecutorConfig{Type: "command"},
	}
	env := runtime.NewEnv(ctx, step)
	ctx = runtime.WithEnv(ctx, env)
	t.Setenv("DAGU_COMMAND_RESOLUTION_OS", "from-os")

	command := registry.CommandResolution(ctx, step)
	assert.Equal(t, cmnvalue.CommandTargetLocal, command.Target)
	assert.True(t, command.ShellConfigured)
	require.Equal(t, []string{"direct"}, command.Shell)

	got, err := runtime.ResolveString(ctx, "$DAGU_COMMAND_RESOLUTION_OS", cmnvalue.DirectCommandField("command", command))
	require.NoError(t, err)
	assert.Equal(t, "from-os", got)
}
