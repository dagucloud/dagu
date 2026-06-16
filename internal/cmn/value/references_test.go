// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"context"
	"runtime"
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

func TestScanReferencesMarksEvalStepOutputRefs(t *testing.T) {
	t.Parallel()

	refs := value.ScanReferencesForTest("${extract.output.user.id} ${steps.extract.outputs.user.id} $extract.output.user.id ${extract.output.bad-name}")

	require.Len(t, refs, 4)
	require.NotNil(t, refs[0].StepOutput)
	assert.Equal(t, "extract", refs[0].StepOutput.StepName)
	assert.Equal(t, []string{"user", "id"}, refs[0].StepOutput.Path)
	require.NotNil(t, refs[1].StepOutput)
	assert.Equal(t, "extract", refs[1].StepOutput.StepName)
	assert.Equal(t, []string{"user", "id"}, refs[1].StepOutput.Path)
	assert.Nil(t, refs[2].StepOutput)
	assert.Nil(t, refs[3].StepOutput)

	var outputRefs []value.StepOutputReference
	for _, ref := range refs {
		if ref.StepOutput != nil {
			outputRefs = append(outputRefs, *ref.StepOutput)
		}
	}
	require.Len(t, outputRefs, 2)
	assert.Equal(t, "${extract.output.user.id}", outputRefs[0].Expression)
	assert.Equal(t, "${steps.extract.outputs.user.id}", outputRefs[1].Expression)
}

func TestResolverValidateFieldMatrix(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(value.StaticScope{
		Consts: value.Values{"service": "api"},
	}, value.RuntimeScope{})

	tests := []struct {
		name    string
		raw     string
		field   value.Field
		wantErr string
	}{
		{
			name:  "ConstLoadAllowsConsts",
			raw:   "${consts.service}",
			field: value.ConstLoadField("run"),
		},
		{
			name:    "ConstLoadRejectsRuntimeNamespace",
			raw:     "${params.environment}",
			field:   value.ConstLoadField("run"),
			wantErr: "not available while loading consts",
		},
		{
			name:    "ReservedShorthandRejected",
			raw:     "$consts.service",
			field:   value.WorkflowField("run"),
			wantErr: "invalid binding shorthand",
		},
		{
			name:    "UnknownConstRejected",
			raw:     "${consts.missing}",
			field:   value.StaticValidationField("run"),
			wantErr: "unknown consts binding",
		},
		{
			name:  "NonConstNamespacesAllowed",
			raw:   "${steps.build.outputs.digest}",
			field: value.StaticValidationField("run"),
		},
		{
			name:  "EvalRefsAllowed",
			raw:   "${DATA.image} $DATA.tag",
			field: value.StaticValidationField("run"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := resolver.Validate(tt.raw, tt.field)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestResolverValidateIgnoresNonConstNamespaces(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})
	err := resolver.Validate(
		"${params.environment} ${env.RUNTIME_ONLY} ${steps.build.outputs.image}",
		value.StaticValidationField("run"),
	)
	require.NoError(t, err)
}

func TestResolverStringPreservesNonConstNamespaces(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Env:    testEnvScope(map[string]string{"HOME": "/workspace"}),
		},
	)
	got, err := resolver.String(
		context.Background(),
		"${consts.service}:${params.environment}:${env.HOME}:${steps.build.outputs.image}",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "api:${params.environment}:${env.HOME}:${steps.build.outputs.image}", got)
}

func TestResolverWorkflowFieldPreservesCommandSubstitution(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})
	got, err := resolver.String(context.Background(), "`echo resolved`", value.WorkflowField("env.VALUE"))
	require.NoError(t, err)
	assert.Equal(t, "`echo resolved`", got)
}

func TestResolverDynamicParamEvalRunsCommandSubstitution(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping backtick command substitution test on Windows")
	}

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})
	got, err := resolver.String(context.Background(), "`echo resolved`", value.DynamicParamEvalField("params"))
	require.NoError(t, err)
	assert.Equal(t, "resolved", got)
}

func TestResolverStringResolvesConstRefsAndKeepsEvalRefs(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Env:    testEnvScope(map[string]string{"HOME": "/workspace"}),
		},
	)
	got, err := resolver.String(
		context.Background(),
		"${consts.service}:${params.environment}:${env.HOME}:${steps.build.outputs.image}:${DATA.image}:$DATA.tag",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "api:${params.environment}:${env.HOME}:${steps.build.outputs.image}:${DATA.image}:$DATA.tag", got)
}

func TestResolverObjectResolvesConstRefsAcrossNestedValues(t *testing.T) {
	t.Parallel()

	obj := map[string]any{
		"image": "${consts.repo}:${params.tag}",
		"env": []any{
			"${env.TOKEN}",
			"${steps.build.outputs.digest}",
		},
		"evalRef": "${DATA.image}",
	}

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Consts: value.Values{"repo": "repo/api"},
			Env:    testEnvScope(map[string]string{"TOKEN": "secret"}),
		},
	)
	gotAny, err := resolver.Object(context.Background(), obj, value.WorkflowObjectField("with"))
	require.NoError(t, err)
	got, ok := gotAny.(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "repo/api:${params.tag}", got["image"])
	assert.Equal(t, []any{"${env.TOKEN}", "${steps.build.outputs.digest}"}, got["env"])
	assert.Equal(t, "${DATA.image}", got["evalRef"])
}

func TestResolverObjectRejectsInvalidConstShorthand(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
	)
	_, err := resolver.Object(
		context.Background(),
		map[string]any{"service": "$consts.service"},
		value.WorkflowObjectField("with.service"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with.service")
	assert.Contains(t, err.Error(), "invalid binding shorthand")
}

func TestResolverSemanticFieldsApplyOwnerSemantics(t *testing.T) {
	t.Setenv("DAGU_VALUE_MODE_DIRECT", "from-os")
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Env:    testEnvScope(map[string]string{"TOKEN": "secret"}),
		},
	)
	ctx := context.Background()

	tests := []struct {
		name  string
		raw   string
		field value.Field
		want  string
	}{
		{
			name:  "ConstLoad",
			raw:   "${consts.service}",
			field: value.ConstLoadField("run"),
			want:  "api",
		},
		{
			name:  "StaticValidation",
			raw:   "${consts.service}",
			field: value.StaticValidationField("run"),
			want:  "api",
		},
		{
			name:  "WorkflowValueExpandsScopedEnv",
			raw:   "$TOKEN",
			field: value.WorkflowField("run"),
			want:  "secret",
		},
		{
			name:  "ShellCommandExpandsScopedEnv",
			raw:   "$TOKEN",
			field: value.ShellCommandField("run", value.CommandContext{}),
			want:  "secret",
		},
		{
			name:  "DirectCommandExpandsScopedEnv",
			raw:   "$TOKEN",
			field: value.DirectCommandField("run", value.CommandContext{}),
			want:  "secret",
		},
		{
			name:  "DirectCommandUsesHostOnlyEnvFallback",
			raw:   "$DAGU_VALUE_MODE_DIRECT",
			field: value.DirectCommandField("run", value.CommandContext{}),
			want:  "from-os",
		},
		{
			name:  "DynamicEvalUsesDefaultExpansion",
			raw:   "$TOKEN",
			field: value.DynamicParamEvalField("params"),
			want:  "secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.String(ctx, tt.raw, tt.field)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
