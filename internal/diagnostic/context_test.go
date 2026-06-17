// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package diagnostic_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/diagnostic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportUsesContextSink(t *testing.T) {
	t.Parallel()

	var collector diagnostic.Collector
	ctx := diagnostic.WithSink(context.Background(), &collector)
	diagnostic.Report(ctx, diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "message",
	})

	diagnostics := collector.Diagnostics()
	require.Len(t, diagnostics, 1)
	assert.Equal(t, diagnostic.Code("value_reference_unresolved"), diagnostics[0].Code)
}

func TestReportWithoutContextSinkIsNoop(t *testing.T) {
	t.Parallel()

	diagnostic.Report(context.Background(), diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "message",
	})
}
