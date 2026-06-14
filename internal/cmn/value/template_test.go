// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateCheckAndResolveReservedBindings(t *testing.T) {
	t.Parallel()

	tmpl := value.ParseTemplate("deploy ${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}")

	scope := value.Scope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": "prod"},
		Env:    value.Values{"HOME": "/workspace"},
		Steps: value.StepOutputs{
			"build": value.Values{"image": "repo/api:v1"},
		},
	}

	require.NoError(t, tmpl.Check(scope))

	got, err := tmpl.Resolve(scope)
	require.NoError(t, err)
	assert.Equal(t, "deploy api prod /workspace repo/api:v1", got)
}

func TestTemplateCheckRejectsReservedBindingErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		scope value.Scope
		want  string
	}{
		{
			name:  "ReservedShorthand",
			input: "echo $consts.service",
			scope: value.Scope{Consts: value.Values{"service": "api"}},
			want:  "invalid binding shorthand",
		},
		{
			name:  "MissingConst",
			input: "echo ${consts.missing}",
			scope: value.Scope{Consts: value.Values{"service": "api"}},
			want:  "unknown consts binding",
		},
		{
			name:  "InvalidStepShape",
			input: "echo ${steps.build.image}",
			scope: value.Scope{Steps: value.StepOutputs{"build": value.Values{"image": "repo/api:v1"}}},
			want:  "steps bindings must use",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := value.ParseTemplate(tt.input).Check(tt.scope)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestTemplateLeavesLegacyReferencesForExistingEvaluator(t *testing.T) {
	t.Parallel()

	tmpl := value.ParseTemplate("legacy ${DATA.image} and $DATA.tag")
	require.NoError(t, tmpl.Check(value.Scope{}))

	got, err := tmpl.Resolve(value.Scope{})
	require.NoError(t, err)
	assert.Equal(t, "legacy ${DATA.image} and $DATA.tag", got)
}

func TestExpandStringResolvesReservedBindingsWithScope(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandString(
		"${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}",
		value.Scope{
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
