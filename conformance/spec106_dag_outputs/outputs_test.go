// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec106_dag_outputs_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestValidateDAGOutputs(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"valid_short.yaml", "valid_canonical.yaml"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
		})
	}

	for _, tc := range []struct {
		file        string
		stderrParts []string
	}{
		{"invalid_name.yaml", []string{"outputs.1summary", "must match"}},
		{"invalid_literal.yaml", []string{"outputs.summary", "exactly one"}},
		{"invalid_combined.yaml", []string{"outputs.summary", "exactly one"}},
		{"invalid_escaped.yaml", []string{"outputs.summary", "exactly one"}},
		{"invalid_nested.yaml", []string{"outputs.findings", "exactly one"}},
		{"invalid_missing_step.yaml", []string{"outputs.summary", `step "nope" not found`}},
		{"invalid_undeclared.yaml", []string{"outputs.summary", `does not declare output "nope"`}},
		{"invalid_foreach_body.yaml", []string{"outputs.x", `step "body" not found`}},
		{"invalid_handler.yaml", []string{"outputs.x", `step "onfail" not found`}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
		})
	}
}

func TestRunWithDAGOutputs(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "run.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("result.out", "done")
}
