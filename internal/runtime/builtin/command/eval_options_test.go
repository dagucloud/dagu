// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	goruntime "runtime"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/require"
)

func getEvalOptions(t *testing.T, step core.Step) *cmnvalue.Options {
	t.Helper()

	ctx := runtime.NewContextForTest(context.Background(), &core.DAG{Name: "test-dag"}, "run-1", "test.log")
	env := runtime.NewEnv(ctx, step)
	ctx = runtime.WithEnv(ctx, env)

	opts := cmnvalue.NewOptions()
	for _, opt := range step.CommandEvalOptions(ctx) {
		opt(opts)
	}

	return opts
}

func TestCommandExecutor_GetEvalOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		shell         string
		wantExpandEnv bool
		wantExpandOS  bool
		wantEscape    bool
	}{
		{
			name:          "EmptyShellUsesRuntimeDefault",
			shell:         "",
			wantExpandEnv: goruntime.GOOS == "windows",
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "DirectShellUsesOSExpansion",
			shell:         "direct",
			wantExpandEnv: true,
			wantExpandOS:  true,
			wantEscape:    true,
		},
		{
			name:          "ShDisablesDaguEnvExpansion",
			shell:         "/bin/sh",
			wantExpandEnv: false,
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "BashDisablesDaguEnvExpansion",
			shell:         "/bin/bash",
			wantExpandEnv: false,
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "ZshDisablesDaguEnvExpansion",
			shell:         "/bin/zsh",
			wantExpandEnv: false,
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "FishKeepsDaguEnvExpansion",
			shell:         "fish",
			wantExpandEnv: true,
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "NuKeepsDaguEnvExpansion",
			shell:         "nu",
			wantExpandEnv: true,
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "PowerShellKeepsDaguEnvExpansion",
			shell:         "powershell",
			wantExpandEnv: true,
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "PwshKeepsDaguEnvExpansion",
			shell:         "pwsh",
			wantExpandEnv: true,
			wantExpandOS:  false,
			wantEscape:    false,
		},
		{
			name:          "CmdKeepsDaguEnvExpansion",
			shell:         "cmd.exe",
			wantExpandEnv: true,
			wantExpandOS:  false,
			wantEscape:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			step := core.Step{
				Shell:          tt.shell,
				ExecutorConfig: core.ExecutorConfig{Type: "command"},
			}
			opts := getEvalOptions(t, step)

			require.Equal(t, tt.wantExpandEnv, opts.ExpandEnv)
			require.Equal(t, tt.wantExpandOS, opts.ExpandOS)
			require.Equal(t, tt.wantEscape, opts.EscapeDollar)
			require.False(t, opts.Substitute)
		})
	}
}
