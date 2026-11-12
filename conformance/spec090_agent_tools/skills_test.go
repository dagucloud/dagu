// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec090_agent_tools_test

import (
	"os"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

const (
	refundBody = "Refund an order in 30 days. Ask a manager above 100 euros."
	toneBody   = "Be warm and short."
)

// loadSkillEnum returns the names that the schema of load_skill lists.
func (r chatRequest) loadSkillEnum(t *testing.T) []string {
	t.Helper()
	for _, tool := range r.Tools {
		if tool.Function.Name == "load_skill" {
			return tool.Function.Parameters.Properties["name"].Enum
		}
	}
	t.Fatalf("no load_skill tool in %v", r.toolNamesInOrder())
	return nil
}

// A step with skills sends the Skills section in the system message and the
// tool load_skill. The section lists each skill with its description. It does
// not hold the body of a skill.
func TestSkillsSectionAndLoadSkillTool(t *testing.T) {
	t.Parallel()

	l := newLLM(t, "")
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l), "start", "skills_step.yaml")
	result.ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 1)
	system := requests[0].message("system")
	require.True(t, strings.HasPrefix(system, "You help customers.\n\n"), system)
	require.Contains(t, system, "## Skills\n")
	require.Contains(t, system, "`load_skill`")
	require.Contains(t, system, "- refund-policy: Rules for a refund")
	require.Contains(t, system, "- tone-of-voice: ")
	require.NotContains(t, system, refundBody)
	require.NotContains(t, system, toneBody)

	require.Equal(t, []string{"load_skill"}, requests[0].toolNamesInOrder())
	require.Equal(t, []string{"refund-policy", "tone-of-voice"}, requests[0].loadSkillEnum(t))
}

// The model calls load_skill. The next request carries the body of the skill
// as the tool result.
func TestLoadSkillReturnsTheBody(t *testing.T) {
	t.Parallel()

	l := newLLM(t, toolCall("load_skill", `{"name":"refund-policy"}`))
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env(l), "start", "skills_step.yaml")
	result.ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 2)
	last := requests[1].Messages[len(requests[1].Messages)-1]
	require.Equal(t, "tool", last.Role)
	require.Equal(t, "call_1", last.ToolCallID)
	require.Contains(t, last.Content, "# Refund policy")
	require.Contains(t, last.Content, refundBody)
	// The next request still offers the tool and the section.
	require.Contains(t, requests[1].message("system"), "- refund-policy: Rules for a refund")
	require.Equal(t, []string{"load_skill"}, requests[1].toolNamesInOrder())
}

// dagu validate rejects a skill that does not exist, a skill whose name differs
// from its folder, a front matter key that the format does not know, and a
// skill item that is not a name. The error names the skill, the file and the
// folders searched.
func TestValidateRejectsBadSkills(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture  string
		messages []string
	}{
		{"invalid_missing_skill.yaml", []string{`skill "nowhere"`, "no folder", "skills"}},
		{"invalid_skill_name_mismatch.yaml", []string{`skill "name-mismatch"`, `front matter name is "other-name"`, "SKILL.md", "name-folder"}},
		{"invalid_skill_front_matter_key.yaml", []string{`skill "bad-key"`, `"model"`, "SKILL.md", "front-matter-key"}},
		{"invalid_skill_object_item.yaml", []string{`llm.skills[0]`, "a skill item must be a skill name"}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			require.NotEqual(t, 0, result.ExitCode())
			output := result.Stdout() + result.Stderr()
			for _, message := range tc.messages {
				require.Contains(t, output, message)
			}
		})
	}
}

// The skills directory holds skill folders, not workflows.
func TestSkillFoldersAreNotWorkflows(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "skills_step.yaml")
	result.ExpectExitCode(0)
	_, err := os.Stat(dagu.ProjectPath("skills/refund-policy/SKILL.md"))
	require.NoError(t, err)
}
