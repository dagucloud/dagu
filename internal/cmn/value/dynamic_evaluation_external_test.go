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

func TestDynamicParamEvalSyntax(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX command snippets")
	}

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.String(context.Background(), "prefix `printf one` suffix", value.DynamicParamEvalField("params[0].eval"))
	require.NoError(t, err)
	assert.Equal(t, "prefix one suffix", got)

	got, err = resolver.String(context.Background(), "prefix $(printf two) suffix", value.DynamicParamEvalField("params[0].eval"))
	require.NoError(t, err)
	assert.Equal(t, "prefix two suffix", got)
}

func TestDynamicParamEvalPreservesUnclosedCommandText(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.String(context.Background(), "`printf one", value.DynamicParamEvalField("params[0].eval"))
	require.NoError(t, err)
	assert.Equal(t, "`printf one", got)

	got, err = resolver.String(context.Background(), "$(printf two", value.DynamicParamEvalField("params[0].eval"))
	require.NoError(t, err)
	assert.Equal(t, "$(printf two", got)
}

func TestDynamicParamEvalShellSyntaxUsesNextUnescapedClosingParen(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX command snippets")
	}

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.String(context.Background(), "$(printf a)b)", value.DynamicParamEvalField("params[0].eval"))
	require.NoError(t, err)
	assert.Equal(t, "ab)", got)

	got, err = resolver.String(context.Background(), "$(printf a\\)b)", value.DynamicParamEvalField("params[0].eval"))
	require.NoError(t, err)
	assert.Equal(t, "a)b", got)
}

func TestDynamicParamEvalRejectsNestedShellSubstitution(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX command snippets")
	}

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	_, err := resolver.String(context.Background(), "$(printf $(printf nested))", value.DynamicParamEvalField("params[0].eval"))
	require.Error(t, err)
}

func TestWorkflowFieldsPreserveCommandSubstitutionText(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})

	got, err := resolver.String(context.Background(), "`printf one` and $(printf two)", value.WorkflowField("steps[0].run"))
	require.NoError(t, err)
	assert.Equal(t, "`printf one` and $(printf two)", got)
}

func TestNonDynamicFieldsPreserveCommandSubstitutionText(t *testing.T) {
	t.Parallel()

	fields := []struct {
		name  string
		field value.Field
	}{
		{name: "workflow", field: value.WorkflowField("steps[0].run")},
		{name: "dag env", field: value.DAGEnvField("env.OUTSIDE")},
		{name: "runtime dag env", field: value.RuntimeDAGEnvField("env.OUTSIDE")},
		{name: "step env", field: value.StepEnvField("steps[0].env.OUTSIDE")},
		{name: "container env", field: value.ContainerEnvField("steps[0].container.env.OUTSIDE")},
		{name: "executor config", field: value.ExecutorConfigField("steps[0].with.value")},
	}

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})
	for _, tc := range fields {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolver.String(context.Background(), "`printf one` and $(printf two)", tc.field)
			require.NoError(t, err)
			assert.Equal(t, "`printf one` and $(printf two)", got)
		})
	}
}
