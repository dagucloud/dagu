// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tests_test

import (
	"testing"

	dagutest "github.com/dagucloud/dagu/tests/internal"
)

func Test002Schema(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"minimal_valid.yaml",
		"inline_subdag_valid.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := dagutest.New(t, "002_schema")

			result := dagu.Run("workflow", "validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("executed.txt")
		})
	}

	invalidCases := []struct {
		name         string
		file         string
		stderrParts  []string
		assertNoExec bool
	}{
		{
			name:         "entrypoint name is forbidden",
			file:         "entrypoint_name_forbidden.yaml",
			stderrParts:  []string{"entrypoint", "name"},
			assertNoExec: true,
		},
		{
			name:         "steps mapping is forbidden",
			file:         "steps_mapping_forbidden.yaml",
			stderrParts:  []string{"steps", "sequence"},
			assertNoExec: true,
		},
		{
			name:        "missing steps is forbidden",
			file:        "missing_steps.yaml",
			stderrParts: []string{"steps"},
		},
		{
			name:        "empty steps is forbidden",
			file:        "empty_steps.yaml",
			stderrParts: []string{"steps", "non-empty"},
		},
		{
			name:        "unknown root field is forbidden",
			file:        "unknown_root_field.yaml",
			stderrParts: []string{"unknown root field"},
		},
		{
			name:        "duplicate root key is forbidden",
			file:        "duplicate_root_keys.yaml",
			stderrParts: []string{"already defined"},
		},
		{
			name:        "empty document is forbidden",
			file:        "empty_document.yaml",
			stderrParts: []string{"document 2", "empty"},
		},
		{
			name:        "later document name is required",
			file:        "later_document_without_name.yaml",
			stderrParts: []string{"document 2", "name"},
		},
		{
			name:        "duplicate document names are forbidden",
			file:        "duplicate_document_names.yaml",
			stderrParts: []string{"child", "unique"},
		},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := dagutest.New(t, "002_schema")

			result := dagu.Run("workflow", "validate", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
			if tc.assertNoExec {
				dagu.ExpectNoFile("executed.txt")
			}
		})
	}
}
