// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sum(data string) string {
	s := sha256.Sum256([]byte(data))
	return hex.EncodeToString(s[:])
}

func textFile(path, content string) ir.SkillFile {
	return ir.SkillFile{Path: path, Size: int64(len(content)), SHA256: sum(content), Text: &content}
}

// The mount holds every file of the skill with its original bytes. SKILL.md is
// not rewritten, and nothing is writable.
func TestMaterializeSkillsMountsTheFolderVerbatim(t *testing.T) {
	t.Parallel()

	const original = "---\nname: guide\ndescription: Read the guide. Use when asked.\nlicense: MIT\nmetadata:\n  author: me\nallowed-tools: Read\n---\n# Guide\r\n\nBody.\n"
	assetDir := t.TempDir()
	asset := []byte{0, 1, 2, 255}
	require.NoError(t, os.MkdirAll(filepath.Join(assetDir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(assetDir, "assets", "logo.bin"), asset, 0o644))

	entry := ir.SkillEntry{
		Name: "guide", Description: "Read the guide. Use when asked.", Body: "# Guide\n", Hash: "h", Dir: assetDir,
		Files: []ir.SkillFile{
			textFile("SKILL.md", original),
			{Path: "assets/logo.bin", Size: 4, SHA256: sum(string(asset))},
			textFile("references/deep/a.md", "# A\n"),
			textFile("scripts/run.sh", "echo hi\n"),
			textFile("references/.gitkeep", ""),
		},
	}
	dir, cleanup, err := materializeSkills([]ir.SkillEntry{entry})
	require.NoError(t, err)
	defer cleanup()

	root := filepath.Join(dir, ".claude", "skills", "guide")
	got, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, original, string(got), "SKILL.md keeps its bytes, with all keys")
	got, err = os.ReadFile(filepath.Join(root, "assets", "logo.bin"))
	require.NoError(t, err)
	assert.Equal(t, asset, got)
	got, err = os.ReadFile(filepath.Join(root, "references", "deep", "a.md"))
	require.NoError(t, err)
	assert.Equal(t, "# A\n", string(got))
	assert.FileExists(t, filepath.Join(root, "references", ".gitkeep"))

	// Windows does not report the permission bits of a POSIX file.
	if runtime.GOOS == "windows" {
		return
	}
	for _, rel := range []string{"SKILL.md", "assets/logo.bin", "scripts/run.sh"} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o400), info.Mode().Perm(), rel+" is read-only and not executable")
	}
}

// A binary file that changed on disk after the run got its version fails the
// mount: the step never gets other bytes.
func TestMaterializeSkillsRefusesAChangedAsset(t *testing.T) {
	t.Parallel()

	assetDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(assetDir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(assetDir, "assets", "logo.bin"), []byte("new"), 0o644))
	entry := ir.SkillEntry{
		Name: "guide", Hash: "h1", Dir: assetDir,
		Files: []ir.SkillFile{
			textFile("SKILL.md", "---\nname: guide\ndescription: d\n---\nb\n"),
			{Path: "assets/logo.bin", Size: 3, SHA256: sum("old")},
		},
	}
	_, _, err := materializeSkills([]ir.SkillEntry{entry})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assets/logo.bin")
	assert.Contains(t, err.Error(), "changed after the run got version h1")
}

func TestMaterializeSkillsRefusesAPathOutsideTheLayout(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"../evil", "/etc/x", "README.md", "references/../../evil"} {
		entry := ir.SkillEntry{Name: "guide", Files: []ir.SkillFile{textFile(path, "x")}}
		_, _, err := materializeSkills([]ir.SkillEntry{entry})
		require.Error(t, err, path)
	}
}

// A record of a skill always has its files. A record with none fails the
// attempt: Dagu does not make a SKILL.md from other fields.
func TestMaterializeSkillsNeedsTheFilesOfTheRecord(t *testing.T) {
	t.Parallel()

	_, _, err := materializeSkills([]ir.SkillEntry{{Name: "empty", Description: "A skill.", Body: "Do it.\n"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no file of the skill")
}
