// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package diagnostic_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/diagnostic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectorReport(t *testing.T) {
	t.Parallel()

	var collector diagnostic.Collector
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "first",
		Location: diagnostic.Location{
			FieldPath: "steps[0].run",
		},
		Attributes: map[string]string{
			"token": "${params.name}",
		},
	})
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "duplicate",
		Location: diagnostic.Location{
			FieldPath: "steps[0].run",
		},
		Attributes: map[string]string{
			"token": "${params.name}",
		},
	})
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "same field with another token",
		Location: diagnostic.Location{
			FieldPath: "steps[0].run",
		},
		Attributes: map[string]string{
			"token": "${params.other}",
		},
	})
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "same condition in another field",
		Location: diagnostic.Location{
			FieldPath: "steps[1].run",
		},
		Attributes: map[string]string{
			"token": "${params.name}",
		},
	})

	diagnostics := collector.Diagnostics()
	require.Len(t, diagnostics, 3)
	assert.Equal(t, "steps[0].run", diagnostics[0].Location.FieldPath)
	assert.Equal(t, "first", diagnostics[0].Message)
	assert.Equal(t, "${params.other}", diagnostics[1].Attributes["token"])
	assert.Equal(t, "steps[1].run", diagnostics[2].Location.FieldPath)
}

func TestCollectorRejectsIncompleteDiagnostics(t *testing.T) {
	t.Parallel()

	var collector diagnostic.Collector
	collector.Report(diagnostic.Diagnostic{
		Kind:    "value_resolution",
		Code:    "value_reference_unresolved",
		Message: "missing severity",
	})
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Code:     "value_reference_unresolved",
		Message:  "missing kind",
	})
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Message:  "missing code",
	})
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
	})

	assert.Empty(t, collector.Diagnostics())
}

func TestCollectorDiagnosticsReturnsSnapshot(t *testing.T) {
	t.Parallel()

	var collector diagnostic.Collector
	collector.Report(diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "original",
		Location: diagnostic.Location{
			FieldPath: "steps[0].run",
		},
		Attributes: map[string]string{
			"token": "${params.name}",
		},
	})

	diagnostics := collector.Diagnostics()
	require.Len(t, diagnostics, 1)
	diagnostics[0].Message = "changed"
	diagnostics[0].Attributes["token"] = "${params.changed}"
	diagnostics = append(diagnostics, diagnostic.Diagnostic{
		Severity: diagnostic.SeverityNotice,
		Kind:     "value_resolution",
		Code:     "value_reference_unresolved",
		Message:  "extra",
	})

	snapshot := collector.Diagnostics()
	require.Len(t, snapshot, 1)
	assert.Equal(t, "original", snapshot[0].Message)
	assert.Equal(t, "${params.name}", snapshot[0].Attributes["token"])
}
