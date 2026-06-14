// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStepsValueResolutionReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dag       *core.DAG
		errChecks []string
	}{
		{
			name: "AllowsNamespacedReferences",
			dag: &core.DAG{
				Consts:    map[string]any{"service": "api"},
				ParamDefs: []core.ParamDef{{Name: "environment", Type: core.ParamDefTypeString}},
				Steps: []core.Step{
					{
						ID:   "Build_1",
						Name: "build",
						Commands: []core.CommandEntry{{
							Command: "echo",
						}},
						StdoutOutputs: &core.StepOutputsConfig{
							Field: "image",
						},
					},
					{
						ID:   "deploy",
						Name: "deploy",
						Commands: []core.CommandEntry{{
							CmdWithArgs: "printf '%s' ${consts.service} ${params.environment} ${steps.Build_1.outputs.image}",
						}},
					},
				},
			},
		},
		{
			name: "ChecksLegacyCmdWithArgsField",
			dag: &core.DAG{
				Consts: map[string]any{"service": "api"},
				Steps: []core.Step{
					{
						Command:     "echo",
						Name:        "deploy",
						CmdWithArgs: "echo ${consts.service}",
						Commands:    []core.CommandEntry{{Command: "echo", CmdWithArgs: "echo ok"}},
					},
				},
			},
		},
		{
			name: "IgnoresLegacyUnqualifiedReferenceWithoutValueResolutionSurface",
			dag:  dagWithValueRun(nil, "echo ${ENVIRONMENT}"),
		},
		{
			name: "IgnoresLegacyUnqualifiedReferenceWhenConstsArePresent",
			dag:  dagWithValueRun(map[string]any{"service": "api"}, "echo ${environment}"),
		},
		{
			name: "IgnoresLegacyUnqualifiedReferenceWhenSupportedNamespaceIsPresent",
			dag: &core.DAG{
				ParamDefs: []core.ParamDef{{Name: "environment", Type: core.ParamDefTypeString}},
				Steps: []core.Step{{
					Name:     "deploy",
					Commands: []core.CommandEntry{{CmdWithArgs: "echo ${params.environment} ${environment}"}},
				}},
			},
		},
		{
			name:      "RejectsShorthandReference",
			dag:       dagWithValueRun(nil, "echo $consts.service"),
			errChecks: []string{"$consts.service", "invalid Dagu-looking reference"},
		},
		{
			name:      "RejectsUnknownConstReference",
			dag:       dagWithValueRun(map[string]any{"service": "api"}, "echo ${consts.missing}"),
			errChecks: []string{"unknown consts reference", "${consts.missing}"},
		},
		{
			name: "RejectsUndeclaredParamReference",
			dag: &core.DAG{
				ParamDefs: []core.ParamDef{{Name: "environment", Type: core.ParamDefTypeString}},
				Steps: []core.Step{{
					Name:     "deploy",
					Commands: []core.CommandEntry{{CmdWithArgs: "echo ${params.region}"}},
				}},
			},
			errChecks: []string{"undeclared params reference", "${params.region}"},
		},
		{
			name:      "RejectsUnknownNamespace",
			dag:       dagWithValueRun(nil, "echo ${vars.service}"),
			errChecks: []string{"unknown namespace", "vars"},
		},
		{
			name:      "RejectsNamespaceOnlyReference",
			dag:       dagWithValueRun(nil, "echo ${params} ${environment}"),
			errChecks: []string{"params references", "${params.<name>}"},
		},
		{
			name:      "RejectsEmptyReference",
			dag:       dagWithValueRun(nil, "echo ${}"),
			errChecks: []string{"malformed Dagu reference", "${}"},
		},
		{
			name: "AllowsLegacyOutputReferencePath",
			dag: &core.DAG{
				Consts: map[string]any{"service": "api"},
				Steps: []core.Step{
					{
						ID:   "build",
						Name: "build",
						Commands: []core.CommandEntry{{
							Command: "echo",
						}},
						StructuredOutput: map[string]core.StepOutputEntry{
							"image": {HasValue: true, Value: "v1.2.3"},
						},
					},
					{
						ID:   "deploy",
						Name: "deploy",
						Commands: []core.CommandEntry{{
							CmdWithArgs: "echo ${build.output.image}",
						}},
					},
				},
			},
		},
		{
			name:      "RejectsMalformedReference",
			dag:       dagWithValueRun(map[string]any{"service": "api"}, "echo ${consts.service"),
			errChecks: []string{"malformed Dagu reference", "${consts.service"},
		},
		{
			name:      "RejectsInvalidPathSegment",
			dag:       dagWithValueRun(map[string]any{"service": "api"}, "echo ${consts.1service}"),
			errChecks: []string{"path segment", "1service"},
		},
		{
			name:      "RejectsInvalidParamsShape",
			dag:       dagWithValueRun(nil, "echo ${params.environment.name}"),
			errChecks: []string{"params references", "${params.<name>}"},
		},
		{
			name:      "RejectsInvalidStepsShape",
			dag:       dagWithValueRun(nil, "echo ${steps.build.stdout}"),
			errChecks: []string{"steps references", "outputs"},
		},
		{
			name: "RejectsUnknownStepReference",
			dag: &core.DAG{
				Steps: []core.Step{{
					Name:     "deploy",
					Commands: []core.CommandEntry{{CmdWithArgs: "echo ${steps.build.outputs.image}"}},
				}},
			},
			errChecks: []string{"unknown steps reference", "build"},
		},
		{
			name: "RejectsUnknownDeclaredStepOutput",
			dag: &core.DAG{
				Steps: []core.Step{
					{
						ID:   "build",
						Name: "build",
						Commands: []core.CommandEntry{{
							Command: "echo",
						}},
						StdoutOutputs: &core.StepOutputsConfig{
							Field: "image_tag",
						},
					},
					{
						ID:   "deploy",
						Name: "deploy",
						Commands: []core.CommandEntry{{
							CmdWithArgs: "echo ${steps.build.outputs.image}",
						}},
					},
				},
			},
			errChecks: []string{"unknown steps output reference", "image"},
		},
		{
			name: "AllowsUnknownStepOutputWithoutDeclaredContract",
			dag: &core.DAG{
				Steps: []core.Step{
					{
						ID:   "build",
						Name: "build",
						Commands: []core.CommandEntry{{
							Command: "echo",
						}},
					},
					{
						ID:   "deploy",
						Name: "deploy",
						Commands: []core.CommandEntry{{
							CmdWithArgs: "echo ${steps.build.outputs.image}",
						}},
					},
				},
			},
		},
		{
			name: "AllowsEmptyStdoutOutputsAsUnknownContract",
			dag: &core.DAG{
				Steps: []core.Step{
					{
						ID:   "build",
						Name: "build",
						Commands: []core.CommandEntry{{
							Command: "echo",
						}},
						StdoutOutputs: &core.StepOutputsConfig{},
					},
					{
						ID:   "deploy",
						Name: "deploy",
						Commands: []core.CommandEntry{{
							CmdWithArgs: "echo ${steps.build.outputs.image}",
						}},
					},
				},
			},
		},
		{
			name: "RejectsUnknownStructuredStepOutput",
			dag: &core.DAG{
				Steps: []core.Step{
					{
						ID:   "build",
						Name: "build",
						Commands: []core.CommandEntry{{
							Command: "echo",
						}},
						StructuredOutput: map[string]core.StepOutputEntry{
							"image_tag": {HasValue: true, Value: "v1.2.3"},
						},
					},
					{
						ID:   "deploy",
						Name: "deploy",
						Commands: []core.CommandEntry{{
							CmdWithArgs: "echo ${steps.build.outputs.image}",
						}},
					},
				},
			},
			errChecks: []string{"unknown steps output reference", "image"},
		},
		{
			name: "RejectsUnknownSchemaStepOutput",
			dag: &core.DAG{
				Steps: []core.Step{
					{
						ID:   "build",
						Name: "build",
						Commands: []core.CommandEntry{{
							Command: "echo",
						}},
						OutputSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"image_tag": map[string]any{"type": "string"},
							},
							"additionalProperties": false,
						},
					},
					{
						ID:   "deploy",
						Name: "deploy",
						Commands: []core.CommandEntry{{
							CmdWithArgs: "echo ${steps.build.outputs.image}",
						}},
					},
				},
			},
			errChecks: []string{"unknown steps output reference", "image"},
		},
		{
			name: "ChecksScriptAndCommandEntries",
			dag: &core.DAG{
				Consts: map[string]any{"service": "api"},
				Steps: []core.Step{
					{
						Name:     "scripted",
						Script:   "echo ${environment}",
						Commands: []core.CommandEntry{{CmdWithArgs: "echo ${consts.missing}"}},
					},
				},
			},
			errChecks: []string{"unknown consts reference"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := core.ValidateSteps(tt.dag)
			if len(tt.errChecks) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, check := range tt.errChecks {
				assert.Contains(t, err.Error(), check)
			}
		})
	}
}

func TestValidateValueResolutionReferencesNilDAG(t *testing.T) {
	t.Parallel()

	assert.Empty(t, core.ValidateValueResolutionReferencesForTest(nil))
}

func dagWithValueRun(consts map[string]any, run string) *core.DAG {
	return &core.DAG{
		Consts: consts,
		Steps: []core.Step{
			{
				Name:     "deploy",
				Commands: []core.CommandEntry{{CmdWithArgs: run}},
			},
		},
	}
}
