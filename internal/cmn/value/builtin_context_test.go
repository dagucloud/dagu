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

func TestBuiltinContextReferencesResolveExactFields(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			Params: value.Values{"run": "param-run"},
			BuiltinContext: value.NewBuiltinContext(map[string]string{
				"context.dag.name":                      "daily",
				"context.run.id":                        "run-1",
				"context.attempt.started_at":            "2026-03-13T10:00:01Z",
				"context.run.scheduled_at":              "2026-03-13T10:00:00Z",
				"context.attempt.id":                    "attempt-1",
				"context.step.name":                     "build",
				"context.paths.step_stdout_file":        "/tmp/stdout.log",
				"context.pushback.previous_stdout_file": "/tmp/previous.log",
			}),
		},
	)

	got, err := resolver.String(
		context.Background(),
		"${context.dag.name} ${context.run.id} ${context.attempt.started_at} ${context.run.scheduled_at} ${context.attempt.id} ${context.step.name} ${context.paths.step_stdout_file} ${context.pushback.previous_stdout_file} ${params.run}",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "daily run-1 2026-03-13T10:00:01Z 2026-03-13T10:00:00Z attempt-1 build /tmp/stdout.log /tmp/previous.log param-run", got)
}

func TestBuiltinContextLegacyAliasesResolveFrozenFields(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			BuiltinContext: value.NewBuiltinContext(map[string]string{
				"context.run.id":             "run-1",
				"context.attempt.started_at": "2026-03-13T10:00:01Z",
			}),
		},
	)

	got, err := resolver.String(context.Background(), "${run.id} ${run.started_at}", value.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, "run-1 2026-03-13T10:00:01Z", got)
}

func TestBuiltinContextCanonicalReferencesReadLegacyValues(t *testing.T) {
	t.Parallel()

	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			BuiltinContext: value.NewBuiltinContext(map[string]string{
				"run.id":         "run-1",
				"run.started_at": "2026-03-13T10:00:01Z",
			}),
		},
	)

	got, err := resolver.String(context.Background(), "${context.run.id} ${context.attempt.started_at}", value.WorkflowField("run"))
	require.NoError(t, err)
	assert.Equal(t, "run-1 2026-03-13T10:00:01Z", got)
}

func TestBuiltinContextMissingSupportedReferencePreservesAndReportsNotice(t *testing.T) {
	t.Parallel()

	var collector value.ValueReferenceNoticeCollector
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			BuiltinContext: value.NewBuiltinContext(map[string]string{"run.id": "run-1"}),
		},
		value.WithValueReferenceNotices(&collector),
	)

	got, err := resolver.String(context.Background(), "status=${context.run.status}", value.WorkflowField("steps[0].run"))
	require.NoError(t, err)
	assert.Equal(t, "status=${context.run.status}", got)

	notices := collector.Notices()
	require.Len(t, notices, 1)
	assert.Equal(t, "steps[0].run", notices[0].FieldPath)
	assert.Equal(t, "${context.run.status}", notices[0].Token)
	assert.Equal(t, value.ValueReferenceReasonNamespaceUnavailable, notices[0].Reason)
}

func TestBuiltinContextUnknownReservedReferencesPreserveAndReportNotice(t *testing.T) {
	t.Parallel()

	var collector value.ValueReferenceNoticeCollector
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			BuiltinContext: value.NewBuiltinContext(map[string]string{"run.id": "run-1"}),
		},
		value.WithValueReferenceNotices(&collector),
	)

	input := "${context.run.foo} ${context.run.id.extra} ${context.paths.context} ${context.trigger.payload}"
	got, err := resolver.String(context.Background(), input, value.WorkflowField("steps[0].run"))
	require.NoError(t, err)
	assert.Equal(t, input, got)

	notices := collector.Notices()
	require.Len(t, notices, 4)
	for _, notice := range notices {
		assert.Equal(t, value.ValueReferenceReasonUnknownContextField, notice.Reason)
	}
}

func TestBuiltinContextUnrelatedUnsupportedLookingReferencesStaySilent(t *testing.T) {
	t.Parallel()

	var collector value.ValueReferenceNoticeCollector
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			BuiltinContext: value.NewBuiltinContext(map[string]string{"context.run.id": "run-1"}),
		},
		value.WithValueReferenceNotices(&collector),
	)

	input := "${data.run.foo} ${ctx.run.id} ${run.bad-name} ${run.foo} ${step.xxx.foo}"
	got, err := resolver.String(context.Background(), input, value.WorkflowField("steps[0].run"))
	require.NoError(t, err)
	assert.Equal(t, input, got)
	assert.Empty(t, collector.Notices())
}
