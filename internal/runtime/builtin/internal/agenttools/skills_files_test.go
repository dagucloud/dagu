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

//go:fix inline
func strPtr(s string) *string { return new(s) }

// withFiles is a skill with a reference, a script, an asset and SKILL.md.
func withFiles() ir.SkillEntry {
	e := skillEntry("guide", "Read the references.")
	e.Hash = strings.Repeat("a", 64)
	e.Files = []ir.SkillFile{
		{Path: "SKILL.md", Text: new("---\nname: guide\n---\nRead the references.\n")},
		{Path: "assets/logo.bin"},
		{Path: "references/a.md", Text: new("# A\n")},
		{Path: "scripts/run.sh", Text: new("echo hi\n")},
	}
	return e
}

func callSkillTool(t *testing.T, exec *ToolExecutor, tool, args string) ir.ToolResult {
	t.Helper()
	results := exec.ExecuteToolCalls(context.Background(), []llmpkg.ToolCall{{
		ID: "c", Function: llmpkg.ToolCallFunction{Name: tool, Arguments: args},
	}})
	require.Len(t, results, 1)
	return results[0].Result
}

func TestReadSkillFileToolExistsOnlyWhenAFileIsReadable(t *testing.T) {
	t.Parallel()

	plain, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{skillEntry("refund", "Body")}})
	require.NoError(t, err)
	assert.Equal(t, []string{"load_skill"}, toolNames(plain))

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{withFiles()}})
	require.NoError(t, err)
	assert.Equal(t, []string{"load_skill", "read_skill_file"}, toolNames(registry))
	assert.Equal(t, strings.Repeat("a", 64), registry.SkillSet().Definitions()[0].Hash)
}

func TestExecutor_LoadSkillListsTheFilesAndKeepsAHashFreeCatalog(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{withFiles()}})
	require.NoError(t, err)
	exec := NewToolExecutor(registry, "")
	got := callSkillTool(t, exec, "load_skill", `{"name":"guide"}`)
	assert.Empty(t, got.Error)
	assert.True(t, strings.HasPrefix(got.Content, "<skill_content name=\"guide\">\nRead the references."), got.Content)
	assert.Contains(t, got.Content, "<file>references/a.md</file>")
	assert.Contains(t, got.Content, "<file>scripts/run.sh</file>")
	assert.NotContains(t, got.Content, "assets/logo.bin", "the model cannot read an asset")
	assert.NotContains(t, got.Content, "SKILL.md")
	assert.True(t, strings.HasSuffix(got.Content, "</skill_content>"))

	// The same skill twice: a short note, not the body again.
	again := callSkillTool(t, exec, "load_skill", `{"name":"guide"}`)
	assert.Contains(t, again.Content, "already loaded")
	assert.NotContains(t, again.Content, "Read the references.")

	// The catalog never changes: no hash, no count, no time.
	assert.NotContains(t, registry.SkillSet().SystemText(), strings.Repeat("a", 64))
}

func TestExecutor_ReadSkillFile(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{withFiles()}})
	require.NoError(t, err)
	exec := NewToolExecutor(registry, "")

	ok := callSkillTool(t, exec, "read_skill_file", `{"name":"guide","path":"references/a.md"}`)
	assert.Empty(t, ok.Error)
	assert.Equal(t, "# A\n", ok.Content)
	ok = callSkillTool(t, exec, "read_skill_file", `{"name":"guide","path":"scripts/run.sh"}`)
	assert.Equal(t, "echo hi\n", ok.Content)

	for _, tc := range []struct{ args, want string }{
		{`{"name":"guide","path":"assets/logo.bin"}`, "cannot read it"},
		{`{"name":"guide","path":"SKILL.md"}`, "cannot read it"},
		{`{"name":"guide","path":"../../etc/passwd"}`, "has no file"},
		{`{"name":"guide","path":"/etc/passwd"}`, "has no file"},
		{`{"name":"guide","path":"references/missing.md"}`, "references/a.md"},
		{`{"name":"other","path":"references/a.md"}`, `skill "other" is not available`},
		{`{"name":"guide"}`, "has no file"},
	} {
		got := callSkillTool(t, exec, "read_skill_file", tc.args)
		assert.Empty(t, got.Content, tc.args)
		assert.Contains(t, got.Error, tc.want, tc.args)
	}
}

func TestReadSkillFileRefusesALargeFile(t *testing.T) {
	t.Parallel()

	e := withFiles()
	e.Files = append(e.Files, ir.SkillFile{Path: "references/big.md", Text: new(strings.Repeat("x", 256*1024+1))})
	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{Skills: []ir.SkillEntry{e}})
	require.NoError(t, err)
	got := callSkillTool(t, NewToolExecutor(registry, ""), "read_skill_file", `{"name":"guide","path":"references/big.md"}`)
	assert.Contains(t, got.Error, "limit of read_skill_file")
}
