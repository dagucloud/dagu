// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/require"
)

func newTestContext() context.Context {
	ctx := context.Background()
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, ir.Step{}))
}

func evalConditions(ctx context.Context, shell []string, conditions []*ir.Condition) error {
	_, err := runtime.EvaluateConditions(ctx, shell, conditions)
	return err
}

func TestEvalConditions(t *testing.T) {
	tests := []struct {
		name                string
		conditions          []*ir.Condition
		wantErr             bool
		wantConditionNotMet bool // true if error should be ErrConditionNotMet
		notConditionNotMet  bool // true if error should NOT be ErrConditionNotMet
	}{
		{
			name:       "ValueMatch",
			conditions: []*ir.Condition{{Condition: "1", Expected: "1"}},
		},
		{
			name:       "EnvVar",
			conditions: []*ir.Condition{{Condition: "${env.TEST_CONDITION}", Expected: "100"}},
		},
		{
			name: "MultipleCond",
			conditions: []*ir.Condition{
				{Condition: "1", Expected: "1"},
				{Condition: "100", Expected: "100"},
			},
		},
		{
			name: "MultipleCondOneMet",
			conditions: []*ir.Condition{
				{Condition: "1", Expected: "1"},
				{Condition: "100", Expected: "1"},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name:       "CommandResultMet",
			conditions: []*ir.Condition{{Condition: "true"}},
		},
		{
			name:                "CommandResultNotMet",
			conditions:          []*ir.Condition{{Condition: "false"}},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name:       "ComplexCommand",
			conditions: []*ir.Condition{{Condition: "test 1 -eq 1"}},
		},
		{
			name:       "EvenMoreComplexCommand",
			conditions: []*ir.Condition{{Condition: "df / | awk 'NR==2 {exit $4 > 5000 ? 0 : 1}'"}},
		},
		{
			name:       "CommandResultTest",
			conditions: []*ir.Condition{{Condition: "test 1 -eq 1"}},
		},
		{
			name:       "RegexMatch",
			conditions: []*ir.Condition{{Condition: "test", Expected: "re:^test$"}},
		},
		// Negate tests
		{
			name: "NegateMatchingCondition",
			conditions: []*ir.Condition{
				{Condition: "success", Expected: "success", Negate: true},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name: "NegateNonMatchingCondition",
			conditions: []*ir.Condition{
				{Condition: "failure", Expected: "success", Negate: true},
			},
		},
		{
			name: "NegateCommandSuccess",
			conditions: []*ir.Condition{
				{Condition: "true", Negate: true},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name: "NegateCommandFailure",
			conditions: []*ir.Condition{
				{Condition: "false", Negate: true},
			},
		},
		{
			name: "NegateEnvVar",
			conditions: []*ir.Condition{
				{Condition: "${env.TEST_CONDITION}", Expected: "wrong_value", Negate: true},
			},
		},
		{
			name: "NegateEnvVarMatching",
			conditions: []*ir.Condition{
				{Condition: "${env.TEST_CONDITION}", Expected: "100", Negate: true},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		// Error handling tests
		{
			name: "UnresolvedReferencePreservedThenEvaluated",
			conditions: []*ir.Condition{
				{
					Condition: "${consts.missing}",
					Expected:  "anything",
					Negate:    true,
				},
			},
		},
		{
			name: "CommandNotFoundInvertedToSuccess",
			conditions: []*ir.Condition{
				{
					Condition: "/nonexistent/path/to/command_xyz_123_abc",
					Negate:    true,
				},
			},
		},
		{
			name: "FalseCommandInvertedToSuccess",
			conditions: []*ir.Condition{
				{
					Condition: "false",
					Negate:    true,
				},
			},
		},
		// Environment variable passthrough tests
		{
			name:       "CommandWithDAGEnvVars",
			conditions: []*ir.Condition{{Condition: "test ${TEST_CONDITION} -eq 100"}},
		},
		{
			name:                "CommandWithDAGEnvVarsNotMet",
			conditions:          []*ir.Condition{{Condition: "test ${TEST_CONDITION} -eq 999"}},
			wantErr:             true,
			wantConditionNotMet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newTestContext()
			// Add TEST_CONDITION to the env scope (not OS env)
			env := runtime.GetEnv(ctx)
			env.Scope = env.Scope.WithEntry("TEST_CONDITION", "100", cmnvalue.EnvSourceDAGEnv)
			ctx = runtime.WithEnv(ctx, env)
			err := evalConditions(ctx, []string{"sh"}, tt.conditions)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantConditionNotMet {
					require.True(t, errors.Is(err, runtime.ErrConditionNotMet),
						"expected ErrConditionNotMet but got: %v", err)
				}
				if tt.notConditionNotMet {
					require.False(t, errors.Is(err, runtime.ErrConditionNotMet),
						"evaluation errors should not be wrapped as ErrConditionNotMet")
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestEvalConditions_ValueMatchPreservesCommandSubstitution(t *testing.T) {
	ctx := newTestContext()
	err := evalConditions(ctx, []string{"sh"}, []*ir.Condition{
		{Condition: "`printf 100`", Expected: "`printf 100`"},
		{Condition: "$(printf 200)", Expected: "$(printf 200)"},
	})
	require.NoError(t, err)

	err = evalConditions(ctx, []string{"sh"}, []*ir.Condition{
		{Condition: "`printf 100`", Expected: "100"},
	})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)
}

func TestEvalConditionsClearsErrorsWhenReevaluationSucceeds(t *testing.T) {
	ctx := newTestContext()
	conditions := []*ir.Condition{
		{Condition: "ready", Expected: "waiting"},
		{Condition: "ready", Expected: "ready"},
	}

	results, err := runtime.EvaluateConditions(ctx, nil, conditions)
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)
	require.NotEmpty(t, results[0].Error)
	require.Equal(t, runtime.ErrMsgOtherConditionNotMet, results[1].Error)

	conditions[0].Expected = "ready"
	results, err = runtime.EvaluateConditions(ctx, nil, conditions)
	require.NoError(t, err)
	require.Empty(t, results[0].Error)
	require.Empty(t, results[1].Error)
}

func TestEvalConditions_ValueMatchEvalRunsCommandSubstitution(t *testing.T) {
	ctx := newTestContext()
	err := evalConditions(ctx, []string{"sh"}, []*ir.Condition{
		{Eval: "$(printf 100)", Expected: "100"},
		{Eval: "`printf 200`", Expected: "200"},
	})
	require.NoError(t, err)

	err = evalConditions(ctx, []string{"sh"}, []*ir.Condition{
		{Eval: "$(printf 100)", Expected: "101"},
	})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)
}

func TestEvalConditions_ValueMatchEvalUsesWorkingDir(t *testing.T) {
	ctx := newTestContext()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ready.flag"), nil, 0o644))

	env := runtime.GetEnv(ctx)
	env.WorkingDir = dir
	ctx = runtime.WithEnv(ctx, env)

	err := evalConditions(ctx, []string{"sh"}, []*ir.Condition{
		{Eval: "$(test -f ready.flag && printf ready)", Expected: "ready"},
	})
	require.NoError(t, err)
}

func TestEvalConditions_ShellWithDuplicateCFlag(t *testing.T) {
	ctx := newTestContext()
	// Shell already includes -c; should not get doubled
	err := evalConditions(ctx, []string{"sh", "-c"}, []*ir.Condition{
		{Condition: "true"},
	})
	require.NoError(t, err)
}

func TestAppendShellCommandFlagUsesShellSpecificFlag(t *testing.T) {
	tests := []struct {
		name  string
		shell string
		args  []string
		want  []string
	}{
		{
			name:  "unix",
			shell: "sh",
			want:  []string{"-c"},
		},
		{
			name:  "unix existing",
			shell: "bash",
			args:  []string{"-e", "-c"},
			want:  []string{"-e", "-c"},
		},
		{
			name:  "powershell",
			shell: "powershell",
			want:  []string{"-Command"},
		},
		{
			name:  "powershell existing",
			shell: "pwsh",
			args:  []string{"-NoProfile", "-C"},
			want:  []string{"-NoProfile", "-C"},
		},
		{
			name:  "cmd",
			shell: "cmd.exe",
			want:  []string{"/c"},
		},
		{
			name:  "cmd existing",
			shell: "cmd",
			args:  []string{"/d", "/C"},
			want:  []string{"/d", "/C"},
		},
		{
			name:  "nix",
			shell: "nix-shell",
			want:  []string{"--run"},
		},
		{
			name:  "nix existing",
			shell: "nix-shell",
			args:  []string{"--pure", "--run"},
			want:  []string{"--pure", "--run"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtime.AppendShellCommandFlag(tt.shell, tt.args)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEvalConditions_NilShell(t *testing.T) {
	ctx := newTestContext()
	// With nil shell, OnlyReplaceVars should still be applied and
	// the condition should run as a direct command
	err := evalConditions(ctx, nil, []*ir.Condition{
		{Condition: "true"},
	})
	require.NoError(t, err)
}

func TestEvalConditions_DirectCommandPreservesBacktickSubstitution(t *testing.T) {
	ctx := newTestContext()

	err := evalConditions(ctx, nil, []*ir.Condition{
		{Condition: "`printf true`"},
	})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)
}

func TestEvalConditions_CommandFormExpandsHomeRelativeScopeVars(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix shell test on Windows")
	}

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	tempFile, err := os.CreateTemp(homeDir, ".dagu-condition-*")
	require.NoError(t, err)
	require.NoError(t, tempFile.Close())
	t.Cleanup(func() {
		_ = os.Remove(tempFile.Name())
	})

	ctx := newTestContext()
	env := runtime.GetEnv(ctx)
	env.Scope = env.Scope.WithEntry("TEST_FILE", "~/"+filepath.Base(tempFile.Name()), cmnvalue.EnvSourceDAGEnv)
	ctx = runtime.WithEnv(ctx, env)

	err = evalConditions(ctx, []string{"sh"}, []*ir.Condition{
		{Condition: "test -f $TEST_FILE"},
	})
	require.NoError(t, err)
}
