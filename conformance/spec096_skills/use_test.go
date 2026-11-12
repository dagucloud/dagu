// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package spec096_skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// chat.completion: the system message lists the skills, load_skill returns the
// body in tags with the list of the files, read_skill_file returns a file of
// references/ or scripts/, and a repeated load_skill gets a short note. The
// model cannot read an asset or a path outside the skill. The run record holds
// the hash of each skill.
func TestChatCompletionUsesLoadSkillAndReadSkillFile(t *testing.T) {
	t.Parallel()

	p := newProject(t)
	dir := p.skill("guide", map[string]string{
		"SKILL.md":            good("guide"),
		"references/rules.md": "# Rules\nRule one.\n",
		"scripts/check.sh":    "echo check\n",
		"assets/logo.bin":     "\x00\x01\xff",
	})
	p.skill("plain", map[string]string{"SKILL.md": good("plain")})
	l := newLLM(t,
		[2]string{"load_skill", `{"name":"guide"}`},
		[2]string{"load_skill", `{"name":"guide"}`},
		[2]string{"read_skill_file", `{"name":"guide","path":"references/rules.md"}`},
		[2]string{"read_skill_file", `{"name":"guide","path":"scripts/check.sh"}`},
		[2]string{"read_skill_file", `{"name":"guide","path":"assets/logo.bin"}`},
		[2]string{"read_skill_file", `{"name":"guide","path":"../../etc/passwd"}`},
	)
	dag := p.chatDAG("use.yaml", "guide", "plain")
	home := filepath.Join(t.TempDir(), "home")
	p.dagu.RunWithEnv([]string{"LLM_BASE_URL=" + l.srv.URL, "DAGU_HOME=" + home}, "start", dag).ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 7)
	first := requests[0]
	require.Equal(t, []string{"load_skill", "read_skill_file"}, first.toolNames())
	require.Contains(t, first.system(), "- guide: Explain the rules of guide.")
	require.Contains(t, first.system(), "- plain: Explain the rules of plain.")
	require.NotContains(t, first.system(), "Follow the rules", "the catalog holds no body")

	loaded := requests[1].lastToolResult()
	require.True(t, strings.HasPrefix(loaded, "<skill_content name=\"guide\">\n# guide"), loaded)
	require.Contains(t, loaded, "<file>references/rules.md</file>")
	require.Contains(t, loaded, "<file>scripts/check.sh</file>")
	require.NotContains(t, loaded, "assets/logo.bin")
	require.NotContains(t, loaded, "description:", "the front matter is removed")

	require.Contains(t, requests[2].lastToolResult(), "already loaded")
	require.Equal(t, "# Rules\nRule one.\n", requests[3].lastToolResult())
	require.Equal(t, "echo check\n", requests[4].lastToolResult())
	require.Contains(t, requests[5].lastToolResult(), "cannot read it", "an asset is not readable")
	require.Contains(t, requests[6].lastToolResult(), "has no file", "a path outside the skill is refused")

	hashes := savedSkillHashes(t, home, "ask")
	require.Equal(t, folderHash(t, dir), hashes["guide"])
	require.Len(t, hashes["guide"], 64)
	require.NotEmpty(t, hashes["plain"])
}

// Spec 096, "Secrets": Dagu does not evaluate a value reference in a skill. The
// body and a reference file reach the model with the reference as text, and the
// value of the variable reaches no request.
func TestSkillTextHasNoValueReference(t *testing.T) {
	t.Parallel()

	const value = "skill-value-that-must-stay-out"
	p := newProject(t)
	p.skill("refs", map[string]string{
		"SKILL.md":            "---\nname: refs\ndescription: Explain the rules of refs. Use when the user asks about refs.\n---\n# refs\n\nThe token is ${SKILL_VALUE}.\n",
		"references/token.md": "Token: ${SKILL_VALUE}\n",
	})
	l := newLLM(t,
		[2]string{"load_skill", `{"name":"refs"}`},
		[2]string{"read_skill_file", `{"name":"refs","path":"references/token.md"}`},
	)
	dag := p.chatDAG("use.yaml", "refs")
	p.dagu.RunWithEnv([]string{"LLM_BASE_URL=" + l.srv.URL, "SKILL_VALUE=" + value}, "start", dag).ExpectExitCode(0)

	requests := l.captured()
	require.Len(t, requests, 3)
	require.Contains(t, requests[1].lastToolResult(), "The token is ${SKILL_VALUE}.")
	require.Equal(t, "Token: ${SKILL_VALUE}\n", requests[2].lastToolResult())
	for i, request := range requests {
		for _, message := range request.Messages {
			require.NotContains(t, message.Content, value, "request %d", i)
		}
	}
}

