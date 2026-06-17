// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectorAddDiagnostic(t *testing.T) {
	t.Parallel()

	var collector value.Collector
	collector.AddDiagnostic(value.Diagnostic{
		Level:   value.LevelNotice,
		Code:    value.CodeValueReferenceUnresolved,
		Field:   "steps[0].run",
		Token:   "${params.name}",
		Message: "first",
	})
	collector.AddDiagnostic(value.Diagnostic{
		Level:   value.LevelNotice,
		Code:    value.CodeValueReferenceUnresolved,
		Field:   "steps[0].run",
		Token:   "${params.name}",
		Message: "duplicate",
	})
	collector.AddDiagnostic(value.Diagnostic{
		Level:   value.LevelNotice,
		Code:    value.CodeValueReferenceUnresolved,
		Field:   "steps[1].run",
		Token:   "${params.name}",
		Message: "same token in another field",
	})

	diagnostics := collector.Diagnostics()
	require.Len(t, diagnostics, 2)
	assert.Equal(t, "steps[0].run", diagnostics[0].Field)
	assert.Equal(t, "first", diagnostics[0].Message)
	assert.Equal(t, "steps[1].run", diagnostics[1].Field)
}

func TestCollectorRejectsIncompleteDiagnostics(t *testing.T) {
	t.Parallel()

	var collector value.Collector
	collector.AddDiagnostic(value.Diagnostic{
		Code:    value.CodeValueReferenceUnresolved,
		Message: "missing level",
	})
	collector.AddDiagnostic(value.Diagnostic{
		Level:   value.LevelNotice,
		Message: "missing code",
	})
	collector.AddDiagnostic(value.Diagnostic{
		Level: value.LevelNotice,
		Code:  value.CodeValueReferenceUnresolved,
	})

	assert.Empty(t, collector.Diagnostics())
}

func TestCollectorDiagnosticsReturnsSnapshot(t *testing.T) {
	t.Parallel()

	var collector value.Collector
	collector.AddDiagnostic(value.Diagnostic{
		Level:   value.LevelNotice,
		Code:    value.CodeValueReferenceUnresolved,
		Field:   "steps[0].run",
		Token:   "${params.name}",
		Message: "original",
	})

	diagnostics := collector.Diagnostics()
	require.Len(t, diagnostics, 1)
	diagnostics[0].Message = "changed"
	diagnostics = append(diagnostics, value.Diagnostic{
		Level:   value.LevelNotice,
		Code:    value.CodeValueReferenceUnresolved,
		Field:   "steps[1].run",
		Token:   "${params.other}",
		Message: "extra",
	})

	snapshot := collector.Diagnostics()
	require.Len(t, snapshot, 1)
	assert.Equal(t, "original", snapshot[0].Message)
}
