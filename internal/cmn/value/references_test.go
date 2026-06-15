// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanReferencesClassifiesReservedAndEvalRefs(t *testing.T) {
	t.Parallel()

	refs := value.ScanReferencesForTest("${consts.service} $consts.service $env.FOO $params.foo $steps.build ${DATA.image} $DATA.tag")

	require.Len(t, refs, 7)
	assert.Equal(t, value.ReferenceStrictForTest, refs[0].Kind)
	assert.Equal(t, "consts", refs[0].Namespace)
	assert.True(t, refs[0].Braced)
	assert.Equal(t, value.ReferenceInvalidForTest, refs[1].Kind)
	assert.Contains(t, refs[1].Err.Error(), "invalid binding shorthand")
	assert.Equal(t, value.ReferenceEvalForTest, refs[2].Kind)
	assert.False(t, refs[2].Braced)
	assert.Equal(t, value.ReferenceEvalForTest, refs[3].Kind)
	assert.False(t, refs[3].Braced)
	assert.Equal(t, value.ReferenceEvalForTest, refs[4].Kind)
	assert.False(t, refs[4].Braced)
	assert.Equal(t, value.ReferenceEvalForTest, refs[5].Kind)
	assert.True(t, refs[5].Braced)
	assert.Equal(t, value.ReferenceEvalForTest, refs[6].Kind)
	assert.False(t, refs[6].Braced)
}

func TestModeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode value.ModeForTest
		want string
	}{
		{mode: value.ModeConstLoadForTest, want: "const-load"},
		{mode: value.ModeStaticValidationForTest, want: "static-validation"},
		{mode: value.ModeWorkflowValueForTest, want: "workflow-value"},
		{mode: value.ModeShellCommandForTest, want: "shell-command"},
		{mode: value.ModeDirectCommandForTest, want: "direct-command"},
		{mode: value.ModeDynamicEvalForTest, want: "dynamic-eval"},
		{mode: value.ModeForTest(99), want: "mode(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.mode.String())
		})
	}
}

func TestScanReferencesMarksEvalStepOutputRefs(t *testing.T) {
	t.Parallel()

	refs := value.ScanReferencesForTest("${extract.output.user.id} $extract.output.user.id ${extract.output.bad-name}")

	require.Len(t, refs, 3)
	require.NotNil(t, refs[0].StepOutput)
	assert.Equal(t, "extract", refs[0].StepOutput.StepName)
	assert.Equal(t, []string{"user", "id"}, refs[0].StepOutput.Path)
	assert.Nil(t, refs[1].StepOutput)
	assert.Nil(t, refs[2].StepOutput)

	var outputRefs []value.StepOutputReference
	for _, ref := range refs {
		if ref.StepOutput != nil {
			outputRefs = append(outputRefs, *ref.StepOutput)
		}
	}
	require.Len(t, outputRefs, 1)
	assert.Equal(t, "${extract.output.user.id}", outputRefs[0].Expression)
}

func TestValidateReferencesModeMatrix(t *testing.T) {
	t.Parallel()

	scope := value.StaticScope{
		Consts: value.Values{"service": "api"},
	}

	tests := []struct {
		name    string
		raw     string
		mode    value.ModeForTest
		wantErr string
	}{
		{
			name: "ConstLoadAllowsConsts",
			raw:  "${consts.service}",
			mode: value.ModeConstLoadForTest,
		},
		{
			name:    "ConstLoadRejectsRuntimeNamespace",
			raw:     "${params.environment}",
			mode:    value.ModeConstLoadForTest,
			wantErr: "not available while loading consts",
		},
		{
			name:    "ReservedShorthandRejected",
			raw:     "$consts.service",
			mode:    value.ModeWorkflowValueForTest,
			wantErr: "invalid binding shorthand",
		},
		{
			name:    "UnknownConstRejected",
			raw:     "${consts.missing}",
			mode:    value.ModeStaticValidationForTest,
			wantErr: "unknown consts binding",
		},
		{
			name: "NonConstNamespacesAllowed",
			raw:  "${steps.build.outputs.digest}",
			mode: value.ModeStaticValidationForTest,
		},
		{
			name: "EvalRefsAllowed",
			raw:  "${DATA.image} $DATA.tag",
			mode: value.ModeStaticValidationForTest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := value.ValidateReferencesForTest(tt.raw, scope, tt.mode, "run")
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateReferencesIgnoresNonConstNamespaces(t *testing.T) {
	t.Parallel()

	err := value.ValidateReferencesForTest("${params.environment} ${env.RUNTIME_ONLY} ${steps.build.outputs.image}", value.StaticScope{}, value.ModeStaticValidationForTest, "run")
	require.NoError(t, err)
}

func TestExpandStringPreservesNonConstNamespaces(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandStringForTest(
		"${consts.service}:${params.environment}:${env.HOME}:${steps.build.outputs.image}",
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Env:    testEnvScope(map[string]string{"HOME": "/workspace"}),
		},
		value.ModeWorkflowValueForTest,
		"run",
	)
	require.NoError(t, err)
	assert.Equal(t, "api:${params.environment}:${env.HOME}:${steps.build.outputs.image}", got)
}

func TestExpandStringWorkflowValuePreservesCommandSubstitution(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandStringForTest("`echo resolved`", value.RuntimeScope{}, value.ModeWorkflowValueForTest, "env.VALUE")
	require.NoError(t, err)
	assert.Equal(t, "`echo resolved`", got)
}

func TestExpandStringDynamicEvalRunsCommandSubstitution(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandStringForTest("`echo resolved`", value.RuntimeScope{}, value.ModeDynamicEvalForTest, "params")
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)
}

func TestExpandStringResolvesConstRefsAndKeepsEvalRefs(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandStringForTest(
		"${consts.service}:${params.environment}:${env.HOME}:${steps.build.outputs.image}:${DATA.image}:$DATA.tag",
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Env:    testEnvScope(map[string]string{"HOME": "/workspace"}),
		},
		value.ModeWorkflowValueForTest,
		"run",
	)
	require.NoError(t, err)
	assert.Equal(t, "api:${params.environment}:${env.HOME}:${steps.build.outputs.image}:${DATA.image}:$DATA.tag", got)
}

