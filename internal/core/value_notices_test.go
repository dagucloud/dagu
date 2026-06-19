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
			Script: "printf '%s\\n' '${dag.name}' '${step.id}' '${step.name}' '${run.status}' '${paths.context}'",
		}},
	}

	var collector cmnvalue.ValueReferenceNoticeCollector
	core.ReportValueReferenceNotices(dag, &collector)

	notices := collector.Notices()
	require.Len(t, notices, 1)
	assert.Equal(t, "steps[0].run", notices[0].FieldPath)
	assert.Equal(t, "${run.status}", notices[0].Token)
}
