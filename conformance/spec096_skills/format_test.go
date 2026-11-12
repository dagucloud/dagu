// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec096_skills_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const goodDescription = "description: Explain the rules of the demo. Use when the user asks about the demo."

func front(lines ...string) string {
	return "---\n" + strings.Join(lines, "\n") + "\n---\nBody.\n"
}

// manyFiles returns the files of a valid skill with count files in total.
func manyFiles(count int) map[string]string {
	files := map[string]string{"SKILL.md": good("bad")}
	for i := 1; i < count; i++ {
		files[fmt.Sprintf("assets/f%03d.txt", i)] = "x"
	}
	return files
}

// dagu validate refuses a skill that is not in the format. The message names
// the skill, the file and the rule. A step that names the skill fails at start
// with the same message, and the model is never called.
func TestValidateAndStartRefuseEachBadSkill(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 65)
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"no SKILL.md", map[string]string{"references/a.md": "x"}, []string{"SKILL.md", "skill-md-missing"}},
		{"no front matter", map[string]string{"SKILL.md": "# Title\n"}, []string{"SKILL.md", "front-matter-start"}},
		{"no closing line", map[string]string{"SKILL.md": "---\nname: bad\n" + goodDescription + "\nBody.\n"}, []string{"SKILL.md", "front-matter-end"}},
		{"front matter is not YAML", map[string]string{"SKILL.md": front("name: [bad", goodDescription)}, []string{"SKILL.md", "front-matter-yaml"}},
		{"no name", map[string]string{"SKILL.md": front(goodDescription)}, []string{"SKILL.md", "name-missing"}},
		{"name is a list", map[string]string{"SKILL.md": front("name: [bad]", goodDescription)}, []string{"SKILL.md", "name-type"}},
		{"no description", map[string]string{"SKILL.md": front("name: bad")}, []string{"SKILL.md", "description-missing"}},
		{"description is a number", map[string]string{"SKILL.md": front("name: bad", "description: 12")}, []string{"SKILL.md", "description-type"}},
		{"license too long", map[string]string{"SKILL.md": front("name: bad", goodDescription, "license: "+strings.Repeat("x", 129))}, []string{"SKILL.md", "license"}},
		{"unknown key", map[string]string{"SKILL.md": front("name: bad", goodDescription, "model: sonnet")}, []string{"SKILL.md", `"model"`, "front-matter-key"}},
		{"claude extension key", map[string]string{"SKILL.md": front("name: bad", goodDescription, "argument-hint: x")}, []string{`"argument-hint"`, "front-matter-key"}},
		{"duplicate key", map[string]string{"SKILL.md": front("name: bad", "name: bad", goodDescription)}, []string{"SKILL.md", "front-matter-duplicate-key"}},
		{"name differs from the folder", map[string]string{"SKILL.md": front("name: other", goodDescription)}, []string{"SKILL.md", "name-folder", `"other"`}},
		{"name with capitals", map[string]string{"SKILL.md": front("name: Bad", goodDescription)}, []string{"name-charset"}},
		{"description too long", map[string]string{"SKILL.md": front("name: bad", "description: "+strings.Repeat("x", 1025))}, []string{"description-length", "1025"}},
		{"description with a tag", map[string]string{"SKILL.md": front("name: bad", "description: Use the <tool> when asked about things.")}, []string{"description-xml"}},
		{"compatibility too long", map[string]string{"SKILL.md": front("name: bad", goodDescription, "compatibility: "+strings.Repeat("x", 501))}, []string{"compatibility"}},
		{"metadata value is a number", map[string]string{"SKILL.md": front("name: bad", goodDescription, "metadata:", "  version: 1.0")}, []string{"metadata.version"}},
		{"allowed-tools is a list", map[string]string{"SKILL.md": front("name: bad", goodDescription, "allowed-tools:", "  - Read")}, []string{"allowed-tools"}},
		{"empty body", map[string]string{"SKILL.md": "---\nname: bad\n" + goodDescription + "\n---\n\n"}, []string{"body-empty"}},
		{"body above 64 KiB", map[string]string{"SKILL.md": "---\nname: bad\n" + goodDescription + "\n---\n" + strings.Repeat("x", 64*1024+1)}, []string{"body-too-large"}},
		{"SKILL.md is not UTF-8", map[string]string{"SKILL.md": "---\nname: bad\n" + goodDescription + "\n---\n\xff\xfe\n"}, []string{"SKILL.md", "not-utf8"}},
		{"file at the root", map[string]string{"SKILL.md": good("bad"), "README.md": "x"}, []string{"README.md", "root-entry"}},
		{"directory at the root", map[string]string{"SKILL.md": good("bad"), "docs/a.md": "x"}, []string{"docs", "root-entry"}},
		{"hidden file", map[string]string{"SKILL.md": good("bad"), "references/.env": "SECRET=1"}, []string{"references/.env", "hidden-file"}},
		{"reference is not UTF-8", map[string]string{"SKILL.md": good("bad"), "references/a.md": "\xff\xfe"}, []string{"references/a.md", "not-utf8"}},
		{"script is not UTF-8", map[string]string{"SKILL.md": good("bad"), "scripts/a.sh": "\xff\xfe"}, []string{"scripts/a.sh", "not-utf8"}},
		{"file above the limit", map[string]string{"SKILL.md": good("bad"), "assets/big.bin": strings.Repeat("x", 4<<20+1)}, []string{"assets/big.bin", "file-too-large"}},
		{"path with a backslash", map[string]string{"SKILL.md": good("bad"), "references/a\\b.md": "x"}, []string{"path"}},
		{"too many files", manyFiles(257), []string{"too-many-files"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			p.skill("bad", tc.files)
			dag := p.chatDAG("use_bad.yaml", "bad")

			result := p.dagu.Run("validate", dag)
			require.NotEqual(t, 0, result.ExitCode(), "validate must refuse the skill")
			output := result.Stdout() + result.Stderr()
			require.Contains(t, output, `skill "bad"`)
			for _, want := range tc.want {
				require.Contains(t, output, want)
			}

			// A step that names the skill fails, and the model is never called.
			l := newLLM(t)
			start := p.dagu.RunWithEnv([]string{"LLM_BASE_URL=" + l.srv.URL, "DAGU_HOME=" + filepath.Join(t.TempDir(), "home")}, "start", dag)
			require.NotEqual(t, 0, start.ExitCode())
			output = start.Stdout() + start.Stderr()
			require.Contains(t, output, `skill "bad"`)
			require.Contains(t, output, tc.want[len(tc.want)-1])
			require.Empty(t, l.captured())
		})
	}

	t.Run("name rules that need their own folder", func(t *testing.T) {
		t.Parallel()
		for folder, rule := range map[string]string{
			"-bad":      "name-hyphen-edge",
			"bad-":      "name-hyphen-edge",
			"ba--d":     "name-hyphen-double",
			long:        "name-length",
			"bad_skill": "name-charset",
		} {
			p := newProject(t)
			p.skill(folder, map[string]string{"SKILL.md": front("name: "+folder, goodDescription)})
			dag := p.chatDAG("use.yaml", fmt.Sprintf("%q", folder))
			result := p.dagu.Run("validate", dag)
			require.NotEqual(t, 0, result.ExitCode(), folder)
			require.Contains(t, result.Stdout()+result.Stderr(), rule, folder)
		}
	})

	t.Run("links and escapes", func(t *testing.T) {
		t.Parallel()
		p := newProject(t)
		dir := p.skill("bad", map[string]string{"SKILL.md": good("bad"), "references/a.md": "x"})
		outside := filepath.Join(t.TempDir(), "secret.md")
		require.NoError(t, os.WriteFile(outside, []byte("top secret"), 0o600))
		require.NoError(t, os.Symlink(outside, filepath.Join(dir, "references", "link.md")))
		dag := p.chatDAG("use_bad.yaml", "bad")
		result := p.dagu.Run("validate", dag)
		require.NotEqual(t, 0, result.ExitCode())
		require.Contains(t, result.Stdout()+result.Stderr(), "references/link.md")
		require.Contains(t, result.Stdout()+result.Stderr(), "symlink")

		// A hard link is refused too.
		require.NoError(t, os.Remove(filepath.Join(dir, "references", "link.md")))
		require.NoError(t, os.Link(filepath.Join(dir, "references", "a.md"), filepath.Join(dir, "references", "b.md")))
		result = p.dagu.Run("validate", dag)
		require.NotEqual(t, 0, result.ExitCode())
		require.Contains(t, result.Stdout()+result.Stderr(), "hardlink")
	})
}

