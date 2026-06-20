// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportValueReferenceNoticesForBuiltInRunContext(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Name: "daily",
		Steps: []core.Step{{
			ID:     "build-id",
			Name:   "build",
			Script: "printf '%s\\n' '${context.dag.name}' '${context.step.id}' '${context.step.name}' '${context.run.status}' '${context.paths.context}'",
		}},
	}

	var collector cmnvalue.ValueReferenceNoticeCollector
	core.ReportValueReferenceNotices(dag, &collector)

	notices := collector.Notices()
	require.Len(t, notices, 2)
	assert.Equal(t, "steps[0].run", notices[0].FieldPath)
	assert.Equal(t, "${context.run.status}", notices[0].Token)
	assert.Equal(t, cmnvalue.ValueReferenceReasonNamespaceUnavailable, notices[0].Reason)
	assert.Equal(t, "steps[0].run", notices[1].FieldPath)
	assert.Equal(t, "${context.paths.context}", notices[1].Token)
	assert.Equal(t, cmnvalue.ValueReferenceReasonUnknownContextField, notices[1].Reason)
}
