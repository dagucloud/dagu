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
	"time"

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

		// Numeric comparison tests
		{
			name:       "NumericMatch",
			conditions: []*ir.Condition{{Condition: "0.87", Expected: "num:>=0.8"}},
		},
		{
			name:       "NumericMatchEnvVar",
			conditions: []*ir.Condition{{Condition: "${env.TEST_CONDITION}", Expected: "num:>50"}},
		},
		{
			name:                "NumericNotMet",
			conditions:          []*ir.Condition{{Condition: "0.5", Expected: "num:>=0.8"}},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name:       "NumericNotMetNegated",
			conditions: []*ir.Condition{{Condition: "0.5", Expected: "num:>=0.8", Negate: true}},
		},
		{
			name:               "NumericValueNotANumber",
			conditions:         []*ir.Condition{{Condition: "abc", Expected: "num:>=0.8"}},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			// Negation must not turn a non-numeric value into a passing gate.
			name:               "NumericValueNotANumberNegated",
			conditions:         []*ir.Condition{{Condition: "abc", Expected: "num:>=0.8", Negate: true}},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			name:               "NumericValueMultiline",
			conditions:         []*ir.Condition{{Condition: "0.5\n0.9", Expected: "num:>=0.8"}},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			name:               "NumericPatternInvalid",
			conditions:         []*ir.Condition{{Condition: "0.87", Expected: "num:==0.8"}},
			wantErr:            true,
			notConditionNotMet: true,
		},
		// A threshold may come from a value reference.
		{
			name:       "NumericThresholdReference",
			conditions: []*ir.Condition{{Condition: "0.9", Expected: "num:>=${env.TEST_THRESHOLD}"}},
		},
		{
			name:                "NumericThresholdReferenceNotMet",
			conditions:          []*ir.Condition{{Condition: "0.5", Expected: "num:>=${env.TEST_THRESHOLD}"}},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name:               "NumericThresholdReferenceNotANumber",
			conditions:         []*ir.Condition{{Condition: "0.9", Expected: "num:>=${env.TEST_NOT_NUMBER}"}},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			name:               "NumericThresholdReferenceUnresolved",
			conditions:         []*ir.Condition{{Condition: "0.9", Expected: "num:>=${env.TEST_UNDEFINED}"}},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			// An evaluation error must survive a later not-met condition,
			// otherwise the gate it belongs to silently downgrades to skipped.
			name: "EvaluationErrorOutranksLaterNotMet",
			conditions: []*ir.Condition{
				{Condition: "abc", Expected: "num:>=0.8"},
				{Condition: "x", Expected: "y"},
			},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			name: "EvaluationErrorOutranksEarlierNotMet",
			conditions: []*ir.Condition{
				{Condition: "x", Expected: "y"},
				{Condition: "abc", Expected: "num:>=0.8"},
			},
			wantErr:            true,
			notConditionNotMet: true,
		},

		// Any-of tests: one matching pattern satisfies the condition.
		{
			name:       "ExpectedAnyExactMatch",
			conditions: []*ir.Condition{{Condition: "minimal", ExpectedAny: []string{"full", "minimal"}}},
		},
		{
			name:       "ExpectedAnyRegexMatch",
			conditions: []*ir.Condition{{Condition: "apple_pie", ExpectedAny: []string{"banana", "re:^apple"}}},
		},
		{
			name:                "ExpectedAnyNoMatch",
			conditions:          []*ir.Condition{{Condition: "other", ExpectedAny: []string{"full", "re:^min"}}},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			name:       "ExpectedAnyNumericLowBand",
			conditions: []*ir.Condition{{Condition: "0.05", ExpectedAny: []string{"num:<0.1", "num:>0.9"}}},
		},
		{
			name:       "ExpectedAnyNumericHighBand",
			conditions: []*ir.Condition{{Condition: "0.95", ExpectedAny: []string{"num:<0.1", "num:>0.9"}}},
		},
		{
			name:                "ExpectedAnyNumericBetweenBands",
			conditions:          []*ir.Condition{{Condition: "0.5", ExpectedAny: []string{"num:<0.1", "num:>0.9"}}},
			wantErr:             true,
			wantConditionNotMet: true,
		},
		{
			// A numeric pattern that cannot be evaluated fails the check even
			// when another pattern matches, so the gate never half-applies.
			name:               "ExpectedAnyNumericValueNotANumber",
			conditions:         []*ir.Condition{{Condition: "abc", ExpectedAny: []string{"num:>0.9", "abc"}}},
			wantErr:            true,
			notConditionNotMet: true,
		},
		{
			name:                "ExpectedAnyNegated",
			conditions:          []*ir.Condition{{Condition: "minimal", ExpectedAny: []string{"full", "minimal"}, Negate: true}},
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
			env.Scope = env.Scope.WithEntry("TEST_THRESHOLD", "0.8", cmnvalue.EnvSourceDAGEnv)
			env.Scope = env.Scope.WithEntry("TEST_NOT_NUMBER", "abc", cmnvalue.EnvSourceDAGEnv)
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

// Passing siblings are back-filled with a message matching the deciding
// failure: an evaluation error fails the owner, so the back-fill must not
// claim another condition was not met.
func TestEvalConditions_SiblingErrorWording(t *testing.T) {
	ctx := newTestContext()

	results, err := runtime.EvaluateConditions(ctx, nil, []*ir.Condition{
		{Condition: "ok", Expected: "ok"},
		{Condition: "abc", Expected: "num:>=0.8"},
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, runtime.ErrConditionNotMet)
	require.Equal(t, runtime.ErrMsgOtherConditionFailed, results[0].Error)
	require.NotEmpty(t, results[1].Error)

	results, err = runtime.EvaluateConditions(ctx, nil, []*ir.Condition{
		{Condition: "ok", Expected: "ok"},
		{Condition: "x", Expected: "y"},
	})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)
	require.Equal(t, runtime.ErrMsgOtherConditionNotMet, results[0].Error)
	require.NotEmpty(t, results[1].Error)
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

// A command check interrupted by workflow abort or timeout is an evaluation
// error, not a not-met condition, so the owning DAG or step follows the abort
// or timeout outcome instead of skipping.
func TestEvalConditions_CommandCheckInterrupted(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("uses POSIX shell snippets")
	}

	t.Run("ShellCanceledMidCommand", func(t *testing.T) {
		ctx, cancel := context.WithCancel(newTestContext())
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()

		err := evalConditions(ctx, []string{"sh"}, []*ir.Condition{{Condition: "sleep 30"}})
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, runtime.ErrConditionNotMet)
	})

	t.Run("ShellDeadlineExceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(newTestContext(), 100*time.Millisecond)
		defer cancel()

		err := evalConditions(ctx, []string{"sh"}, []*ir.Condition{{Condition: "sleep 30"}})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NotErrorIs(t, err, runtime.ErrConditionNotMet)
	})

	t.Run("DirectCanceledMidCommand", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "sleep.sh")
		require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755))

		ctx, cancel := context.WithCancel(newTestContext())
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()

		err := evalConditions(ctx, nil, []*ir.Condition{{Condition: script}})
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, runtime.ErrConditionNotMet)
	})
}

