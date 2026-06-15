// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAndExpandStringWithConstBinding(t *testing.T) {
	t.Parallel()

	raw := "deploy ${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}"
	staticScope := value.StaticScope{
		Consts: value.Values{"service": "api"},
	}

	require.NoError(t, value.ValidateReferencesForTest(raw, staticScope, value.ModeWorkflowValueForTest, "run"))

	got, err := value.ExpandStringForTest(raw, value.RuntimeScope{
		Consts: value.Values{"service": "api"},
	}, value.ModeWorkflowValueForTest, "run")
	require.NoError(t, err)
	assert.Equal(t, "deploy api ${params.environment} ${env.HOME} ${steps.build.outputs.image}", got)
}

func TestValidateReferencesRejectsConstBindingErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		scope value.StaticScope
		want  string
	}{
		{
			name:  "ReservedShorthand",
			input: "echo $consts.service",
			scope: value.StaticScope{Consts: value.Values{"service": "api"}},
			want:  "invalid binding shorthand",
		},
		{
			name:  "MissingConst",
			input: "echo ${consts.missing}",
			scope: value.StaticScope{Consts: value.Values{"service": "api"}},
			want:  "unknown consts binding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := value.ValidateReferencesForTest(tt.input, tt.scope, value.ModeWorkflowValueForTest, "run")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestExpandStringRejectsMalformedBinding(t *testing.T) {
	t.Parallel()

	_, err := value.ExpandStringForTest(
		"echo ${consts.service",
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
		value.ModeWorkflowValueForTest,
		"run",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed binding")
}

func TestExpandStringLeavesEvalReferencesForEvaluator(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandStringForTest("eval ${DATA.image} and $DATA.tag", value.RuntimeScope{}, value.ModeWorkflowValueForTest, "run")
	require.NoError(t, err)
	assert.Equal(t, "eval ${DATA.image} and $DATA.tag", got)
}

func TestExpandStringResolvesConstBindingsWithScope(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandStringForTest(
		"${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}",
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Env:    testEnvScope(map[string]string{"HOME": "/workspace"}),
		},
		value.ModeWorkflowValueForTest,
		"run",
	)
	require.NoError(t, err)
	assert.Equal(t, "api ${params.environment} ${env.HOME} ${steps.build.outputs.image}", got)
}

func TestExpandStringDoesNotRejectFutureNamespaceShorthand(t *testing.T) {
	t.Parallel()

	tests := []string{
		"$env.FOO",
		"$params.foo",
		"$steps.build.outputs.image",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			got, err := value.ExpandStringForTest(raw, value.RuntimeScope{}, value.ModeWorkflowValueForTest, "run")
			require.NoError(t, err)
			assert.Equal(t, raw, got)
		})
	}
}
