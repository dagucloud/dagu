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

	t.Run("const shorthand is ordinary content", func(t *testing.T) {
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

		require.NoError(t, core.ValidateSteps(dag))
	})
}

func TestValidateStepsHandlesParamsAndFutureStrictNamespaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dag  *core.DAG
	}{
		{
			name: "ParamReference",
			dag: &core.DAG{
				ParamDefs: []core.ParamDef{
					{Name: "environment", Type: core.ParamDefTypeString},
				},
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

func TestValidateStepsTreatsTemplateRunAsLiteral(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Steps: []core.Step{
			{
				Name:           "render",
				ExecutorConfig: core.ExecutorConfig{Type: "template"},
				Script:         "{{ .Value }} ${consts.missing} ${missing.output.value} `printf keep`",
			},
		},
	}

	require.NoError(t, core.ValidateSteps(dag))
}

func TestValidateStepsCoversRuntimeResolvedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step core.Step
	}{
		{
			name: "RetryLimitString",
			step: core.Step{
				Name:           "retry",
				ExecutorConfig: valueReferenceTestExec,
				RetryPolicy: core.RetryPolicy{
					LimitStr: "${consts.missing}",
				},
			},
		},
		{
			name: "RepeatMaxIntervalString",
			step: core.Step{
				Name:           "repeat",
				ExecutorConfig: valueReferenceTestExec,
				RepeatPolicy: core.RepeatPolicy{
					MaxIntervalStr: "${consts.missing}",
				},
			},
		},
		{
			name: "SubDAGName",
			step: core.Step{
				Name:           "subdag-name",
				ExecutorConfig: valueReferenceTestExec,
				SubDAG:         &core.SubDAG{Name: "${consts.missing}"},
			},
		},
		{
			name: "SubDAGParams",
			step: core.Step{
				Name:           "subdag-params",
				ExecutorConfig: valueReferenceTestExec,
				SubDAG:         &core.SubDAG{Params: "IMAGE=${consts.missing}"},
			},
		},
		{
			name: "MessageContent",
			step: core.Step{
				Name:           "message",
				ExecutorConfig: valueReferenceTestExec,
				Messages: []core.LLMMessage{
					{Role: core.LLMRoleUser, Content: "${consts.missing}"},
				},
			},
		},
		{
			name: "LLMSystem",
			step: core.Step{
				Name:           "llm-system",
				ExecutorConfig: valueReferenceTestExec,
				LLM:            &core.LLMConfig{System: "${consts.missing}"},
			},
		},
		{
			name: "LLMModelEntryBaseURL",
			step: core.Step{
				Name:           "llm-model-base-url",
				ExecutorConfig: valueReferenceTestExec,
				LLM: &core.LLMConfig{
					Models: []core.ModelEntry{
						{BaseURL: "${consts.missing}"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := core.ValidateSteps(&core.DAG{
				Consts: map[string]any{"service": "api"},
				Steps:  []core.Step{tt.step},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown consts binding")
		})
	}
}

func TestDAGValidateCoversRetryRepeatOutputReferences(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name: "output-ref-dag",
		Steps: []core.Step{
			{
				Name:           "build",
				ExecutorConfig: valueReferenceTestExec,
				StructuredOutput: map[string]core.StepOutputEntry{
					"image": {HasValue: true, Value: "repo/api"},
				},
			},
			{
				Name:           "retry",
				ExecutorConfig: valueReferenceTestExec,
				RetryPolicy: core.RetryPolicy{
					LimitStr: "${build.output.missing}",
				},
			},
			{
				Name:           "repeat",
				ExecutorConfig: valueReferenceTestExec,
				RepeatPolicy: core.RepeatPolicy{
					IntervalStr: "${build.output.missing}",
				},
			},
		},
	}

	err := dag.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build")
	assert.Contains(t, err.Error(), "missing")
}
