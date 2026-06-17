// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolverSemanticFieldsHaveUniquePolicyKinds(t *testing.T) {
	t.Parallel()

	fields := value.SemanticFieldsForTest("field")
	require.Len(t, fields, value.FieldKindCountForTest())

	seen := make(map[int]string, len(fields))
	for _, field := range fields {
		kind := value.FieldKindForTest(field.Field)
		require.NotContains(t, seen, kind, "duplicate field kind for %s and %s", seen[kind], field.Name)
		seen[kind] = field.Name
	}
}

func TestResolverFieldPolicyMatrix(t *testing.T) {
	ctx := context.Background()
	t.Setenv("VALUE_RESOLUTION_MATRIX_OS", "from-os")

	scope := value.NewEnvScope(nil, false).
		WithEntry("MATRIX_SCOPE", "scoped", value.EnvSourceDAGEnv)
	resolver := value.NewResolver(
		value.StaticScope{Consts: value.Values{"service": "api"}},
		value.RuntimeScope{Consts: value.Values{"service": "api"}, Env: scope},
	)

	raw := "${consts.service}:$MATRIX_SCOPE:$VALUE_RESOLUTION_MATRIX_OS"
	strictNoOS := "api:scoped:$VALUE_RESOLUTION_MATRIX_OS"
	strictOS := "api:scoped:from-os"
	nonStrictNoOS := "${consts.service}:scoped:$VALUE_RESOLUTION_MATRIX_OS"
	nonStrictOS := "${consts.service}:scoped:from-os"

	expected := map[string]string{
		"Workflow":                   strictNoOS,
		"ConstLoad":                  strictNoOS,
		"StaticValidation":           strictNoOS,
		"WorkflowObject":             strictNoOS,
		"HostConfigObject":           nonStrictNoOS,
		"DAGEnv":                     strictOS,
		"RuntimeDAGEnv":              strictNoOS,
		"DynamicParamEval":           strictOS,
		"DotenvPath":                 strictOS,
		"StepDir":                    strictNoOS,
		"DAGWorkingDir":              strictNoOS,
		"AgentWorkingDir":            strictNoOS,
		"ServerBasePath":             nonStrictOS,
		"LogPath":                    nonStrictOS,
		"CoordinatorArtifactBaseDir": nonStrictOS,
		"StepArtifactOutput":         strictNoOS,
		"StructuredOutputPath":       strictNoOS,
		"StructuredOutputLiteral":    strictNoOS,
		"DAGShell":                   strictNoOS,
		"StepShell":                  strictNoOS,
		"ConditionValue":             strictNoOS,
		"ConditionCommand":           strictOS,
		"DirectCommand":              strictOS,
		"ShellCommand":               strictNoOS,
		"CommandScript":              strictNoOS,
		"Container":                  strictNoOS,
		"StepEnv":                    strictNoOS,
		"ContainerEnv":               strictNoOS,
		"ExecutorConfig":             strictNoOS,
		"TemplateScript":             raw,
		"TemplateConfig":             strictNoOS,
		"SubDAGName":                 strictNoOS,
		"SubDAGParams":               strictNoOS,
		"ParallelItem":               strictNoOS,
		"ParallelItemParam":          strictNoOS,
		"ParallelSubDAG":             strictNoOS,
		"RetryInteger":               strictOS,
		"RepeatInteger":              strictOS,
	}
	require.Len(t, expected, value.FieldKindCountForTest())

	for _, field := range value.SemanticFieldsForTest("field") {
		want, ok := expected[field.Name]
		require.True(t, ok, "missing expectation for %s", field.Name)
		got, err := resolver.String(ctx, raw, field.Field)
		require.NoError(t, err, field.Name)
		assert.Equal(t, want, got, field.Name)
	}
}

func TestResolverFieldPolicyBacktickMatrix(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	for _, tt := range []struct {
		name  string
		field value.Field
		want  string
	}{
		{name: "workflow", field: value.WorkflowField("field"), want: "`printf matrix`"},
		{name: "DAG env", field: value.DAGEnvField("field"), want: "matrix"},
		{name: "runtime DAG env", field: value.RuntimeDAGEnvField("field"), want: "`printf matrix`"},
		{name: "step env", field: value.StepEnvField("field"), want: "matrix"},
		{name: "container env", field: value.ContainerEnvField("field"), want: "matrix"},
		{name: "dynamic params", field: value.DynamicParamEvalField("field"), want: "matrix"},
		{name: "template script", field: value.TemplateScriptField("field"), want: "`printf matrix`"},
		{name: "direct command", field: value.DirectCommandField("field", value.CommandContext{}), want: "`printf matrix`"},
	} {
		got, err := resolver.String(ctx, "`printf matrix`", tt.field)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, got, tt.name)
	}
}
