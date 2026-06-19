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

func TestExpandStringWithConstBinding(t *testing.T) {
	t.Parallel()

	raw := "deploy ${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}"
	output := `{"image":"repo/api:v1"}`
	staticScope := value.StaticScope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": nil},
	}
	resolver := value.NewResolver(staticScope, value.RuntimeScope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": "prod"},
		Steps: map[string]value.StepInfo{
			"build": {DeclaredOutputs: &output},
		},
	})

	got, err := resolver.String(context.Background(), raw, value.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, "deploy api prod ${env.HOME} repo/api:v1", got)
}

func TestExpandStringPreservesMalformedBindingText(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{Consts: value.Values{"service": "api"}},
	)
	got, err := resolver.String(
		context.Background(),
		"echo ${consts.service",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "echo ${consts.service", got)
}

func TestExpandStringLeavesEvalReferencesForEvaluator(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})
	got, err := resolver.String(context.Background(), "eval ${DATA.image} and $DATA.tag", value.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, "eval ${DATA.image} and $DATA.tag", got)
}

func TestExpandStringResolvesConstBindingsWithScope(t *testing.T) {
	t.Parallel()

	output := `{"image":"repo/api:v1"}`
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"environment": "prod"},
			Env:    testEnvScope(map[string]string{"HOME": "/workspace"}),
			Steps: map[string]value.StepInfo{
				"build": {DeclaredOutputs: &output},
			},
		},
	)
	got, err := resolver.String(
		context.Background(),
		"${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "api prod /workspace repo/api:v1", got)
}

func TestExpandStringDoesNotRejectFutureNamespaceShorthand(t *testing.T) {
	t.Parallel()

	tests := []string{
		"$consts.service",
		"$env.FOO",
		"$params.foo",
		"$steps.build.outputs.image",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			resolver := value.NewResolver(value.StaticScope{}, value.RuntimeScope{})
			got, err := resolver.String(context.Background(), raw, value.WorkflowField("run"))
			require.NoError(t, err)
			assert.Equal(t, raw, got)
		})
	}
}
