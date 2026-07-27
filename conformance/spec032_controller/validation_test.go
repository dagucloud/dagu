// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec032_controller_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestControllerShapeValidation(t *testing.T) {
	t.Parallel()

	t.Run("valid_controller.yaml", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "valid_controller.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderr("")
	})

	invalid := []struct {
		file  string
		parts []string
	}{
		{file: "invalid_missing_llm.yaml", parts: []string{"llm", "requires an llm configuration"}},
		{file: "invalid_no_tasks.yaml", parts: []string{"tasks", "at least one task"}},
		{file: "invalid_duplicate_task.yaml", parts: []string{"duplicate task name", "done"}},
		{file: "invalid_depends.yaml", parts: []string{"depends", "not allowed in type: controller"}},
		{file: "invalid_reserved_step_name.yaml", parts: []string{"__controller__", "reserved"}},
		{file: "invalid_tasks_without_controller.yaml", parts: []string{"tasks", "require type: controller"}},
		{file: "invalid_unknown_type.yaml", parts: []string{"invalid type", "graph, chain, controller"}},
	}

	for _, tt := range invalid {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tt.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tt.parts...)
		})
	}
}
