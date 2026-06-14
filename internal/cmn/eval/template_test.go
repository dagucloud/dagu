// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eval_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateCheckAndResolveReservedBindings(t *testing.T) {
	t.Parallel()

	tmpl := eval.ParseTemplate("deploy ${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}")

	scope := eval.Scope{
		Consts: eval.Values{"service": "api"},
		Params: eval.Values{"environment": "prod"},
		Env:    eval.Values{"HOME": "/workspace"},
		Steps: eval.StepOutputs{
			"build": eval.Values{"image": "repo/api:v1"},
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
		scope eval.Scope
		want  string
	}{
		{
			name:  "ReservedShorthand",
			input: "echo $consts.service",
			scope: eval.Scope{Consts: eval.Values{"service": "api"}},
			want:  "invalid binding shorthand",
		},
		{
			name:  "MissingConst",
			input: "echo ${consts.missing}",
			scope: eval.Scope{Consts: eval.Values{"service": "api"}},
			want:  "unknown consts binding",
		},
		{
			name:  "InvalidStepShape",
			input: "echo ${steps.build.image}",
			scope: eval.Scope{Steps: eval.StepOutputs{"build": eval.Values{"image": "repo/api:v1"}}},
			want:  "steps bindings must use",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := eval.ParseTemplate(tt.input).Check(tt.scope)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestTemplateLeavesLegacyReferencesForExistingEvaluator(t *testing.T) {
	t.Parallel()

	tmpl := eval.ParseTemplate("legacy ${DATA.image} and $DATA.tag")
	require.NoError(t, tmpl.Check(eval.Scope{}))

	got, err := tmpl.Resolve(eval.Scope{})
	require.NoError(t, err)
	assert.Equal(t, "legacy ${DATA.image} and $DATA.tag", got)
}

func TestStringResolvesReservedBindingsWithScope(t *testing.T) {
	t.Parallel()

	got, err := eval.String(
		t.Context(),
		"${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}",
		eval.WithBindingScope(eval.Scope{
			Consts: eval.Values{"service": "api"},
			Params: eval.Values{"environment": "prod"},
			Env:    eval.Values{"HOME": "/workspace"},
			Steps:  eval.StepOutputs{"build": eval.Values{"image": "repo/api:v1"}},
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, "api prod /workspace repo/api:v1", got)
}