func TestExpandObjectResolvesConstRefsAcrossNestedValues(t *testing.T) {
	t.Parallel()

	obj := map[string]any{
		"image": "${consts.repo}:${params.tag}",
		"env": []any{
			"${env.TOKEN}",
			"${steps.build.outputs.digest}",
		},
		"evalRef": "${DATA.image}",
	}

	got, err := value.ExpandObjectForTest(obj, value.RuntimeScope{
		Consts: value.Values{"repo": "repo/api"},
		Env:    testEnvScope(map[string]string{"TOKEN": "secret"}),
	}, value.ModeWorkflowValueForTest, "with")
	require.NoError(t, err)

	assert.Equal(t, "repo/api:${params.tag}", got["image"])
	assert.Equal(t, []any{"${env.TOKEN}", "${steps.build.outputs.digest}"}, got["env"])
	assert.Equal(t, "${DATA.image}", got["evalRef"])
}

func TestExpandObjectRejectsInvalidConstShorthand(t *testing.T) {
	t.Parallel()

	_, err := value.ExpandObjectForTest(
		map[string]any{"service": "$consts.service"},
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
		value.ModeWorkflowValueForTest,
		"with.service",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with.service")
	assert.Contains(t, err.Error(), "invalid binding shorthand")
}

func TestExpandStringModesApplyOwnerSemantics(t *testing.T) {
	t.Setenv("DAGU_VALUE_MODE_DIRECT", "from-os")
	scope := value.RuntimeScope{
		Consts: value.Values{"service": "api"},
		Env:    testEnvScope(map[string]string{"TOKEN": "secret"}),
	}

	tests := []struct {
		name string
		raw  string
		mode value.ModeForTest
		want string
	}{
		{
			name: "ConstLoad",
			raw:  "${consts.service}",
			mode: value.ModeConstLoadForTest,
			want: "api",
		},
		{
			name: "StaticValidation",
			raw:  "${consts.service}",
			mode: value.ModeStaticValidationForTest,
			want: "api",
		},
		{
			name: "WorkflowValueExpandsScopedEnv",
			raw:  "$TOKEN",
			mode: value.ModeWorkflowValueForTest,
			want: "secret",
		},
		{
			name: "ShellCommandPreservesShellEnv",
			raw:  "$TOKEN",
			mode: value.ModeShellCommandForTest,
			want: "$TOKEN",
		},
		{
			name: "DirectCommandExpandsScopedEnv",
			raw:  "$TOKEN",
			mode: value.ModeDirectCommandForTest,
			want: "secret",
		},
		{
			name: "DirectCommandUsesHostOnlyEnvFallback",
			raw:  "$DAGU_VALUE_MODE_DIRECT",
			mode: value.ModeDirectCommandForTest,
			want: "from-os",
		},
		{
			name: "DynamicEvalUsesDefaultExpansion",
			raw:  "$TOKEN",
			mode: value.ModeDynamicEvalForTest,
			want: "secret",
		},
		{
			name: "UnknownModeUsesConservativeExpansion",
			raw:  "$TOKEN",
			mode: value.ModeForTest(99),
			want: "secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := value.ExpandStringForTest(tt.raw, scope, tt.mode, "run")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
