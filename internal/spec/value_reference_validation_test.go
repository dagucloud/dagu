// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

var valueReferenceTestExec = ir.ExecutorConfig{Type: "test-no-validator"}

func TestValidateStepsConstReferencesUseRootConstScope(t *testing.T) {
	t.Parallel()

	t.Run("declared const is valid", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{
			Consts: map[string]any{"service": "api"},
			Steps: []ir.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${consts.service}",
				},
			},
		}

		require.NoError(t, spec.ValidateSteps(dag))
	})

	t.Run("unknown const is non-fatal", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{
			Consts: map[string]any{"service": "api"},
			Steps: []ir.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${consts.missing}",
				},
			},
		}

		require.NoError(t, spec.ValidateSteps(dag))
	})

	t.Run("const shorthand is ordinary content", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{
			Consts: map[string]any{"service": "api"},
			Steps: []ir.Step{
				{
					Name:           "deploy",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo $consts.service",
				},
			},
		}

		require.NoError(t, spec.ValidateSteps(dag))
	})
}

func TestValidateStepsHandlesParamsAndFutureStrictNamespaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dag  *ir.DAG
	}{
		{
			name: "ParamReference",
			dag: &ir.DAG{
				ParamDefs: []ir.ParamDef{
					{Name: "environment", Type: ir.ParamDefTypeString},
				},
				Steps: []ir.Step{
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
			dag: &ir.DAG{
				Env: []string{"SERVICE=${env.SERVICE}", "API_HOST=${env.LATER}", "LATER=api"},
				Steps: []ir.Step{
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
			dag: &ir.DAG{
				Steps: []ir.Step{
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

			require.NoError(t, spec.ValidateSteps(tt.dag))
		})
	}
}

func TestValidateStepsTreatsTemplateRunAsLiteral(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Steps: []ir.Step{
			{
				Name:           "render",
				ExecutorConfig: ir.ExecutorConfig{Type: "template"},
				Script:         "{{ .Value }} ${consts.missing} ${missing.output.value} `printf keep`",
			},
		},
	}

	require.NoError(t, spec.ValidateSteps(dag))
}

func TestValidateStepsCoversRuntimeResolvedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step ir.Step
	}{
		{
			name: "RetryLimitString",
			step: ir.Step{
				Name:           "retry",
				ExecutorConfig: valueReferenceTestExec,
				RetryPolicy: ir.RetryPolicy{
					LimitStr: "${consts.missing}",
				},
			},
		},
		{
			name: "RepeatMaxIntervalString",
			step: ir.Step{
				Name:           "repeat",
				ExecutorConfig: valueReferenceTestExec,
				RepeatPolicy: ir.RepeatPolicy{
					MaxIntervalStr: "${consts.missing}",
				},
			},
		},
		{
			name: "SubDAGName",
			step: ir.Step{
				Name:           "subdag-name",
				ExecutorConfig: valueReferenceTestExec,
				SubDAG:         &ir.SubDAG{Name: "${consts.missing}"},
			},
		},
		{
			name: "SubDAGParams",
			step: ir.Step{
				Name:           "subdag-params",
				ExecutorConfig: valueReferenceTestExec,
				SubDAG:         &ir.SubDAG{Params: "IMAGE=${consts.missing}"},
			},
		},
		{
			name: "MessageContent",
			step: ir.Step{
				Name:           "message",
				ExecutorConfig: valueReferenceTestExec,
				Messages: []ir.LLMMessage{
					{Role: ir.LLMRoleUser, Content: "${consts.missing}"},
				},
			},
		},
		{
			name: "LLMSystem",
			step: ir.Step{
				Name:           "llm-system",
				ExecutorConfig: valueReferenceTestExec,
				LLM:            &ir.LLMConfig{System: "${consts.missing}"},
			},
		},
		{
			name: "LLMModelEntryBaseURL",
			step: ir.Step{
				Name:           "llm-model-base-url",
				ExecutorConfig: valueReferenceTestExec,
				LLM: &ir.LLMConfig{
					Models: []ir.ModelEntry{
						{BaseURL: "${consts.missing}"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := spec.ValidateSteps(&ir.DAG{
				Consts: map[string]any{"service": "api"},
				Steps:  []ir.Step{tt.step},
			})
			require.NoError(t, err)
		})
	}
}

func TestDAGValidateAllowsStepOutputReferencesInRetryRepeatPolicy(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "output-ref-dag",
		Steps: []ir.Step{
			{
				Name:           "build",
				ExecutorConfig: valueReferenceTestExec,
				StructuredOutput: map[string]ir.StepOutputEntry{
					"image": {HasValue: true, Value: "repo/api"},
				},
			},
			{
				Name:           "retry",
				ExecutorConfig: valueReferenceTestExec,
				RetryPolicy: ir.RetryPolicy{
					LimitStr: "${steps.build.outputs.missing}",
				},
			},
			{
				Name:           "repeat",
				ExecutorConfig: valueReferenceTestExec,
				RepeatPolicy: ir.RepeatPolicy{
					IntervalStr: "${steps.build.outputs.missing}",
				},
			},
		},
	}

	err := dag.Validate()
	require.NoError(t, err)
}
