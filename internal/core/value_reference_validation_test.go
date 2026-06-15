// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var valueReferenceTestExec = core.ExecutorConfig{Type: "test-no-validator"}

func TestValidateStepsConstReferencesUseRootConstScope(t *testing.T) {
	t.Parallel()

	t.Run("declared const is valid", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Consts: map[string]any{"service": "api"},
			Steps: []core.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${consts.service}",
				},
			},
		}

		require.NoError(t, core.ValidateSteps(dag))
	})

	t.Run("unknown const is invalid", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Consts: map[string]any{"service": "api"},
			Steps: []core.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${consts.missing}",
				},
			},
		}

		err := core.ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown consts binding")
	})

	t.Run("const shorthand is invalid", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Consts: map[string]any{"service": "api"},
			Steps: []core.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo $consts.service",
				},
			},
		}

		err := core.ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid binding shorthand")
	})
}

func TestValidateStepsIgnoresFutureStrictNamespaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dag  *core.DAG
	}{
		{
			name: "ParamReference",
			dag: &core.DAG{
				Steps: []core.Step{
					{
						Name:           "print",
						ExecutorConfig: valueReferenceTestExec,
						Script:         "echo ${params.environment}",
					},
				},
			},
		},
		{
			name: "EnvSelfAndLaterReferences",
			dag: &core.DAG{
				Env: []string{"SERVICE=${env.SERVICE}", "API_HOST=${env.LATER}", "LATER=api"},
				Steps: []core.Step{
					{
						Name:           "print",
						ExecutorConfig: valueReferenceTestExec,
						Script:         "echo ${env.SERVICE}",
					},
				},
			},
		},
		{
			name: "StepOutputReference",
			dag: &core.DAG{
				Steps: []core.Step{
					{
						Name:           "print",
						ExecutorConfig: valueReferenceTestExec,
						Script:         "echo ${steps.missing.outputs.value}",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, core.ValidateSteps(tt.dag))
		})
	}
}
