// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAndExpandStringWithReservedBindings(t *testing.T) {
	t.Parallel()

	raw := "deploy ${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}"
	staticScope := value.StaticScope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": "prod"},
		Env:    value.Values{"HOME": "/workspace"},
		Steps:  value.StepOutputContracts{"build": value.StepOutputNames{"image": {}}},
	}

	require.NoError(t, value.ValidateReferences(raw, staticScope, value.ModeWorkflowValue, "run"))

	got, err := value.ExpandString(raw, value.RuntimeScope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": "prod"},
		Env:    value.Values{"HOME": "/workspace"},
		Steps:  value.StepOutputs{"build": value.Values{"image": "repo/api:v1"}},
	}, value.ModeWorkflowValue, "run")
	require.NoError(t, err)
	assert.Equal(t, "deploy api prod /workspace repo/api:v1", got)
}

func TestValidateReferencesRejectsReservedBindingErrors(t *testing.T) {
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
		{
			name:  "InvalidStepShape",
			input: "echo ${steps.build.image}",
			scope: value.StaticScope{Steps: value.StepOutputContracts{"build": value.StepOutputNames{"image": {}}}},
			want:  "steps bindings must use",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := value.ValidateReferences(tt.input, tt.scope, value.ModeWorkflowValue, "run")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestExpandStringRejectsMalformedBinding(t *testing.T) {
	t.Parallel()

	_, err := value.ExpandString(
		"echo ${consts.service",
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
		value.ModeWorkflowValue,
		"run",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed binding")
}

func TestExpandStringLeavesEvalReferencesForEvaluator(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandString("eval ${DATA.image} and $DATA.tag", value.RuntimeScope{}, value.ModeWorkflowValue, "run")
	require.NoError(t, err)
	assert.Equal(t, "eval ${DATA.image} and $DATA.tag", got)
}

func TestExpandStringResolvesReservedBindingsWithScope(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandString(
		"${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}",
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"environment": "prod"},
			Env:    value.Values{"HOME": "/workspace"},
			Steps:  value.StepOutputs{"build": value.Values{"image": "repo/api:v1"}},
		},
		value.ModeWorkflowValue,
		"run",
	)
	require.NoError(t, err)
	assert.Equal(t, "api prod /workspace repo/api:v1", got)
}