// A skill with no readable file adds no read_skill_file tool.
func TestNoReadSkillFileToolWithoutReadableFiles(t *testing.T) {
	t.Parallel()

	p := newProject(t)
	p.skill("plain", map[string]string{"SKILL.md": good("plain"), "assets/logo.bin": "\x00"})
	l := newLLM(t)
	p.dagu.RunWithEnv([]string{"LLM_BASE_URL=" + l.srv.URL}, "start", p.chatDAG("use.yaml", "plain")).ExpectExitCode(0)
	requests := l.captured()
	require.Len(t, requests, 1)
	require.Equal(t, []string{"load_skill"}, requests[0].toolNames())
}

// agent.run: Dagu mounts the whole folder of the skill, verbatim, under
// .claude/skills/<name>/ of the agent directory. SKILL.md keeps its bytes and its
// keys. Every file is read-only. The run record holds the hash of the skill.
func TestAgentRunMountsTheFolderVerbatim(t *testing.T) {
	t.Parallel()

	p := newProject(t)
	skillMD := "---\nname: guide\ndescription: Explain the rules of the guide. Use when the user asks.\nlicense: MIT\nmetadata:\n  author: me\nallowed-tools: Read\n---\n# Guide\r\n\nBody with a CRLF line.\n"
	files := map[string]string{
		"SKILL.md":                skillMD,
		"references/guide.md":     "# Guide\n",
		"references/.gitkeep":     "",
		"scripts/run.sh":          "#!/bin/sh\necho hi\n",
		"assets/logo.bin":         "\x00\x01\x02\xff\xfe",
		"assets/deep/er/data.csv": "a,b\n",
	}
	dir := p.skill("guide", files)
	fake := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	p.seedEndpoint(home)
	harness.WriteIsolationOff(t, home)
	env := []string{
		"PATH=" + p.dagu.ProjectPath("bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DAGU_HOME=" + home, "DAGU_COORDINATOR_ENABLED=false", "FAKE_CLAUDE_DIR=" + fake,
	}
	p.dagu.RunWithEnv(env, "start", p.agentDAG("use.yaml", "guide")).ExpectExitCode(0)

	manifest, err := os.ReadFile(filepath.Join(fake, "manifest"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(manifest)), "\n")
	require.Len(t, lines, len(files))
	got := map[string]string{}
	for _, line := range lines {
		parts := strings.SplitN(line, " ", 3)
		require.Len(t, parts, 3, line)
		got[parts[2]] = parts[0] + " " + parts[1]
	}
	for rel, content := range files {
		want, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		require.NoError(t, err)
		require.Equal(t, content, string(want))
		mounted := got["guide/"+rel]
		require.NotEmpty(t, mounted, "the file %s is mounted", rel)
		sum, mode, _ := strings.Cut(mounted, " ")
		require.Equal(t, sha256Hex(content), sum, "the file %s keeps its bytes", rel)
		require.Equal(t, "400", mode, "the file %s is read-only", rel)
	}
	argv, err := os.ReadFile(filepath.Join(fake, "argv"))
	require.NoError(t, err)
	require.Contains(t, string(argv), "--add-dir")
	require.Contains(t, string(argv), "Skill")

	require.Equal(t, folderHash(t, dir), savedSkillHashes(t, home, "code")["guide"])
}
