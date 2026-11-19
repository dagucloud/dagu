// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillEntry(name, body string) ir.SkillEntry {
	return ir.SkillEntry{Name: name, Description: "About " + name, Body: body}
}

func TestBuildToolRegistrySkillsAddLoadSkill(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{
		skillEntry("refund", "Refund in 30 days."), skillEntry("tone", "Be kind."),
	}})
	require.NoError(t, err)
	require.NotNil(t, registry)
	assert.Equal(t, []string{"load_skill"}, toolNames(registry))

	definitions := registry.ToolDefinitions()
	require.Len(t, definitions, 1)
	assert.Equal(t, "skills", definitions[0].Source)
	properties, _ := definitions[0].Parameters["properties"].(map[string]any)
	name, _ := properties["name"].(map[string]any)
	assert.Equal(t, []any{"refund", "tone"}, name["enum"])

	records := registry.SkillSet().Definitions()
	assert.Equal(t, []ir.SkillDefinition{
		{Name: "refund", Description: "About refund"},
		{Name: "tone", Description: "About tone"},
	}, records)

	text := registry.SkillSet().SystemText()
	assert.Contains(t, text, "## Skills\n")
	assert.Contains(t, text, "`load_skill`")
	assert.Contains(t, text, "- refund: About refund")
	assert.Contains(t, text, "- tone: About tone")
	assert.NotContains(t, text, "Refund in 30 days.", "the section lists the skills and holds no body")
}

func TestBuildToolRegistryNoSkillGivesNoSection(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, map[string]*ir.DAG{"a": toolDAG("a", "A")}),
		&ir.LLMConfig{ToolEntries: []ir.ToolEntry{dagEntry("a")}})
	require.NoError(t, err)
	assert.Empty(t, registry.SkillSet().SystemText())
	assert.Nil(t, registry.SkillSet().Definitions())
	assert.False(t, registry.HasTool("load_skill"))
}

func TestBuildToolRegistryLoadSkillCountsAgainstMaxTools(t *testing.T) {
	t.Parallel()

	ctx := catalogContext(t, map[string]*ir.DAG{"lookup": toolDAG("lookup", "Look up")})
	limit := 1
	_, err := BuildToolRegistry(ctx, &ir.LLMConfig{
		ToolEntries: []ir.ToolEntry{dagEntry("lookup")},
		Skills:      []ir.SkillEntry{skillEntry("refund", "Body")},
		MaxTools:    &limit,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 tools, above max_tools 1")
}

func TestSkillSetApplyAddsToTheFirstSystemMessage(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{skillEntry("refund", "Body")}})
	require.NoError(t, err)
	set := registry.SkillSet()

	msgs := set.Apply([]llmpkg.Message{
		{Role: llmpkg.RoleSystem, Content: "You help."},
		{Role: llmpkg.RoleUser, Content: "hi"},
	})
	require.Len(t, msgs, 2)
	assert.True(t, strings.HasPrefix(msgs[0].Content, "You help.\n\n## Skills"))

	msgs = set.Apply([]llmpkg.Message{{Role: llmpkg.RoleUser, Content: "hi"}})
	require.Len(t, msgs, 2)
	assert.Equal(t, llmpkg.RoleSystem, msgs[0].Role)
	assert.True(t, strings.HasPrefix(msgs[0].Content, "## Skills"))

	var none *SkillSet
	original := []llmpkg.Message{{Role: llmpkg.RoleUser, Content: "hi"}}
	assert.Equal(t, original, none.Apply(original))
}

func TestExecutor_LoadSkillRejectsAnUnknownName(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{skillEntry("refund", "Body")}})
	require.NoError(t, err)
	exec := NewToolExecutor(registry, "")
	results := exec.ExecuteToolCalls(context.Background(), []llmpkg.ToolCall{{
		ID:       "c",
		Function: llmpkg.ToolCallFunction{Name: "load_skill", Arguments: `{"name":"nope"}`},
	}})
	require.Len(t, results, 1)
	assert.Contains(t, results[0].Result.Error, `skill "nope" is not available; available skills: refund`)
}

func TestExecutor_LoadSkillResultIsTruncated(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{skillEntry("big", strings.Repeat("x", 500))}})
	require.NoError(t, err)
	exec := NewToolExecutor(registry, "")
	exec.SetResultLimit(100)
	results := exec.ExecuteToolCalls(context.Background(), []llmpkg.ToolCall{{
		ID:       "c",
		Function: llmpkg.ToolCallFunction{Name: "load_skill", Arguments: `{"name":"big"}`},
	}})
	require.Len(t, results, 1)
	assert.LessOrEqual(t, len(results[0].Result.Content), 100)
	assert.Contains(t, results[0].Result.Content, "[truncated:")
}