// dagu validate passes a good skill with the full layout and the optional front
// matter keys. A warning does not refuse the skill, and validate prints it.
func TestValidateAcceptsGoodSkillsAndShowsWarnings(t *testing.T) {
	t.Parallel()

	p := newProject(t)
	p.skill("full", map[string]string{
		"SKILL.md": front("name: full", goodDescription, "license: MIT", "compatibility: Needs git",
			"metadata:", "  author: me", `  version: "1.0"`, "allowed-tools: Bash(git:*) Read"),
		"references/guide.md":     "# Guide\n",
		"references/.gitkeep":     "",
		"scripts/run.sh":          "echo hi\n",
		"assets/logo.bin":         "\x00\x01\xff",
		"assets/deep/er/data.csv": "a,b\n",
	})
	result := p.dagu.Run("validate", p.chatDAG("use_full.yaml", "full"))
	result.ExpectExitCode(0)
	require.NotContains(t, result.Stdout()+result.Stderr(), "description-short")

	// Warnings: a short description, a body of many lines and many tokens, a deep
	// reference and a reference that read_skill_file cannot return.
	p.skill("warned", map[string]string{
		"SKILL.md":                "---\nname: warned\ndescription: Short one\n---\n" + strings.Repeat("a line of the body that is long enough\n", 501),
		"references/a/b/deep.md":  "x",
		"references/other/big.md": strings.Repeat("x", 262145),
	})
	result = p.dagu.Run("validate", p.chatDAG("use_warned.yaml", "warned"))
	result.ExpectExitCode(0)
	output := result.Stdout() + result.Stderr()
	for _, want := range []string{"warned", "description-short", "body-lines", "body-tokens", "reference-depth", "file-too-large-to-read"} {
		require.Contains(t, output, want)
	}
}

