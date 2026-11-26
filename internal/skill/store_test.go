// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/stretchr/testify/require"
)

// The hash is the SHA-256 of the sha256sum style list of the files, sorted by
// path. It does not depend on the order of creation, the times or the modes.
func TestHashCanonicalForm(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"SKILL.md":        good,
		"references/b.md": "b\n",
		"references/a.md": "a\n",
		"assets/z.bin":    "\x00\x01",
		"scripts/run.sh":  "echo\n",
	}
	dir := writeSkill(t, "demo", files)
	s, err := skill.Read(dir)
	require.NoError(t, err)

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var manifest strings.Builder
	for _, p := range paths {
		sum := sha256.Sum256([]byte(files[p]))
		manifest.WriteString(hex.EncodeToString(sum[:]) + "  " + p + "\n")
	}
	want := sha256.Sum256([]byte(manifest.String()))
	require.Equal(t, hex.EncodeToString(want[:]), s.Hash)
	require.Equal(t, s.Hash, skill.HashFiles(s.Files))

	// Another creation order, other times and other modes give the same hash.
	other := t.TempDir()
	otherDir := filepath.Join(other, "demo")
	for i, path := range slices.Backward(paths) {
		full := filepath.Join(otherDir, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(files[path]), 0o600))
		require.NoError(t, os.Chtimes(full, time.Unix(1000, 0), time.Unix(1000+int64(i), 0)))
	}
	s2, err := skill.Read(otherDir)
	require.NoError(t, err)
	require.Equal(t, s.Hash, s2.Hash)

	// A change of one byte, of one name or a new file changes the hash.
	require.NoError(t, os.WriteFile(filepath.Join(otherDir, "references", "a.md"), []byte("A\n"), 0o600))
	s3, err := skill.Read(otherDir)
	require.NoError(t, err)
	require.NotEqual(t, s.Hash, s3.Hash)
}

// A partial folder (a new file but no SKILL.md yet) is refused, not used.
func TestPartialSkillIsRefused(t *testing.T) {
	t.Parallel()
	dir := writeSkill(t, "demo", map[string]string{"references/a.md": "x"})
	s, err := skill.Read(dir)
	require.NoError(t, err)
	require.False(t, s.Valid())
	require.Contains(t, ruleSet(s.Issues), skill.RuleSkillMissing)
	// The temporary file of an atomic write in progress is not part of the
	// skill: a reader ignores it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md.tmp.123"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(good), 0o644))
	s, err = skill.Read(dir)
	require.NoError(t, err)
	require.True(t, s.Valid(), "%v", s.Issues)
}
