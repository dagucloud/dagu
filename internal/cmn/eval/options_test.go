// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptions_Defaults(t *testing.T) {
	opts := NewOptions()
	assert.True(t, opts.ExpandEnv, "ExpandEnv should default to true")
	assert.True(t, opts.ExpandShell, "ExpandShell should default to true")
	assert.True(t, opts.Substitute, "Substitute should default to true")
	assert.True(t, opts.EscapeDollar, "EscapeDollar should default to true")
	assert.False(t, opts.ExpandOS, "ExpandOS should default to false")
	assert.False(t, opts.DeferShellVars, "DeferShellVars should default to false")
	assert.Nil(t, opts.Variables, "Variables should default to nil")
	assert.Nil(t, opts.StepMap, "StepMap should default to nil")
}

func TestOptions_OnlyReplaceVars(t *testing.T) {
	opts := NewOptions()
	OnlyReplaceVars()(opts)

	assert.False(t, opts.ExpandEnv, "OnlyReplaceVars should disable ExpandEnv")
	assert.False(t, opts.Substitute, "OnlyReplaceVars should disable Substitute")
	assert.True(t, opts.DeferShellVars, "OnlyReplaceVars should enable DeferShellVars")
	// These should remain at their default values
	assert.True(t, opts.ExpandShell, "OnlyReplaceVars should not change ExpandShell")
	assert.True(t, opts.EscapeDollar, "OnlyReplaceVars should not change EscapeDollar")
	assert.False(t, opts.ExpandOS, "OnlyReplaceVars should not change ExpandOS")
}

func TestOptions_WithOSExpansion(t *testing.T) {
	opts := NewOptions()
	assert.False(t, opts.ExpandOS)
	WithOSExpansion()(opts)
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
		opts  []Option
		want  string
	}{
		{
			name:  "ShellExpansionEnabled",
			input: "${VAR:0:3}",
			opts:  []Option{WithVariables(map[string]string{"VAR": "HelloWorld"})},
			want:  "Hel",
		},
		{
			name:  "ShellExpansionDisabledPreservesSubstring",
			input: "${VAR:0:3}",
			opts:  []Option{WithVariables(map[string]string{"VAR": "HelloWorld"}), WithoutExpandShell()},
			want:  "${VAR:0:3}",
		},
		{
			name:  "SimpleVarStillWorks",
			input: "${VAR}",
			opts:  []Option{WithVariables(map[string]string{"VAR": "value"}), WithoutExpandShell()},
			want:  "value",
		},
		{
			name:  "EnvVarStillExpandsWithoutShellExpansion",
			input: "$TEST_VAR",
			opts:  []Option{WithoutExpandShell(), WithOSExpansion()},
			want:  "test_value_for_shell",
		},
		{
			name:  "CommandSubstitutionStillWorks",
			input: "`echo hello`",
			opts:  []Option{WithoutExpandShell()},
			want:  "hello",
		},
		{
			name:  "MixedContentWithShellDisabled",
			input: "prefix ${VAR} suffix",
			opts:  []Option{WithVariables(map[string]string{"VAR": "middle"}), WithoutExpandShell()},
			want:  "prefix middle suffix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := String(ctx, tt.input, tt.opts...)
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
		opts  []Option
		want  string
	}{
		{
			name:  "AllFeaturesDisabled",
			input: "$TEST_ENV `echo hello` ${VAR}",
			opts: []Option{
				WithoutExpandEnv(),
				WithoutSubstitute(),
			},
			want: "$TEST_ENV `echo hello` ${VAR}",
		},
		{
			name:  "OnlyVariablesEnabled",
			input: "$TEST_ENV `echo hello` ${VAR}",
			opts: []Option{
				OnlyReplaceVars(),
				WithVariables(map[string]string{"VAR": "value"}),
			},
			want: "$TEST_ENV `echo hello` value",
		},
		{
			name:  "MultipleVariableSetsWithStepMap",
			input: "${VAR1} ${VAR2} ${step1.exit_code}",
			opts: []Option{
				WithVariables(map[string]string{"VAR1": "first"}),
				WithVariables(map[string]string{"VAR2": "second"}),
				WithStepMap(map[string]StepInfo{
					"step1": {ExitCode: "0"},
				}),
			},
			want: "first second 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := String(ctx, tt.input, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
