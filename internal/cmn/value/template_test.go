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

func TestValidateAndExpandStringWithConstBinding(t *testing.T) {
	t.Parallel()

	raw := "deploy ${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}"
	staticScope := value.StaticScope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": nil},
	}
	resolver := value.NewResolver(staticScope, value.RuntimeScope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": "prod"},
	})

	require.NoError(t, resolver.Validate(raw, value.WorkflowField("run")))

	got, err := resolver.String(context.Background(), raw, value.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, "deploy api prod ${env.HOME} ${steps.build.outputs.image}", got)
}

func TestValidateReferencesRejectsConstBindingErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		scope value.StaticScope
		want  string
	}{
		{
			name:  "MissingConst",
			input: "echo ${consts.missing}",
			scope: value.StaticScope{Consts: value.Values{"service": "api"}},
			want:  "unknown consts binding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolver := value.NewResolver(tt.scope, value.RuntimeScope{})
			err := resolver.Validate(tt.input, value.WorkflowField("run"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
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

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"environment": "prod"},
			Env:    testEnvScope(map[string]string{"HOME": "/workspace"}),
		},
	)
	got, err := resolver.String(
		context.Background(),
		"${consts.service} ${params.environment} ${env.HOME} ${steps.build.outputs.image}",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "api prod ${env.HOME} ${steps.build.outputs.image}", got)
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
