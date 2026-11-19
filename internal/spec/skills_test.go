// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillFile(name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body
}

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	folder := filepath.Join(dir, spec.SkillsDirName, name)
	require.NoError(t, os.MkdirAll(folder, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(folder, spec.SkillFileName), []byte(content), 0o600))
}

const skillChatHead = `
steps:
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: m
      base_url: http://localhost:1
      prompt: hi
`

func loadSkillDAG(t *testing.T, with string, opts ...spec.LoadOption) (*ir.LLMConfig, error) {
	t.Helper()
	dag, err := spec.LoadYAML(t.Context(), []byte(skillChatHead+with), opts...)
	if err != nil {
		return nil, err
	}
	require.Len(t, dag.Steps, 1)
	require.NotNil(t, dag.Steps[0].LLM)
	return dag.Steps[0].LLM, nil
}

func TestSkillEntriesLoadTheFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSkill(t, dir, "refund-policy", skillFile("refund-policy", "Refund rules", "Refund in 30 days.\n"))
	writeSkill(t, dir, "tone", skillFile("tone", "Voice", "Be kind.\n"))

	cfg, err := loadSkillDAG(t, `
      skills:
        - refund-policy
        - tone
        - refund-policy
`, spec.WithDAGsDir(dir))
	require.NoError(t, err)
	assert.True(t, cfg.HasSkills())
	require.Len(t, cfg.Skills, 2)
	assert.Equal(t, "refund-policy", cfg.Skills[0].Name)
	assert.Equal(t, "Refund rules", cfg.Skills[0].Description)
	assert.Equal(t, "Refund in 30 days.\n", cfg.Skills[0].Body)
	assert.Equal(t, "tone", cfg.Skills[1].Name)
}

func TestSkillEntriesFindTheDirectoryNextToTheDAG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSkill(t, dir, "tone", skillFile("tone", "Voice", "Be kind.\n"))
	file := filepath.Join(dir, "team", "flow.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))

	cfg, err := loadSkillDAG(t, "      skills: [tone]\n", spec.WithDAGsDir(dir), spec.WithSourceFile(file))
	require.NoError(t, err)
	require.Len(t, cfg.Skills, 1)
}

func TestSkillEntryErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSkill(t, dir, "tone", skillFile("tone", "Voice", "Be kind.\n"))
	writeSkill(t, dir, "wrong", skillFile("other", "Voice", "Be kind.\n"))
	writeSkill(t, dir, "extra", "---\nname: extra\ndescription: d\nmodel: sonnet\n---\nbody")

	tests := []struct {
		name string
		with string
		want []string
	}{
		{"missing skill", "skills: [nothing]", []string{`skill "nothing"`, "no folder", filepath.Join(dir, "skills")}},
		{"name differs from folder", "skills: [wrong]", []string{`skill "wrong"`, `front matter name is "other"`, "SKILL.md", "name-folder", filepath.Join(dir, "skills", "wrong")}},
		{"unknown front matter key", "skills: [extra]", []string{`skill "extra"`, `"model"`, "front-matter-key", "SKILL.md"}},
		{"object item", "skills:\n        - skill: tone", []string{"a skill item must be a skill name (a string), got a map"}},
		{"object item with inline", "skills:\n        - skill: tone\n          inline: true", []string{"a skill item must be a skill name"}},
		{"number item", "skills: [3]", []string{"a skill item must be a skill name"}},
		{"empty name", "skills: [\"\"]", []string{"a skill name must not be empty"}},
		{"bad name", "skills: [../x]", []string{"not a valid skill name"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadSkillDAG(t, "      "+tt.with+"\n", spec.WithDAGsDir(dir))
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// The key skills is a list of skill names on chat.completion and on agent.run,
// with one error for another value. An empty list offers no skill.
func TestSkillsListOnBothActions(t *testing.T) {
	t.Parallel()

	agentHead := "\nsteps:\n  - id: ask\n    action: agent.run\n    with:\n      provider: claude\n      endpoint: { name: test-endpoint, model: claude-test }\n      prompt: hi\n"
	for name, head := range map[string]string{"chat.completion": skillChatHead, "agent.run": agentHead} {
		_, err := spec.LoadYAML(t.Context(), []byte(head+"      skills: code-style\n"), spec.WithDAGsDir(t.TempDir()))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "skills must be a list of skill names, got a string", name)

		_, err = spec.LoadYAML(t.Context(), []byte(head+"      skills: []\n"), spec.WithDAGsDir(t.TempDir()))
		require.NoError(t, err, name)
	}
}

func TestSkillsNeedASkillsDirectory(t *testing.T) {
	t.Parallel()

	_, err := loadSkillDAG(t, "      skills: [tone]\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Dagu searched")
}

func TestSkillEntryCarriesTheVersionAndTheFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSkill(t, dir, "tone", skillFile("tone", "Rules of the voice of the agent", "Be kind.\n"))
	folder := filepath.Join(dir, spec.SkillsDirName, "tone")
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "references"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "references", "words.md"), []byte("# Words\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "assets", "logo.bin"), []byte{0, 1, 2}, 0o600))

	file := filepath.Join(dir, "flow.yaml")
	dag, err := spec.LoadYAMLAt(t.Context(), []byte(skillChatHead+"      skills: [tone]\n"), file, spec.WithDAGsDir(dir))
	require.NoError(t, err)
	entry := dag.Steps[0].LLM.Skills[0]
	assert.Len(t, entry.Hash, 64)
	assert.Equal(t, folder, entry.Dir)
	require.Len(t, entry.Files, 3)
	assert.Equal(t, "SKILL.md", entry.Files[0].Path)
	var paths []string
	for _, f := range entry.Files {
		paths = append(paths, f.Path)
		if f.Path == "assets/logo.bin" {
			assert.Nil(t, f.Text, "a binary file has no inline content")
		} else {
			require.NotNil(t, f.Text, f.Path)
		}
	}
	assert.Equal(t, []string{"SKILL.md", "assets/logo.bin", "references/words.md"}, paths)

	// The loader records every file for the bundle and the version for the check.
	assert.Equal(t, []string{
		filepath.Join(folder, "SKILL.md"),
		filepath.Join(folder, "assets", "logo.bin"),
		filepath.Join(folder, "references", "words.md"),
	}, dag.SideFiles)
	require.Len(t, dag.SkillRefs, 1)
	assert.Equal(t, entry.Hash, dag.SkillRefs[0].Hash)
}

func TestSkillWarningsReachTheDAG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSkill(t, dir, "tone", skillFile("tone", "Voice", "Be kind.\n"))
	dag, err := spec.LoadYAML(t.Context(), []byte(skillChatHead+"      skills: [tone]\n"), spec.WithDAGsDir(dir))
	require.NoError(t, err)
	require.NotEmpty(t, dag.BuildWarnings)
	assert.Contains(t, strings.Join(dag.BuildWarnings, "\n"), "description-short")
}

func TestSkillCapsOfAStep(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var names []string
	for i := range 21 {
		name := fmt.Sprintf("s%02d", i)
		writeSkill(t, dir, name, skillFile(name, "Rules for the topic "+name, "Body.\n"))
		names = append(names, name)
	}
	_, err := loadSkillDAG(t, "      skills: ["+strings.Join(names[:20], ", ")+"]\n", spec.WithDAGsDir(dir))
	require.NoError(t, err, "20 skills are allowed")
	_, err = loadSkillDAG(t, "      skills: ["+strings.Join(names, ", ")+"]\n", spec.WithDAGsDir(dir))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20 skills at most")

	// The loader counts the names before it reads a folder, so a long list does
	// not make it read each skill.
	var missing []string
	for i := range 1000 {
		missing = append(missing, fmt.Sprintf("missing-%d", i))
	}
	_, err = loadSkillDAG(t, "      skills: ["+strings.Join(missing, ", ")+"]\n", spec.WithDAGsDir(dir))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "20 skills at most, but the list has 1000")

	// The catalog budget: 8 skills of 1,000 characters are above 2,000 tokens.
	var big []string
	for i := range 8 {
		name := fmt.Sprintf("big-%d", i)
		big = append(big, name)
		writeSkill(t, dir, name, skillFile(name, strings.Repeat("word ", 200), "Body.\n"))
	}
	_, err = loadSkillDAG(t, "      skills: ["+strings.Join(big[:7], ", ")+"]\n", spec.WithDAGsDir(dir))
	require.NoError(t, err)
	_, err = loadSkillDAG(t, "      skills: ["+strings.Join(big, ", ")+"]\n", spec.WithDAGsDir(dir))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "budget of 2000 tokens")
}
