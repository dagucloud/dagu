// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command_test

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
)

func TestCommandResolutionWithoutRuntimeEnvUsesDAGShell(t *testing.T) {
	ctx := runtime.NewContextForTest(context.Background(), &ir.DAG{
		Name:               "test-dag",
		WorkingDir:         t.TempDir(),
		WorkingDirExplicit: true,
		Shell:              "dag-shell",
		ShellArgs:          []string{"-lc"},
	}, "run-1", "test.log")
	step := ir.Step{
		Name:           "run",
		ExecutorConfig: ir.ExecutorConfig{Type: "command"},
	}

	for _, command := range []cmnvalue.CommandContext{
		step.CommandResolution(ctx),
		step.ScriptResolution(ctx),
	} {
		assert.Equal(t, cmnvalue.CommandTargetLocal, command.Target)
		assert.True(t, command.ShellConfigured)
		assert.Equal(t, []string{"dag-shell", "-lc"}, command.Shell)
	}
}

func TestCommandResolutionWithoutRuntimeEnvPrefersStepShell(t *testing.T) {
	ctx := runtime.NewContextForTest(context.Background(), &ir.DAG{
		Name:               "test-dag",
		WorkingDir:         t.TempDir(),
		WorkingDirExplicit: true,
		Shell:              "dag-shell",
		ShellArgs:          []string{"-lc"},
	}, "run-1", "test.log")
	step := ir.Step{
		Name:           "run",
		Shell:          "step-shell",
		ShellArgs:      []string{"-c"},
		ExecutorConfig: ir.ExecutorConfig{Type: "command"},
	}

	for _, command := range []cmnvalue.CommandContext{
		step.CommandResolution(ctx),
		step.ScriptResolution(ctx),
	} {
		assert.Equal(t, cmnvalue.CommandTargetLocal, command.Target)
		assert.True(t, command.ShellConfigured)
		assert.Equal(t, []string{"step-shell", "-c"}, command.Shell)
	}
}

func TestCommandResolutionWithoutDAGContextUsesStepShell(t *testing.T) {
	step := ir.Step{
		Name:           "run",
		Shell:          "step-shell",
		ShellArgs:      []string{"-c"},
		ExecutorConfig: ir.ExecutorConfig{Type: "command"},
	}

	for _, command := range []cmnvalue.CommandContext{
		step.CommandResolution(context.Background()),
		step.ScriptResolution(context.Background()),
	} {
		assert.Equal(t, cmnvalue.CommandTargetLocal, command.Target)
		assert.True(t, command.ShellConfigured)
		assert.Equal(t, []string{"step-shell", "-c"}, command.Shell)
	}
}
