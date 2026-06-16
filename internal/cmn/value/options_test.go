// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptions_Defaults(t *testing.T) {
	opts := newOptions()
	assert.True(t, opts.ExpandEnv, "ExpandEnv should default to true")
	assert.True(t, opts.ExpandShell, "ExpandShell should default to true")
	assert.True(t, opts.Substitute, "Substitute should default to true")
	assert.True(t, opts.EscapeDollar, "EscapeDollar should default to true")
	assert.True(t, opts.RecognizeEscapedDollar, "RecognizeEscapedDollar should default to true")
	assert.False(t, opts.ExpandOS, "ExpandOS should default to false")
	assert.False(t, opts.DeferShellVars, "DeferShellVars should default to false")
	assert.Nil(t, opts.Variables, "Variables should default to nil")
	assert.Nil(t, opts.StepMap, "StepMap should default to nil")
}

func TestOptions_OnlyReplaceVars(t *testing.T) {
	opts := newOptions()
	onlyReplaceVars()(opts)

	assert.False(t, opts.ExpandEnv, "onlyReplaceVars should disable ExpandEnv")
	assert.False(t, opts.Substitute, "onlyReplaceVars should disable Substitute")
	assert.True(t, opts.DeferShellVars, "onlyReplaceVars should enable DeferShellVars")
	// These should remain at their default values
	assert.True(t, opts.ExpandShell, "onlyReplaceVars should not change ExpandShell")
	assert.True(t, opts.EscapeDollar, "onlyReplaceVars should not change EscapeDollar")
	assert.True(t, opts.RecognizeEscapedDollar, "onlyReplaceVars should not change RecognizeEscapedDollar")
	assert.False(t, opts.ExpandOS, "onlyReplaceVars should not change ExpandOS")
}

func TestOptions_WithOSExpansion(t *testing.T) {
	opts := newOptions()
	assert.False(t, opts.ExpandOS)
	withOSExpansion()(opts)
	assert.True(t, opts.ExpandOS)
}

func TestWithoutExpandShell(t *testing.T) {
	t.Setenv("TEST_VAR", "test_value_for_shell")
	ctx := context.Background()
	if runtime.GOOS == "windows" {
		ctx = config.WithConfig(ctx, &config.Config{
			Core: config.Core{DefaultShell: "cmd"},
		})
	}

	tests := []struct {
		name  string
		input string
		opts  []option
		want  string
	}{
		{
			name:  "ShellExpansionEnabled",
			input: "${VAR:0:3}",
			opts:  []option{withVariables(map[string]string{"VAR": "HelloWorld"})},
			want:  "Hel",
		},
		{
			name:  "ShellExpansionDisabledPreservesSubstring",
			input: "${VAR:0:3}",
			opts:  []option{withVariables(map[string]string{"VAR": "HelloWorld"}), withoutExpandShell()},
			want:  "${VAR:0:3}",
		},
		{
			name:  "SimpleVarStillWorks",
			input: "${VAR}",
			opts:  []option{withVariables(map[string]string{"VAR": "value"}), withoutExpandShell()},
			want:  "value",
		},
		{
			name:  "EnvVarStillExpandsWithoutShellExpansion",
			input: "$TEST_VAR",
			opts:  []option{withoutExpandShell(), withOSExpansion()},
			want:  "test_value_for_shell",
		},
		{
			name:  "CommandSubstitutionStillWorks",
			input: "`echo hello`",
			opts:  []option{withoutExpandShell()},
			want:  "hello",
		},
		{
			name:  "MixedContentWithShellDisabled",
			input: "prefix ${VAR} suffix",
			opts:  []option{withVariables(map[string]string{"VAR": "middle"}), withoutExpandShell()},
			want:  "prefix middle suffix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evalString(ctx, tt.input, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOptions_Combinations(t *testing.T) {
	t.Setenv("TEST_ENV", "env_value")
	ctx := context.Background()

	tests := []struct {
		name  string
		input string
		opts  []option
		want  string
	}{
		{
			name:  "AllFeaturesDisabled",
			input: "$TEST_ENV `echo hello` ${VAR}",
			opts: []option{
				withoutExpandEnv(),
				withoutSubstitute(),
			},
			want: "$TEST_ENV `echo hello` ${VAR}",
		},
		{
			name:  "OnlyVariablesEnabled",
			input: "$TEST_ENV `echo hello` ${VAR}",
			opts: []option{
				onlyReplaceVars(),
				withVariables(map[string]string{"VAR": "value"}),
			},
			want: "$TEST_ENV `echo hello` value",
		},
		{
			name:  "MultipleVariableSetsWithStepMap",
			input: "${VAR1} ${VAR2} ${step1.exit_code}",
			opts: []option{
				withVariables(map[string]string{"VAR1": "first"}),
				withVariables(map[string]string{"VAR2": "second"}),
				withStepMap(map[string]StepInfo{
					"step1": {ExitCode: "0"},
				}),
			},
			want: "first second 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evalString(ctx, tt.input, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
