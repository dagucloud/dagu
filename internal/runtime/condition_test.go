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

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/require"
)

func newTestContext() context.Context {
	ctx := context.Background()
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, core.Step{}))
}

func TestEvalConditions(t *testing.T) {
	tests := []struct {
		name                string
		conditions          []*core.Condition
		wantErr             bool
		wantConditionNotMet bool // true if error should be ErrConditionNotMet
		notConditionNotMet  bool // true if error should NOT be ErrConditionNotMet
	}{
		{
			name:       "ValueMatch",
			conditions: []*core.Condition{{Condition: "1", Expected: "1"}},
		},
		{
			name:       "EnvVar",
			conditions: []*core.Condition{{Condition: "${TEST_CONDITION}", Expected: "100"}},
		},
		{
			name: "MultipleCond",
			conditions: []*core.Condition{
				{Condition: "1", Expected: "1"},
				{Condition: "100", Expected: "100"},
			},
		},
		{
			name: "MultipleCondOneMet",
			conditions: []*core.Condition{
				{Condition: "1", Expected: "1"},
				{Condition: "100", Expected: "1"},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name:       "CommandResultMet",
			conditions: []*core.Condition{{Condition: "true"}},
		},
		{
			name:                "CommandResultNotMet",
			conditions:          []*core.Condition{{Condition: "false"}},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name:       "ComplexCommand",
			conditions: []*core.Condition{{Condition: "test 1 -eq 1"}},
		},
		{
			name:       "EvenMoreComplexCommand",
			conditions: []*core.Condition{{Condition: "df / | awk 'NR==2 {exit $4 > 5000 ? 0 : 1}'"}},
		},
		{
			name:       "CommandResultTest",
			conditions: []*core.Condition{{Condition: "test 1 -eq 1"}},
		},
		{
			name:       "RegexMatch",
			conditions: []*core.Condition{{Condition: "test", Expected: "re:^test$"}},
		},
		{
			name:       "ValueMatchPreservesBacktickSubstitution",
			conditions: []*core.Condition{{Condition: "`printf 100`", Expected: "`printf 100`"}},
		},
		// Negate tests
		{
			name: "NegateMatchingCondition",
			conditions: []*core.Condition{
				{Condition: "success", Expected: "success", Negate: true},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name: "NegateNonMatchingCondition",
			conditions: []*core.Condition{
				{Condition: "failure", Expected: "success", Negate: true},
			},
		},
		{
			name: "NegateCommandSuccess",
			conditions: []*core.Condition{
				{Condition: "true", Negate: true},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name: "NegateCommandFailure",
			conditions: []*core.Condition{
				{Condition: "false", Negate: true},
			},
		},
		{
			name: "NegateEnvVar",
			conditions: []*core.Condition{
				{Condition: "${TEST_CONDITION}", Expected: "wrong_value", Negate: true},
			},
		},
		{
			name: "NegateEnvVarMatching",
			conditions: []*core.Condition{
				{Condition: "${TEST_CONDITION}", Expected: "100", Negate: true},
			},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		// Error handling tests
		{
			name: "EvalStringErrorNotSwallowed",
			conditions: []*core.Condition{
				{
					Condition: "${consts.missing}",
					Expected:  "anything",
					Negate:    true,
				},
			},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			name: "CommandNotFoundInvertedToSuccess",
			conditions: []*core.Condition{
				{
					Condition: "/nonexistent/path/to/command_xyz_123_abc",
					Negate:    true,
				},
			},
		},
		{
			name: "FalseCommandInvertedToSuccess",
			conditions: []*core.Condition{
				{
					Condition: "false",
					Negate:    true,
				},
			},
		},
		// Environment variable passthrough tests
		{
			name:       "CommandWithDAGEnvVars",
			conditions: []*core.Condition{{Condition: "test ${TEST_CONDITION} -eq 100"}},
		},
		{
			name:                "CommandWithDAGEnvVarsNotMet",
			conditions:          []*core.Condition{{Condition: "test ${TEST_CONDITION} -eq 999"}},
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
			err := runtime.EvalConditions(ctx, []string{"sh"}, tt.conditions)

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

func TestEvalConditions_ShellWithDuplicateCFlag(t *testing.T) {
	ctx := newTestContext()
	// Shell already includes -c; should not get doubled
	err := runtime.EvalConditions(ctx, []string{"sh", "-c"}, []*core.Condition{
		{Condition: "true"},
	})
	require.NoError(t, err)
}

func TestEvalConditions_NilShell(t *testing.T) {
	ctx := newTestContext()
	// With nil shell, OnlyReplaceVars should still be applied and
	// the condition should run as a direct command
	err := runtime.EvalConditions(ctx, nil, []*core.Condition{
		{Condition: "true"},
	})
	require.NoError(t, err)
}

func TestEvalConditions_DirectCommandPreservesBacktickSubstitution(t *testing.T) {
	ctx := newTestContext()

	err := runtime.EvalConditions(ctx, nil, []*core.Condition{
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

	err = runtime.EvalConditions(ctx, []string{"sh"}, []*core.Condition{
		{Condition: "test -f $TEST_FILE"},
	})
	require.NoError(t, err)
}
