// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
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

// A repeated read of an unchanged folder gives the same skill, and a change of
// a file that keeps its time stamp still shows in the next read when it
// changes the size.
func TestReadSeesEveryChange(t *testing.T) {
	t.Parallel()
	dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good, "references/a.md": "one"})
	// Make the files old, so that the cache is allowed to keep them.
	old := time.Now().Add(-time.Hour)
	for _, p := range []string{"SKILL.md", "references/a.md"} {
		require.NoError(t, os.Chtimes(filepath.Join(dir, p), old, old))
	}
	first, err := skill.Read(dir)
	require.NoError(t, err)
	again, err := skill.Read(dir)
	require.NoError(t, err)
	require.Equal(t, first.Hash, again.Hash)

	// The time stamp is set back, so only the size tells the change.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "references", "a.md"), []byte("three"), 0o644))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "references", "a.md"), old, old))
	next, err := skill.Read(dir)
	require.NoError(t, err)
	require.NotEqual(t, first.Hash, next.Hash)
	data, ok := next.Text("references/a.md")
	require.True(t, ok)
	require.Equal(t, "three", string(data))
}

// A read of a folder whose entries are older than 2 seconds is served from
// the cache and reads no file again. A recent change is always read from the
// files (Spec 096, "Source of truth"). The test changes a package variable,
// so it does not run in parallel.
func TestReadCachesOnlyASettledFolder(t *testing.T) {
	reads := 0
	skill.AroundReadFiles(t, func() { reads++ }, func() {})
	read := func(dir string) {
		t.Helper()
		_, err := skill.Read(dir)
		require.NoError(t, err)
	}

	// A time stamp in the future stays younger than 2 seconds however slow the
	// test runs.
	fresh := writeSkill(t, "demo", map[string]string{"SKILL.md": good})
	future := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(fresh, "SKILL.md"), future, future))
	read(fresh)
	read(fresh)
	require.Equal(t, 2, reads, "a change younger than 2 seconds is never cached")

	settled := writeSkill(t, "demo", map[string]string{"SKILL.md": good})
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(settled, "SKILL.md"), old, old))
	reads = 0
	read(settled)
	read(settled)
	require.Equal(t, 1, reads, "the second read of a settled folder comes from the cache")
}

// A change of the layout that leaves every file as it was shows in the next
// read, also when the cache holds the folder (Spec 096, "Source of truth").
func TestReadSeesALayoutChange(t *testing.T) {
	t.Parallel()
	// age gives every entry of dir the same old time, so that the cache keeps
	// the folder and the change leaves the times of the files as they were.
	old := time.Now().Add(-time.Hour)
	age := func(t *testing.T, dir string) {
		require.NoError(t, filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Chtimes(p, old, old)
		}))
	}
	cases := []struct {
		name   string
		change func(t *testing.T, dir string)
		rule   string
	}{
		{name: "folder at the root", rule: skill.RuleRootEntry, change: func(t *testing.T, dir string) {
			require.NoError(t, os.Mkdir(filepath.Join(dir, "docs"), 0o755))
		}},
		{name: "hidden folder", rule: skill.RuleHiddenFile, change: func(t *testing.T, dir string) {
			require.NoError(t, os.Mkdir(filepath.Join(dir, "references", ".cache"), 0o755))
		}},
		{name: "hard link from outside", rule: skill.RuleHardlink, change: func(t *testing.T, dir string) {
			require.NoError(t, os.Link(filepath.Join(dir, "SKILL.md"), filepath.Join(t.TempDir(), "outside.md")))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good, "references/a.md": "x"})
			age(t, dir)
			first, err := skill.Read(dir)
			require.NoError(t, err)
			require.True(t, first.Valid(), "%v", first.Issues)

			tc.change(t, dir)
			age(t, dir)
			next, err := skill.Read(dir)
			require.NoError(t, err)
			require.Contains(t, ruleSet(next.Issues), tc.rule)
			require.False(t, next.Valid())
		})
	}
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
