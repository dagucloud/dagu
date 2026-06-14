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

func TestValidateStepsStrictEnvReferencesUseRootEnvScope(t *testing.T) {
	t.Parallel()

	t.Run("declared root env is valid", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env: []string{"TOKEN=secret"},
			Steps: []core.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${env.TOKEN}",
				},
			},
		}

		require.NoError(t, core.ValidateSteps(dag))
	})

	t.Run("runtime-only env is valid statically", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Env: []string{"TOKEN=secret"},
			Steps: []core.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${env.MISSING}",
				},
			},
		}

		require.NoError(t, core.ValidateSteps(dag))
	})

	t.Run("declared param without runtime value is valid statically", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			ParamDefs: []core.ParamDef{
				{Name: "environment", Type: core.ParamDefTypeString, Required: true},
			},
			Steps: []core.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${params.environment}",
				},
			},
		}

		require.NoError(t, core.ValidateSteps(dag))
	})
}

func TestValidateStepsStrictReferencesInHandlers(t *testing.T) {
	t.Parallel()

	t.Run("handler can reference main step outputs", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Steps: []core.Step{
				{
					Name:           "build",
					ID:             "build",
					ExecutorConfig: valueReferenceTestExec,
					StructuredOutput: map[string]core.StepOutputEntry{
						"image": {HasValue: true, Value: "repo/api:v1"},
					},
				},
			},
			HandlerOn: core.HandlerOn{
				Success: &core.Step{
					Name:           "notify",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${steps.build.outputs.image}",
				},
			},
		}

		require.NoError(t, core.ValidateSteps(dag))
	})

	t.Run("handler cannot reference handler outputs", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Steps: []core.Step{
				{
					Name:           "build",
					ID:             "build",
					ExecutorConfig: valueReferenceTestExec,
				},
			},
			HandlerOn: core.HandlerOn{
				Success: &core.Step{
					Name:           "notify",
					ID:             "notify",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${steps.notify.outputs.message}",
					StructuredOutput: map[string]core.StepOutputEntry{
						"message": {HasValue: true, Value: "sent"},
					},
				},
			},
		}

		err := core.ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown step "notify"`)
	})
}

func TestValidateStepsRejectsEnvSelfAndLaterReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     []string
		wantErr string
	}{
		{
			name:    "SelfReference",
			env:     []string{"SERVICE=${env.SERVICE}"},
			wantErr: `env "SERVICE" cannot reference itself`,
		},
		{
			name:    "LaterReference",
			env:     []string{"API_HOST=${env.SERVICE}.internal", "SERVICE=api"},
			wantErr: `env "API_HOST" cannot reference later env "SERVICE"`,
		},
		{
			name: "EarlierReference",
			env:  []string{"SERVICE=api", "API_HOST=${env.SERVICE}.internal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag := &core.DAG{
				Env: tt.env,
				Steps: []core.Step{
					{
						Name:           "print",
						ExecutorConfig: valueReferenceTestExec,
						Script:         "echo ok",
					},
				},
			}

			err := core.ValidateSteps(dag)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
