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
			dag: dagWithValueRun(
				map[string]any{"service": "api"},
				"printf '%s' ${consts.service} ${params.environment} ${steps.build.outputs.image}",
			),
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
			name:      "RejectsUnqualifiedReferenceWhenConstsArePresent",
			dag:       dagWithValueRun(map[string]any{"service": "api"}, "echo ${environment}"),
			errChecks: []string{"unqualified", "params.environment"},
		},
		{
			name:      "RejectsUnqualifiedReferenceWhenSupportedNamespaceIsPresent",
			dag:       dagWithValueRun(nil, "echo ${params.environment} ${environment}"),
			errChecks: []string{"unqualified", "params.environment"},
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
			errChecks: []string{"unqualified", "unknown consts reference"},
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