// The catalog is limited to 20 skills and 2,000 estimated tokens. An excess is
// an error, never a silent cut.
func TestCatalogCaps(t *testing.T) {
	t.Parallel()

	p := newProject(t)
	var names []string
	for i := range 21 {
		name := fmt.Sprintf("topic-%02d", i)
		p.skill(name, map[string]string{"SKILL.md": good(name)})
		names = append(names, name)
	}
	p.dagu.Run("validate", p.chatDAG("twenty.yaml", names[:20]...)).ExpectExitCode(0)
	result := p.dagu.Run("validate", p.chatDAG("twentyone.yaml", names...))
	require.NotEqual(t, 0, result.ExitCode())
	require.Contains(t, result.Stdout()+result.Stderr(), "20 skills at most")

	var big []string
	for i := range 8 {
		name := fmt.Sprintf("big-%d", i)
		p.skill(name, map[string]string{"SKILL.md": front("name: "+name, "description: "+strings.Repeat("word ", 200))})
		big = append(big, name)
	}
	result = p.dagu.Run("validate", p.chatDAG("budget.yaml", big...))
	require.NotEqual(t, 0, result.ExitCode())
	require.Contains(t, result.Stdout()+result.Stderr(), "budget of 2000 tokens")
	p.dagu.Run("validate", p.chatDAG("budget_ok.yaml", big[:7]...)).ExpectExitCode(0)
}
