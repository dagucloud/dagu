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
				"dag.name":                      "daily",
				"run.id":                        "run-1",
				"run.started_at":                "2026-03-13T10:00:01Z",
				"run.scheduled_at":              "2026-03-13T10:00:00Z",
				"attempt.id":                    "attempt-1",
				"step.name":                     "build",
				"paths.step_stdout_file":        "/tmp/stdout.log",
				"pushback.previous_stdout_file": "/tmp/previous.log",
			}),
		},
	)

	got, err := resolver.String(
		context.Background(),
		"${dag.name} ${run.id} ${run.started_at} ${run.scheduled_at} ${attempt.id} ${step.name} ${paths.step_stdout_file} ${pushback.previous_stdout_file} ${params.run}",
		value.WorkflowField("run"),
	)
	require.NoError(t, err)
	assert.Equal(t, "daily run-1 2026-03-13T10:00:01Z 2026-03-13T10:00:00Z attempt-1 build /tmp/stdout.log /tmp/previous.log param-run", got)
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

	got, err := resolver.String(context.Background(), "status=${run.status}", value.WorkflowField("steps[0].run"))
	require.NoError(t, err)
	assert.Equal(t, "status=${run.status}", got)

	notices := collector.Notices()
	require.Len(t, notices, 1)
	assert.Equal(t, "steps[0].run", notices[0].FieldPath)
	assert.Equal(t, "${run.status}", notices[0].Token)
}

func TestBuiltinContextUnsupportedLookingReferencesStaySilent(t *testing.T) {
	t.Parallel()

	var collector value.ValueReferenceNoticeCollector
	resolver := value.NewResolver(
		value.StaticScope{},
		value.RuntimeScope{
			BuiltinContext: value.NewBuiltinContext(map[string]string{"run.id": "run-1"}),
		},
		value.WithValueReferenceNotices(&collector),
	)

	input := "${run.foo} ${run.id.extra} ${paths.context} ${trigger.payload}"
	got, err := resolver.String(context.Background(), input, value.WorkflowField("steps[0].run"))
	require.NoError(t, err)
	assert.Equal(t, input, got)
	assert.Empty(t, collector.Notices())
}
