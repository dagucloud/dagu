// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
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

func TestExpandStringResolvesForeachBindingsWithScope(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Foreach: value.Values{
				"index": "2",
				"key":   "episode-3",
				"episode": map[string]any{
					"slug": "episode-3",
					"url":  "https://example.test/episode-3",
				},
			},
		},
	)

	got, err := resolver.String(
		context.Background(),
		"${foreach.index} ${foreach.key} ${foreach.episode.url} ${foreach.episode}",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, `2 episode-3 https://example.test/episode-3 {"slug":"episode-3","url":"https://example.test/episode-3"}`, got)
}

func TestExpandStringPreservesUnavailableForeachBinding(t *testing.T) {
	t.Parallel()

	var collector value.ValueReferenceNoticeCollector
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{},
		value.WithValueReferenceNotices(&collector),
	)

	got, err := resolver.String(
		context.Background(),
		"${foreach.item}",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "${foreach.item}", got)
	require.Len(t, collector.Notices(), 1)
	assert.Equal(t, value.ValueReferenceReasonNamespaceUnavailable, collector.Notices()[0].Reason)
}

func TestExpandStringDistinguishesUnknownScopedPathFromUnavailableNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		inputs      value.Values
		wantMessage string
		wantReason  value.ValueReferenceNoticeReason
	}{
		{
			name:        "unknown input in populated namespace",
			inputs:      value.Values{"source": "/tmp/source.txt"},
			wantMessage: "unknown inputs.missing binding",
		},
		{
			name:        "input namespace unavailable",
			wantMessage: "inputs.missing is unavailable in this context",
			wantReason:  value.ValueReferenceReasonNamespaceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var collector value.ValueReferenceNoticeCollector
			resolver := value.NewResolver(
				value.StaticScope{},
				value.RuntimeScope{Inputs: tt.inputs},
				value.WithValueReferenceNotices(&collector),
			)
			got, err := resolver.String(context.Background(), "${inputs.missing}", value.WorkflowField("run"))
			require.NoError(t, err)
			assert.Equal(t, "${inputs.missing}", got)
			notices := collector.Notices()
			require.Len(t, notices, 1)
			assert.Contains(t, notices[0].Message, tt.wantMessage)
			assert.Equal(t, tt.wantReason, notices[0].Reason)
		})
	}
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
