// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/stretchr/testify/require"
)

// good is a valid SKILL.md for the folder "demo".
const good = "---\nname: demo\ndescription: Explain the demo rules. Use when the user asks about the demo.\n---\n# Demo\n\nBody text.\n"

// writeSkill creates the folder name under a new directory. files maps a
// relative path to its content.
func writeSkill(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func skillMD(front string) string { return "---\n" + front + "\n---\nBody.\n" }

func ruleSet(issues []skill.Issue) map[string]skill.Issue {
	out := map[string]skill.Issue{}
	for _, i := range issues {
		out[i.Rule] = i
	}
	return out
}

// One fixture for each rule of the format. A bad fixture gives the rule as an
// error and names the file. A good fixture gives no error.
func TestFormatRules(t *testing.T) {
	t.Parallel()

	long := func(n int) string { return strings.Repeat("a", n) }
	cases := []struct {
		name    string
		folder  string
		content string
		rule    string // empty: the skill is valid
		file    string
		sev     string
	}{
		{name: "valid minimal", folder: "demo", content: good},
		{name: "valid with all keys", folder: "demo", content: skillMD("name: demo\ndescription: Explain the demo rules. Use when asked.\nlicense: MIT\ncompatibility: Needs git\nmetadata:\n  author: me\n  version: \"1.0\"\nallowed-tools: Bash(git:*) Read")},
		{name: "valid unicode name", folder: "données", content: skillMD("name: données\ndescription: Explain the data rules. Use when asked.")},
		{name: "valid digit first", folder: "7zip", content: skillMD("name: 7zip\ndescription: Explain the archive rules. Use when asked.")},
		{name: "no front matter", folder: "demo", content: "# Demo\n", rule: skill.RuleFrontMatterStart, file: "SKILL.md"},
		{name: "unclosed front matter", folder: "demo", content: "---\nname: demo\n", rule: skill.RuleFrontMatterEnd, file: "SKILL.md"},
		{name: "bad yaml", folder: "demo", content: "---\nname: [demo\n---\nBody\n", rule: skill.RuleFrontMatterYAML, file: "SKILL.md"},
		{name: "duplicate key", folder: "demo", content: skillMD("name: demo\nname: demo\ndescription: A description of the skill."), rule: skill.RuleFrontMatterDup, file: "SKILL.md"},
		{name: "unknown key", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\nmodel: sonnet"), rule: skill.RuleFrontMatterKey, file: "SKILL.md"},
		{name: "claude extension key", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\nargument-hint: x"), rule: skill.RuleFrontMatterKey, file: "SKILL.md"},
		{name: "name missing", folder: "demo", content: skillMD("description: A description of the skill."), rule: skill.RuleNameMissing, file: "SKILL.md"},
		{name: "name not string", folder: "demo", content: skillMD("name: 12\ndescription: A description of the skill."), rule: skill.RuleNameType, file: "SKILL.md"},
		{name: "name differs from folder", folder: "demo", content: skillMD("name: other\ndescription: A description of the skill."), rule: skill.RuleNameFolder, file: "SKILL.md"},
		{name: "name upper case", folder: "Demo", content: skillMD("name: Demo\ndescription: A description of the skill."), rule: skill.RuleNameCharset, file: "SKILL.md"},
		{name: "name underscore", folder: "a_b", content: skillMD("name: a_b\ndescription: A description of the skill."), rule: skill.RuleNameCharset, file: "SKILL.md"},
		{name: "name leading hyphen", folder: "-demo", content: skillMD("name: -demo\ndescription: A description of the skill."), rule: skill.RuleNameHyphenEdge, file: "SKILL.md"},
		{name: "name trailing hyphen", folder: "demo-", content: skillMD("name: demo-\ndescription: A description of the skill."), rule: skill.RuleNameHyphenEdge, file: "SKILL.md"},
		{name: "name double hyphen", folder: "de--mo", content: skillMD("name: de--mo\ndescription: A description of the skill."), rule: skill.RuleNameHyphenDouble, file: "SKILL.md"},
		{name: "name 64 characters", folder: long(64), content: skillMD("name: " + long(64) + "\ndescription: A description of the skill.")},
		{name: "name 65 characters", folder: long(65), content: skillMD("name: " + long(65) + "\ndescription: A description of the skill."), rule: skill.RuleNameLength, file: "SKILL.md"},
		{name: "description missing", folder: "demo", content: skillMD("name: demo"), rule: skill.RuleDescriptionMiss, file: "SKILL.md"},
		{name: "description empty", folder: "demo", content: skillMD("name: demo\ndescription: \"  \""), rule: skill.RuleDescriptionMiss, file: "SKILL.md"},
		{name: "description 1024", folder: "demo", content: skillMD("name: demo\ndescription: " + long(1024))},
		{name: "description 1025", folder: "demo", content: skillMD("name: demo\ndescription: " + long(1025)), rule: skill.RuleDescriptionLength, file: "SKILL.md"},
		{name: "description xml", folder: "demo", content: skillMD("name: demo\ndescription: Use the <tool> when asked about things."), rule: skill.RuleDescriptionXML, file: "SKILL.md"},
		{name: "description comparison is not xml", folder: "demo", content: skillMD("name: demo\ndescription: Use when a < b and c > d is true, always.")},
		{name: "license too long", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\nlicense: " + long(129)), rule: skill.RuleLicense, file: "SKILL.md"},
		{name: "compatibility empty", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\ncompatibility: \"\""), rule: skill.RuleCompatibility, file: "SKILL.md"},
		{name: "compatibility 501", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\ncompatibility: " + long(501)), rule: skill.RuleCompatibility, file: "SKILL.md"},
		{name: "compatibility 500", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\ncompatibility: " + long(500))},
		{name: "metadata not a map", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\nmetadata: x"), rule: skill.RuleMetadata, file: "SKILL.md"},
		{name: "metadata number value", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\nmetadata:\n  version: 1.0"), rule: skill.RuleMetadata, file: "SKILL.md"},
		{name: "allowed-tools list", folder: "demo", content: skillMD("name: demo\ndescription: A description of the skill.\nallowed-tools:\n  - Read"), rule: skill.RuleAllowedTools, file: "SKILL.md"},
		{name: "body empty", folder: "demo", content: "---\nname: demo\ndescription: A description of the skill.\n---\n\n", rule: skill.RuleBodyEmpty, file: "SKILL.md"},
		{name: "body too large", folder: "demo", content: "---\nname: demo\ndescription: A description of the skill.\n---\n" + strings.Repeat("x", skill.MaxBodyBytes+1), rule: skill.RuleBodyTooLarge, file: "SKILL.md"},
		{name: "body exactly 64 KiB", folder: "demo", content: "---\nname: demo\ndescription: A description of the skill.\n---\n" + strings.Repeat("x", skill.MaxBodyBytes), rule: skill.RuleBodyTokens, file: "SKILL.md", sev: skill.SeverityWarning},
		{name: "warning body lines", folder: "demo", content: "---\nname: demo\ndescription: A description of the skill.\n---\n" + strings.Repeat("l\n", 501), rule: skill.RuleBodyLines, file: "SKILL.md", sev: skill.SeverityWarning},
		{name: "warning body tokens", folder: "demo", content: "---\nname: demo\ndescription: A description of the skill.\n---\n" + strings.Repeat("x", 18600), rule: skill.RuleBodyTokens, file: "SKILL.md", sev: skill.SeverityWarning},
		{name: "warning short description", folder: "demo", content: skillMD("name: demo\ndescription: Short one"), rule: skill.RuleDescriptionShort, file: "SKILL.md", sev: skill.SeverityWarning},
		{name: "not utf8", folder: "demo", content: "---\nname: demo\ndescription: A description of the skill.\n---\n\xff\xfe\n", rule: skill.RuleNotUTF8, file: "SKILL.md"},
		{name: "crlf is accepted", folder: "demo", content: "---\r\nname: demo\r\ndescription: A description of the skill.\r\n---\r\nBody\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeSkill(t, tc.folder, map[string]string{"SKILL.md": tc.content})
			s, err := skill.Read(dir)
			require.NoError(t, err)
			if tc.rule == "" {
				require.True(t, s.Valid(), "%v", s.Issues)
				require.NoError(t, s.Err())
				return
			}
			issue, ok := ruleSet(s.Issues)[tc.rule]
			require.True(t, ok, "rule %s not in %v", tc.rule, s.Issues)
			require.Equal(t, tc.file, issue.File)
			if tc.sev == skill.SeverityWarning {
				require.Equal(t, skill.SeverityWarning, issue.Severity)
				require.True(t, s.Valid(), "a warning must not refuse the skill: %v", s.Issues)
				return
			}
			require.Equal(t, skill.SeverityError, issue.Severity)
			require.False(t, s.Valid())
			err = s.Err()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.folder)
			require.Contains(t, err.Error(), tc.file)
			require.Contains(t, err.Error(), tc.rule)
		})
	}
}

func TestUnknownKeyIsNamed(t *testing.T) {
	t.Parallel()
	dir := writeSkill(t, "demo", map[string]string{"SKILL.md": skillMD("name: demo\ndescription: A description of the skill.\nwhen_to_use: now")})
	s, err := skill.Read(dir)
	require.NoError(t, err)
	require.Contains(t, s.Err().Error(), `"when_to_use"`)
}

// A folder that the server cannot read gives the rule unreadable, and the
// message names no path of the server. The test needs a user other than root.
func TestUnreadableFolderNamesNoServerPath(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder of mode 0000")
	}
	if runtime.GOOS == "windows" {
		t.Skip("a folder mode of 0 does not stop a read on Windows")
	}
	dir := writeSkill(t, "demo", map[string]string{"SKILL.md": skillMD("name: demo\ndescription: A description of the skill."), "references/a.md": "a"})
	locked := filepath.Join(dir, "references")
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	s, err := skill.Read(dir)
	require.NoError(t, err)
	require.Len(t, s.Issues, 1)
	require.Equal(t, skill.RuleUnreadable, s.Issues[0].Rule)
	require.NotContains(t, s.Issues[0].Message, dir)
}

// A skill folder that is gone is not found.
func TestReadOfAMissingFolder(t *testing.T) {
	t.Parallel()
	_, err := skill.Read(filepath.Join(t.TempDir(), "gone"))
	require.ErrorIs(t, err, skill.ErrNotFound)
}

// The folder rules: the layout, links, hidden files, depth, text and limits.
func TestFolderRules(t *testing.T) {
	t.Parallel()

	t.Run("full layout is valid", func(t *testing.T) {
		t.Parallel()
		dir := writeSkill(t, "demo", map[string]string{
			"SKILL.md":                good,
			"references/a.md":         "# A\n",
			"references/.gitkeep":     "",
			"scripts/run.sh":          "echo hi\n",
			"scripts/lib/util.py":     "print(1)\n",
			"assets/logo.png":         "\x89PNG\x00\xff",
			"assets/deep/er/data.bin": "\x00\x01\x02",
		})
		s, err := skill.Read(dir)
		require.NoError(t, err)
		require.True(t, s.Valid(), "%v", s.Issues)
		require.Len(t, s.Files, 7)
		file, ok := s.File("assets/logo.png")
		require.True(t, ok)
		require.False(t, file.Text)
	})

	cases := []struct {
		name  string
		files map[string]string
		rule  string
		file  string
		sev   string
	}{
		{name: "missing SKILL.md", files: map[string]string{"references/a.md": "x"}, rule: skill.RuleSkillMissing, file: "SKILL.md"},
		{name: "root file", files: map[string]string{"SKILL.md": good, "README.md": "x"}, rule: skill.RuleRootEntry, file: "README.md"},
		{name: "root directory", files: map[string]string{"SKILL.md": good, "docs/a.md": "x"}, rule: skill.RuleRootEntry, file: "docs"},
		{name: "lower case skill.md", files: map[string]string{"skill.md": good}, rule: skill.RuleRootEntry, file: "skill.md"},
		{name: "hidden file", files: map[string]string{"SKILL.md": good, "references/.env": "x"}, rule: skill.RuleHiddenFile, file: "references/.env"},
		{name: "hidden directory", files: map[string]string{"SKILL.md": good, "assets/.cache/x": "x"}, rule: skill.RuleHiddenFile, file: "assets/.cache"},
		{name: "root hidden file", files: map[string]string{"SKILL.md": good, ".gitkeep": ""}, rule: skill.RuleRootEntry, file: ".gitkeep"},
		{name: "reference not utf8", files: map[string]string{"SKILL.md": good, "references/a.md": "\xff\xfe"}, rule: skill.RuleNotUTF8, file: "references/a.md"},
		{name: "script not utf8", files: map[string]string{"SKILL.md": good, "scripts/a.sh": "\xff\xfe"}, rule: skill.RuleNotUTF8, file: "scripts/a.sh"},
		{name: "file too large", files: map[string]string{"SKILL.md": good, "assets/big.bin": strings.Repeat("x", skill.MaxFileBytes+1)}, rule: skill.RuleFileTooLarge, file: "assets/big.bin"},
		{name: "depth warning", files: map[string]string{"SKILL.md": good, "references/a/b.md": "x"}, rule: skill.RuleReferenceDepth, file: "references/a/b.md", sev: skill.SeverityWarning},
		{name: "too large to read warning", files: map[string]string{"SKILL.md": good, "references/big.md": strings.Repeat("x", skill.MaxReadFileBytes+1)}, rule: skill.RuleReadTooLarge, file: "references/big.md", sev: skill.SeverityWarning},
		{name: "backslash in name", files: map[string]string{"SKILL.md": good, "references/a\\b.md": "x"}, rule: skill.RulePath, file: "references/a\\b.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if runtime.GOOS == "windows" && strings.Contains(tc.file, "\\") {
				t.Skip("a file name cannot have a backslash on Windows")
			}
			dir := writeSkill(t, "demo", tc.files)
			s, err := skill.Read(dir)
			require.NoError(t, err)
			issue, ok := ruleSet(s.Issues)[tc.rule]
			require.True(t, ok, "rule %s not in %v", tc.rule, s.Issues)
			require.Equal(t, tc.file, issue.File)
			if tc.sev == skill.SeverityWarning {
				require.True(t, s.Valid(), "%v", s.Issues)
			} else {
				require.False(t, s.Valid())
				require.Contains(t, s.Err().Error(), tc.file)
			}
		})
	}

	t.Run("too many files", func(t *testing.T) {
		t.Parallel()
		files := map[string]string{"SKILL.md": good}
		for i := 0; i <= skill.MaxFiles; i++ {
			files[fmt.Sprintf("assets/f%04d", i)] = "x"
		}
		s, err := skill.Read(writeSkill(t, "demo", files))
		require.NoError(t, err)
		require.Contains(t, ruleSet(s.Issues), skill.RuleTooManyFiles)
	})

	t.Run("symlink to a file", func(t *testing.T) {
		t.Parallel()
		dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good, "references/a.md": "x"})
		secret := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))
		require.NoError(t, os.Symlink(secret, filepath.Join(dir, "references", "link.md")))
		s, err := skill.Read(dir)
		require.NoError(t, err)
		require.Equal(t, "references/link.md", ruleSet(s.Issues)[skill.RuleSymlink].File)
		require.False(t, s.Valid())
		_, ok := s.Text("references/link.md")
		require.False(t, ok, "a symbolic link is never read")
	})

	t.Run("symlinked SKILL.md", func(t *testing.T) {
		t.Parallel()
		dir := writeSkill(t, "demo", map[string]string{})
		secret := filepath.Join(t.TempDir(), "SKILL.md")
		require.NoError(t, os.WriteFile(secret, []byte(good), 0o600))
		require.NoError(t, os.Symlink(secret, filepath.Join(dir, "SKILL.md")))
		s, err := skill.Read(dir)
		require.NoError(t, err)
		require.Contains(t, ruleSet(s.Issues), skill.RuleSymlink)
		require.False(t, s.Valid())
		require.Empty(t, s.Raw())
	})

	t.Run("symlink to a directory that escapes", func(t *testing.T) {
		t.Parallel()
		dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good})
		outside := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outside, "x.md"), []byte("x"), 0o600))
		require.NoError(t, os.Symlink(outside, filepath.Join(dir, "references")))
		s, err := skill.Read(dir)
		require.NoError(t, err)
		require.Contains(t, ruleSet(s.Issues), skill.RuleRootEntry)
		require.False(t, s.Valid())
		for _, f := range s.Files {
			require.NotContains(t, f.Path, "x.md")
		}
	})

	t.Run("symlinked skill folder", func(t *testing.T) {
		t.Parallel()
		real := writeSkill(t, "real", map[string]string{"SKILL.md": good})
		link := filepath.Join(t.TempDir(), "demo")
		require.NoError(t, os.Symlink(real, link))
		s, err := skill.Read(link)
		require.NoError(t, err)
		require.Contains(t, ruleSet(s.Issues), skill.RuleFolderLink)
	})

	t.Run("hard link", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("the link count of a file is not read on Windows")
		}
		dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good, "references/a.md": "x"})
		require.NoError(t, os.Link(filepath.Join(dir, "references", "a.md"), filepath.Join(dir, "references", "b.md")))
		s, err := skill.Read(dir)
		require.NoError(t, err)
		require.Contains(t, ruleSet(s.Issues), skill.RuleHardlink)
		require.False(t, s.Valid())
	})

	t.Run("hard link of SKILL.md", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("the link count of a file is not read on Windows")
		}
		dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good})
		require.NoError(t, os.Link(filepath.Join(dir, "SKILL.md"), filepath.Join(t.TempDir(), "outside.md")))
		s, err := skill.Read(dir)
		require.NoError(t, err)
		require.Contains(t, ruleSet(s.Issues), skill.RuleHardlink)
		require.False(t, s.Valid())
	})

	t.Run("missing folder", func(t *testing.T) {
		t.Parallel()
		_, err := skill.Read(filepath.Join(t.TempDir(), "nope"))
		require.ErrorIs(t, err, skill.ErrNotFound)
	})
}

func TestCheckExistingPathRefusesEveryEscape(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"README.md", "docs/a.md", "references/.env"} {
		_, err := skill.CheckExistingPath(ok)
		require.NoError(t, err, ok)
	}
	for _, bad := range []string{"", "../x", "../../x", "a/../../x", "/abs", "a//b", "./a", "a/.", "a\\b", ".locks/x"[:0], "a/\x00"} {
		_, err := skill.CheckExistingPath(bad)
		require.Error(t, err, bad)
	}
}

func TestCheckPath(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"SKILL.md", "references/a.md", "assets/x/y.png", "scripts/run.sh", "references/.gitkeep"} {
		_, err := skill.CheckPath(ok)
		require.NoError(t, err, ok)
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "references/../../x", "references/./a.md", "a.md", "docs/a.md", "references\\a.md", "references/.env", "references//a.md", "references/a\x00b", ".gitkeep", "references/", "SKILL.md/x"} {
		_, err := skill.CheckPath(bad)
		require.Error(t, err, bad)
	}
}
