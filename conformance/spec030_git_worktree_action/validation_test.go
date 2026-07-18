// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec030_git_worktree_action_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestValidateGitWorktreeActions(t *testing.T) {
	t.Parallel()

	t.Run("accepted shapes do not resolve runtime resources", func(t *testing.T) {
		t.Parallel()
		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "valid_shapes.yaml")
		result.ExpectExitCode(0)
		result.ExpectStdout("")
		result.ExpectStderr("")
	})

	invalidCases := []struct {
		file        string
		stderrParts []string
	}{
		{file: "invalid_action.yaml", stderrParts: []string{"git.worktree_move"}},
		{file: "invalid_repository_missing.yaml", stderrParts: []string{"repository"}},
		{file: "invalid_repository_empty.yaml", stderrParts: []string{"repository"}},
		{file: "invalid_repository_non_string.yaml", stderrParts: []string{"repository"}},
		{file: "invalid_add_branch_missing.yaml", stderrParts: []string{"branch"}},
		{file: "invalid_add_branch_empty.yaml", stderrParts: []string{"branch"}},
		{file: "invalid_add_branch_non_string.yaml", stderrParts: []string{"branch"}},
		{file: "invalid_add_path_empty.yaml", stderrParts: []string{"path"}},
		{file: "invalid_add_path_non_string.yaml", stderrParts: []string{"path"}},
		{file: "invalid_add_from_empty.yaml", stderrParts: []string{"from"}},
		{file: "invalid_add_from_non_string.yaml", stderrParts: []string{"from"}},
		{file: "invalid_add_unknown_field.yaml", stderrParts: []string{"token"}},
		{file: "invalid_add_force.yaml", stderrParts: []string{"force"}},
		{file: "invalid_remove_repository_missing.yaml", stderrParts: []string{"repository"}},
		{file: "invalid_remove_repository_non_string.yaml", stderrParts: []string{"repository"}},
		{file: "invalid_remove_selector_missing.yaml", stderrParts: []string{"branch", "path"}},
		{file: "invalid_remove_branch_empty.yaml", stderrParts: []string{"branch"}},
		{file: "invalid_remove_branch_non_string.yaml", stderrParts: []string{"branch"}},
		{file: "invalid_remove_path_empty.yaml", stderrParts: []string{"path"}},
		{file: "invalid_remove_path_non_string.yaml", stderrParts: []string{"path"}},
		{file: "invalid_remove_force_non_boolean.yaml", stderrParts: []string{"force"}},
		{file: "invalid_remove_delete_branch_non_boolean.yaml", stderrParts: []string{"delete_branch"}},
		{file: "invalid_remove_delete_without_branch.yaml", stderrParts: []string{"delete_branch", "branch"}},
		{file: "invalid_remove_unknown_field.yaml", stderrParts: []string{"from"}},
	}
	for _, tc := range invalidCases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
		})
	}
}
