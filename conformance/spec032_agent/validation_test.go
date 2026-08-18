// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec032_agent_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestAgentShapeValidation(t *testing.T) {
	t.Parallel()

	t.Run("valid_agent.yaml", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "valid_agent.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderr("")
	})

	// type: controller is the pre-rename spelling of type: agent.
	t.Run("legacy_controller_type.yaml", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "legacy_controller_type.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrContains("type: controller is deprecated", "use type: agent")
	})

	invalid := []struct {
		file  string
		parts []string
	}{
		{file: "invalid_missing_llm.yaml", parts: []string{"llm", "requires an llm configuration"}},
		{file: "invalid_no_tasks.yaml", parts: []string{"tasks", "at least one task"}},
		{file: "invalid_duplicate_task.yaml", parts: []string{"duplicate task name", "done"}},
		{file: "invalid_depends.yaml", parts: []string{"depends", "not allowed in type: agent"}},
		{file: "invalid_reserved_step_name.yaml", parts: []string{"__agent__", "reserved"}},
		{file: "invalid_tasks_without_agent.yaml", parts: []string{"tasks", "require type: agent"}},
		{file: "invalid_unknown_type.yaml", parts: []string{"invalid type", "graph, chain, agent"}},
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