// The exit status alone decides a command check, so a background descendant
// that outlives the check neither delays nor changes the result.
func TestEvalConditions_CommandCheckIgnoresDescendants(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("uses POSIX shell snippets")
	}

	// The deadline ends before the descendant does, so waiting on it would
	// turn the result into an interruption.
	ctx, cancel := context.WithTimeout(newTestContext(), 2*time.Second)
	defer cancel()

	err := evalConditions(ctx, []string{"sh"}, []*ir.Condition{{Condition: "sleep 5 & exit 0"}})
	require.NoError(t, err)
}

// A non-zero exit, a death by signal outside abort or timeout, and a command
// that cannot be started remain ordinary not-met conditions.
func TestEvalConditions_CommandCheckNotMet(t *testing.T) {
	ctx := newTestContext()

	err := evalConditions(ctx, []string{"sh"}, []*ir.Condition{{Condition: "exit 3"}})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)

	err = evalConditions(ctx, []string{"sh"}, []*ir.Condition{{Condition: "kill -KILL $$"}})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)

	err = evalConditions(ctx, nil, []*ir.Condition{{Condition: "./definitely-missing-condition-command"}})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)
}

// A threshold can come from a secret, and condition errors are persisted with
// the run, so no error may quote the resolved threshold.
func TestResolveNumericComparisonHidesResolvedThreshold(t *testing.T) {
	ctx := newTestContext()
	env := runtime.GetEnv(ctx)
	env.Scope = env.Scope.WithEntry("SECRET_THRESHOLD", "sensitive-value", cmnvalue.EnvSourceDAGEnv)
	ctx = runtime.WithEnv(ctx, env)

	_, err := runtime.ResolveNumericComparison(ctx, "num:>=${env.SECRET_THRESHOLD}", "expected")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sensitive-value")

	// A literal threshold is authored text, so quoting it is safe and useful.
	_, err = runtime.ResolveNumericComparison(ctx, "num:>=abc", "expected")
	require.Error(t, err)
	require.Contains(t, err.Error(), "abc")

	// A reference with no value is reported as unresolved rather than as a bad
	// number, so the cause is not mistaken for a malformed threshold.
	_, err = runtime.ResolveNumericComparison(ctx, "num:>=${env.NOT_DEFINED}", "expected")
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not resolve")
	require.NotContains(t, err.Error(), "not a number")
}

// A not-met any-of condition names every pattern as authored, never with a
// resolved threshold, since the message is persisted with the run.
func TestEvalConditionsExpectedAnyNotMetMessage(t *testing.T) {
	ctx := newTestContext()
	env := runtime.GetEnv(ctx)
	env.Scope = env.Scope.WithEntry("SECRET_THRESHOLD", "0.8", cmnvalue.EnvSourceDAGEnv)
	ctx = runtime.WithEnv(ctx, env)

	err := evalConditions(ctx, nil, []*ir.Condition{{
		Condition:   "0.5",
		ExpectedAny: []string{"num:>=${env.SECRET_THRESHOLD}", "done"},
	}})
	require.ErrorIs(t, err, runtime.ErrConditionNotMet)
	require.Contains(t, err.Error(), `"num:>=${env.SECRET_THRESHOLD}"`)
	require.Contains(t, err.Error(), `"done"`)
	require.NotContains(t, err.Error(), "0.8")
}
