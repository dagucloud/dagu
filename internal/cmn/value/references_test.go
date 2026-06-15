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

	refs := value.ScanReferences("${consts.service} $env.FOO ${DATA.image} $DATA.tag", value.ModeWorkflowValue)

	require.Len(t, refs, 4)
	assert.Equal(t, value.ReferenceStrict, refs[0].Kind)
	assert.Equal(t, "consts", refs[0].Namespace)
	assert.True(t, refs[0].Braced)
	assert.Equal(t, value.ReferenceInvalid, refs[1].Kind)
	assert.Contains(t, refs[1].Err.Error(), "invalid binding shorthand")
	assert.Equal(t, value.ReferenceEval, refs[2].Kind)
	assert.True(t, refs[2].Braced)
	assert.Equal(t, value.ReferenceEval, refs[3].Kind)
	assert.False(t, refs[3].Braced)
}

func TestModeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode value.Mode
		want string
	}{
		{mode: value.ModeConstLoad, want: "const-load"},
		{mode: value.ModeStaticValidation, want: "static-validation"},
		{mode: value.ModeWorkflowValue, want: "workflow-value"},
		{mode: value.ModeShellCommand, want: "shell-command"},
		{mode: value.ModeDirectCommand, want: "direct-command"},
		{mode: value.ModeDynamicEval, want: "dynamic-eval"},
		{mode: value.Mode(99), want: "mode(99)"},
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

	refs := value.ScanReferences("${extract.output.user.id} $extract.output.user.id ${extract.output.bad-name}", value.ModeStaticValidation)

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
		Params: value.Values{"environment": "prod"},
		Env:    value.Values{"HOME": "/workspace"},
		Steps:  value.StepOutputContracts{"build": value.StepOutputNames{"image": {}}},
	}

	tests := []struct {
		name    string
		raw     string
		mode    value.Mode
		wantErr string
	}{
		{
			name: "ConstLoadAllowsConsts",
			raw:  "${consts.service}",
			mode: value.ModeConstLoad,
		},
		{
			name:    "ConstLoadRejectsParams",
			raw:     "${params.environment}",
			mode:    value.ModeConstLoad,
			wantErr: "not available while loading consts",
		},
		{
			name:    "ReservedShorthandRejected",
			raw:     "$env.HOME",
			mode:    value.ModeWorkflowValue,
			wantErr: "invalid binding shorthand",
		},
		{
			name:    "UnknownConstRejected",
			raw:     "${consts.missing}",
			mode:    value.ModeStaticValidation,
			wantErr: "unknown consts binding",
		},
		{
			name:    "UnknownDeclaredOutputRejected",
			raw:     "${steps.build.outputs.digest}",
			mode:    value.ModeStaticValidation,
			wantErr: "unknown output",
		},
		{
			name: "EvalRefsAllowed",
			raw:  "${DATA.image} $DATA.tag",
			mode: value.ModeStaticValidation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := value.ValidateReferences(tt.raw, scope, tt.mode, "run")
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateReferencesAcceptsDeclaredParamWithoutRuntimeValue(t *testing.T) {
	t.Parallel()

	scope := value.StaticScope{
		Params: value.Values{"environment": nil},
	}

	err := value.ValidateReferences("${params.environment}", scope, value.ModeStaticValidation, "run")
	require.NoError(t, err)
}

func TestValidateReferencesAcceptsEnvReferenceWithoutStaticValue(t *testing.T) {
	t.Parallel()

	err := value.ValidateReferences("${env.RUNTIME_ONLY}", value.StaticScope{}, value.ModeStaticValidation, "run")
	require.NoError(t, err)
}

func TestExpandStringFailsMissingRuntimeEnvBinding(t *testing.T) {
	t.Parallel()

	_, err := value.ExpandString("${env.RUNTIME_ONLY}", value.RuntimeScope{}, value.ModeWorkflowValue, "run")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown env binding "RUNTIME_ONLY"`)
}

func TestExpandStringWorkflowValuePreservesCommandSubstitution(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandString("`echo resolved`", value.RuntimeScope{}, value.ModeWorkflowValue, "env.VALUE")
	require.NoError(t, err)
	assert.Equal(t, "`echo resolved`", got)
}

func TestExpandStringDynamicEvalRunsCommandSubstitution(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandString("`echo resolved`", value.RuntimeScope{}, value.ModeDynamicEval, "params")
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)
}

func TestExpandStringResolvesStrictRefsAndKeepsEvalRefs(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandString(
		"${consts.service}:${params.environment}:${env.HOME}:${steps.build.outputs.image}:${DATA.image}:$DATA.tag",
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
	assert.Equal(t, "api:prod:/workspace:repo/api:v1:${DATA.image}:$DATA.tag", got)
}

func TestExpandObjectResolvesStrictRefsAcrossNestedValues(t *testing.T) {
	t.Parallel()

	obj := map[string]any{
		"image": "${consts.repo}:${params.tag}",
		"env": []any{
			"${env.TOKEN}",
			"${steps.build.outputs.digest}",
		},
		"evalRef": "${DATA.image}",
	}

	got, err := value.ExpandObject(obj, value.RuntimeScope{
		Consts: value.Values{"repo": "repo/api"},
		Params: value.Values{"tag": "v1"},
		Env:    value.Values{"TOKEN": "secret"},
		Steps:  value.StepOutputs{"build": value.Values{"digest": "sha256:abc"}},
	}, value.ModeWorkflowValue, "with")
	require.NoError(t, err)

	assert.Equal(t, "repo/api:v1", got["image"])
	assert.Equal(t, []any{"secret", "sha256:abc"}, got["env"])
	assert.Equal(t, "${DATA.image}", got["evalRef"])
}

func TestExpandObjectRejectsInvalidReservedShorthand(t *testing.T) {
	t.Parallel()

	_, err := value.ExpandObject(
		map[string]any{"token": "$env.TOKEN"},
		value.RuntimeScope{Env: value.Values{"TOKEN": "secret"}},
		value.ModeWorkflowValue,
		"with.token",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with.token")
	assert.Contains(t, err.Error(), "invalid binding shorthand")
}

func TestExpandStringModesApplyOwnerSemantics(t *testing.T) {
	t.Setenv("DAGU_VALUE_MODE_DIRECT", "from-os")
	scope := value.RuntimeScope{
		Consts: value.Values{"service": "api"},
		Env:    value.Values{"TOKEN": "secret"},
	}

	tests := []struct {
		name string
		raw  string
		mode value.Mode
		want string
	}{
		{
			name: "ConstLoad",
			raw:  "${consts.service}",
			mode: value.ModeConstLoad,
			want: "api",
		},
		{
			name: "StaticValidation",
			raw:  "${consts.service}",
			mode: value.ModeStaticValidation,
			want: "api",
		},
		{
			name: "WorkflowValueExpandsScopedEnv",
			raw:  "$TOKEN",
			mode: value.ModeWorkflowValue,
			want: "secret",
		},
		{
			name: "ShellCommandPreservesShellEnv",
			raw:  "$TOKEN",
			mode: value.ModeShellCommand,
			want: "$TOKEN",
		},
		{
			name: "DirectCommandExpandsScopedEnv",
			raw:  "$TOKEN",
			mode: value.ModeDirectCommand,
			want: "secret",
		},
		{
			name: "DirectCommandUsesHostOnlyEnvFallback",
			raw:  "$DAGU_VALUE_MODE_DIRECT",
			mode: value.ModeDirectCommand,
			want: "from-os",
		},
		{
			name: "DynamicEvalUsesDefaultExpansion",
			raw:  "$TOKEN",
			mode: value.ModeDynamicEval,
			want: "secret",
		},
		{
			name: "UnknownModeUsesConservativeExpansion",
			raw:  "$TOKEN",
			mode: value.Mode(99),
			want: "secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := value.ExpandString(tt.raw, scope, tt.mode, "run")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
