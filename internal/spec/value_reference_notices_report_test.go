// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportValueReferenceNoticesForBuiltInRunContext(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "daily",
		Steps: []ir.Step{{
			ID:     "build-id",
			Name:   "build",
			Script: "printf '%s\\n' '${context.dag.name}' '${context.step.id}' '${context.step.name}' '${context.run.status}' '${context.paths.context}'",
		}},
	}

	var collector cmnvalue.ValueReferenceNoticeCollector
	spec.ReportValueReferenceNotices(dag, &collector)

	notices := collector.Notices()
	require.Len(t, notices, 2)
	assert.Equal(t, "steps[0].run", notices[0].FieldPath)
	assert.Equal(t, "${context.run.status}", notices[0].Token)
	assert.Equal(t, cmnvalue.ValueReferenceReasonNamespaceUnavailable, notices[0].Reason)
	assert.Equal(t, "steps[0].run", notices[1].FieldPath)
	assert.Equal(t, "${context.paths.context}", notices[1].Token)
	assert.Equal(t, cmnvalue.ValueReferenceReasonUnknownContextField, notices[1].Reason)
}

func TestReportValueReferenceNoticesDoesNotRunPreconditionEval(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX command snippets")
	}

	marker := filepath.Join(t.TempDir(), "notice-ran.txt")
	dag := &ir.DAG{
		Steps: []ir.Step{{
			Name: "check",
			Preconditions: []*ir.Condition{{
				Eval:     "$(printf bad > '" + marker + "'; printf ok)",
				Expected: "ok",
			}},
		}},
	}

	var collector cmnvalue.ValueReferenceNoticeCollector
	spec.ReportValueReferenceNotices(dag, &collector)

	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err), "expected marker to be absent")
}
