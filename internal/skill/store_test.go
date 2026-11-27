// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/stretchr/testify/assert"
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
// changes the size. The test changes a package variable, so it does not run
// in parallel.
func TestReadSeesEveryChange(t *testing.T) {
	skill.SettleClock(t)
	dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good, "references/a.md": "one"})
	old := time.Now().Add(-time.Hour)
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
	data, err := next.Text("references/a.md")
	require.NoError(t, err)
	require.Equal(t, "three", string(data))
}

// A read of a folder whose entries are older than 2 seconds is served from
// the cache and reads no file again. A recent change is always read from the
// files (Spec 096, "Source of truth"). The test changes package variables,
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
	skill.SettleClock(t)
	reads = 0
	read(settled)
	read(settled)
	require.Equal(t, 1, reads, "the second read of a settled folder comes from the cache")
}

// A read that takes longer than 2 seconds does not make a recent change
// settled: the time of the walk decides. The test changes package variables,
// so it does not run in parallel.
func TestSlowReadIsNotCached(t *testing.T) {
	dir := writeSkill(t, "demo", map[string]string{"SKILL.md": good})
	// A time stamp in the future keeps the change recent however slow the test
	// runs, until the clock moves.
	future := time.Now().Add(30 * time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "SKILL.md"), future, future))
	reads := 0
	skill.AroundReadFiles(t, func() {
		// The first read of the files ends after the change is settled.
		if reads++; reads == 1 {
			skill.SettleClock(t)
		}
	}, func() {})
	for range 2 {
		_, err := skill.Read(dir)
		require.NoError(t, err)
	}
	require.Equal(t, 2, reads, "a read that began before the change settled is not cached")
}

// The cache counts the list of files of a read, so a folder of many small files
// with long names cannot grow the cache past its bound.
func TestCacheCountsTheFileList(t *testing.T) {
	t.Parallel()
	files := map[string]string{"SKILL.md": good}
	var names int64
	for i := range 200 {
		rel := fmt.Sprintf("assets/%s%03d.bin", strings.Repeat("a", 200), i)
		files[rel] = "x"
		names += int64(len(rel))
	}
	s, err := skill.Read(writeSkill(t, "demo", files))
	require.NoError(t, err)
	require.Greater(t, skill.CacheCost(s), names)
}

// A change of the layout that leaves every file as it was shows in the next
// read, also when the cache holds the folder (Spec 096, "Source of truth").
// The test changes a package variable, so it does not run in parallel.
func TestReadSeesALayoutChange(t *testing.T) {
	skill.SettleClock(t)
	// age gives every entry of dir the same old time, so that the change leaves
	// the times of the files as they were.
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

func newStore(t *testing.T) *skill.Store {
	t.Helper()
	return skill.NewStore(filepath.Join(t.TempDir(), "skills"))
}

func create(t *testing.T, s *skill.Store, name string) *skill.Skill {
	t.Helper()
	content := "---\nname: " + name + "\ndescription: Explain the " + name + " rules. Use when asked.\n---\nBody.\n"
	sk, err := s.WriteFile(context.Background(), name, "SKILL.md", []byte(content), skill.Precondition{MustNotExist: true})
	require.NoError(t, err)
	require.True(t, sk.Valid(), "%v", sk.Issues)
	return sk
}

// The reader skips a file with the name of a temporary file of a write, so such
// a file would be outside the hash, the quota and the bundle.
func TestWriteRefusesTheNameOfATemporaryFile(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	created := create(t, s, "demo")
	_, err := s.WriteFile(context.Background(), "demo", "references/notes.tmp.1", []byte("x"), skill.Precondition{IfMatch: created.Hash})
	var invalid *skill.InvalidError
	require.ErrorAs(t, err, &invalid)
}

// A change of a skill that does not exist makes no lock folder, so a request
// for any name cannot fill the skills directory.
func TestChangeOfAMissingSkillMakesNoLockFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	create(t, s, "demo")
	hash := strings.Repeat("0", 64)
	_, err := s.WriteFile(ctx, "ghost", "references/a.md", []byte("x"), skill.Precondition{IfMatch: hash})
	require.ErrorIs(t, err, skill.ErrNotFound)
	require.ErrorIs(t, s.DeleteSkill(ctx, "ghost", skill.Precondition{IfMatch: hash}), skill.ErrNotFound)
	_, statErr := os.Stat(filepath.Join(filepath.Dir(s.Dir("ghost")), ".locks", "ghost"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestWritePreconditions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)

	created := create(t, s, "demo")

	t.Run("create twice conflicts", func(t *testing.T) {
		_, err := s.WriteFile(ctx, "demo", "SKILL.md", []byte(good), skill.Precondition{MustNotExist: true})
		var conflict *skill.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, created.Hash, conflict.Current)
	})
	t.Run("a write needs a precondition", func(t *testing.T) {
		_, err := s.WriteFile(ctx, "demo", "references/a.md", []byte("x"), skill.Precondition{})
		require.ErrorIs(t, err, skill.ErrPreconditionRequired)
	})
	t.Run("a stale hash conflicts and changes nothing", func(t *testing.T) {
		_, err := s.WriteFile(ctx, "demo", "references/a.md", []byte("x"), skill.Precondition{IfMatch: strings.Repeat("0", 64)})
		var conflict *skill.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, created.Hash, conflict.Current)
		_, statErr := os.Stat(filepath.Join(s.Dir("demo"), "references", "a.md"))
		require.True(t, os.IsNotExist(statErr))
	})
	t.Run("the chain of hashes", func(t *testing.T) {
		a, err := s.WriteFile(ctx, "demo", "references/a.md", []byte("a"), skill.Precondition{IfMatch: created.Hash})
		require.NoError(t, err)
		require.NotEqual(t, created.Hash, a.Hash)
		b, err := s.WriteFile(ctx, "demo", "assets/b.bin", []byte{0, 1, 2}, skill.Precondition{IfMatch: a.Hash})
		require.NoError(t, err)
		// The old hash no longer works.
		_, err = s.WriteFile(ctx, "demo", "references/c.md", []byte("c"), skill.Precondition{IfMatch: a.Hash})
		var conflict *skill.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, b.Hash, conflict.Current)
	})
	t.Run("a bad SKILL.md is refused and nothing is written", func(t *testing.T) {
		cur, err := s.Read("demo")
		require.NoError(t, err)
		before, _ := os.ReadFile(filepath.Join(s.Dir("demo"), "SKILL.md"))
		_, err = s.WriteFile(ctx, "demo", "SKILL.md", []byte("no front matter"), skill.Precondition{IfMatch: cur.Hash})
		var invalid *skill.InvalidError
		require.ErrorAs(t, err, &invalid)
		after, _ := os.ReadFile(filepath.Join(s.Dir("demo"), "SKILL.md"))
		require.Equal(t, string(before), string(after))
	})
	t.Run("a path outside the layout is refused", func(t *testing.T) {
		cur, _ := s.Read("demo")
		for _, p := range []string{"../evil.md", "README.md", "references/../../evil", "/abs"} {
			_, err := s.WriteFile(ctx, "demo", p, []byte("x"), skill.Precondition{IfMatch: cur.Hash})
			var invalid *skill.InvalidError
			require.ErrorAs(t, err, &invalid, p)
		}
		_, err := s.WriteFile(ctx, "demo", "references/bin.md", []byte{0xff, 0xfe}, skill.Precondition{IfMatch: cur.Hash})
		require.Error(t, err)
	})
	t.Run("a missing skill", func(t *testing.T) {
		_, err := s.WriteFile(ctx, "missing", "references/a.md", []byte("x"), skill.Precondition{IfMatch: strings.Repeat("0", 64)})
		require.ErrorIs(t, err, skill.ErrNotFound)
	})
	t.Run("delete the skill", func(t *testing.T) {
		cur, _ := s.Read("demo")
		err := s.DeleteSkill(ctx, "demo", skill.Precondition{IfMatch: strings.Repeat("0", 64)})
		var conflict *skill.ConflictError
		require.ErrorAs(t, err, &conflict)
		require.NoError(t, s.DeleteSkill(ctx, "demo", skill.Precondition{IfMatch: cur.Hash}))
		_, err = s.Read("demo")
		require.ErrorIs(t, err, skill.ErrNotFound)
	})
}

// A write or a delete never goes through a symbolic link: not the folder of the skill,
// not a folder below it, not the file itself. The files outside the skill stay as they
// are, whatever hash the client sends (Spec 096, "Source of truth").
func TestWriteAndDeleteRefuseSymbolicLinks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// outside is a folder that a link points at. It holds a file that must never change.
	newOutside := func(t *testing.T) string {
		outside := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(outside, "victim"), []byte("original"), 0o644))
		return outside
	}
	untouched := func(t *testing.T, outside string) {
		data, err := os.ReadFile(filepath.Join(outside, "victim"))
		require.NoError(t, err)
		require.Equal(t, "original", string(data), "the file outside the skill changed")
		entries, err := os.ReadDir(outside)
		require.NoError(t, err)
		require.Len(t, entries, 1, "a file was created outside the skill")
	}
	requireRule := func(t *testing.T, err error, rule string) {
		var invalid *skill.InvalidError
		require.ErrorAs(t, err, &invalid)
		require.Equal(t, rule, invalid.Issues[0].Rule)
	}

	t.Run("symlinked skill folder", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		outside := newOutside(t)
		require.NoError(t, os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(good), 0o644))
		require.NoError(t, os.MkdirAll(skill.Root(s), 0o755))
		require.NoError(t, os.Symlink(outside, s.Dir("demo")))

		cur, err := s.Read("demo")
		require.NoError(t, err)
		require.Contains(t, ruleSet(cur.Issues), skill.RuleFolderLink)
		real, err := skill.Read(outside)
		require.NoError(t, err)
		require.NotEqual(t, real.Hash, cur.Hash)
		require.NotEqual(t, skill.HashFiles(nil), cur.Hash, "the hash is not the hash of an empty skill")

		_, err = s.WriteFile(ctx, "demo", "references/a.md", []byte("x"), skill.Precondition{IfMatch: cur.Hash})
		requireRule(t, err, skill.RuleFolderLink)
		_, err = s.WriteFile(ctx, "demo", "SKILL.md", []byte(good), skill.Precondition{IfMatch: cur.Hash})
		requireRule(t, err, skill.RuleFolderLink)
		entries, err := os.ReadDir(outside)
		require.NoError(t, err)
		require.Len(t, entries, 2, "the target of the link holds SKILL.md and victim only")

		// The owner can still remove the link. The target stays.
		require.NoError(t, s.DeleteSkill(ctx, "demo", skill.Precondition{IfMatch: cur.Hash}))
		_, err = os.Lstat(s.Dir("demo"))
		require.True(t, os.IsNotExist(err))
		_, err = os.Stat(filepath.Join(outside, "victim"))
		require.NoError(t, err)
	})

	t.Run("symlinked folder below the skill", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		outside := newOutside(t)
		created := create(t, s, "demo")
		require.NoError(t, os.Symlink(outside, filepath.Join(s.Dir("demo"), "references")))
		cur, err := s.Read("demo")
		require.NoError(t, err)
		require.NotEqual(t, created.Hash, cur.Hash)

		_, err = s.WriteFile(ctx, "demo", "references/a.md", []byte("x"), skill.Precondition{IfMatch: cur.Hash})
		requireRule(t, err, skill.RuleSymlink)
		untouched(t, outside)

		// A nested link: the link is the second part of the path.
		require.NoError(t, os.Remove(filepath.Join(s.Dir("demo"), "references")))
		require.NoError(t, os.Mkdir(filepath.Join(s.Dir("demo"), "references"), 0o755))
		require.NoError(t, os.Symlink(outside, filepath.Join(s.Dir("demo"), "references", "deep")))
		cur, err = s.Read("demo")
		require.NoError(t, err)
		_, err = s.WriteFile(ctx, "demo", "references/deep/x.md", []byte("x"), skill.Precondition{IfMatch: cur.Hash})
		requireRule(t, err, skill.RuleSymlink)
		untouched(t, outside)
	})

	t.Run("symlinked file", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		outside := newOutside(t)
		create(t, s, "demo")
		refs := filepath.Join(s.Dir("demo"), "references")
		require.NoError(t, os.Mkdir(refs, 0o755))
		require.NoError(t, os.Symlink(filepath.Join(outside, "victim"), filepath.Join(refs, "link.md")))
		// A link that points at a file that does not exist yet.
		require.NoError(t, os.Symlink(filepath.Join(outside, "new"), filepath.Join(refs, "dangling.md")))
		cur, err := s.Read("demo")
		require.NoError(t, err)

		for _, rel := range []string{"references/link.md", "references/dangling.md"} {
			_, err = s.WriteFile(ctx, "demo", rel, []byte("changed"), skill.Precondition{IfMatch: cur.Hash})
			requireRule(t, err, skill.RuleSymlink)
		}
		untouched(t, outside)

		// SKILL.md as a link.
		require.NoError(t, os.Remove(filepath.Join(s.Dir("demo"), "SKILL.md")))
		require.NoError(t, os.Symlink(filepath.Join(outside, "victim"), filepath.Join(s.Dir("demo"), "SKILL.md")))
		cur, err = s.Read("demo")
		require.NoError(t, err)
		_, err = s.WriteFile(ctx, "demo", "SKILL.md", []byte(good), skill.Precondition{IfMatch: cur.Hash})
		requireRule(t, err, skill.RuleSymlink)
		untouched(t, outside)
	})
}

// A lock that fails for another reason than a writer that holds it is an
// error, not a conflict: a conflict tells the client to read and retry.
func TestLockFailureIsNotAConflict(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions that bind the user")
	}
	t.Parallel()
	s := newStore(t)
	created := create(t, s, "demo")
	locks := filepath.Join(skill.Root(s), ".locks", "demo")
	require.NoError(t, os.Chmod(locks, 0o500))
	t.Cleanup(func() { _ = os.Chmod(locks, 0o755) })

	_, err := s.WriteFile(context.Background(), "demo", "SKILL.md", []byte(good), skill.Precondition{IfMatch: created.Hash})
	require.Error(t, err)
	var conflict *skill.ConflictError
	require.False(t, errors.As(err, &conflict), "%v", err)
}

// loseLock hands the lock of the skill name to another owner, as the takeover
// of a stale lock by another instance would.
func loseLock(t *testing.T, s *skill.Store, name string) {
	t.Helper()
	dir := filepath.Join(skill.Root(s), ".locks", name)
	require.NoError(t, os.RemoveAll(filepath.Join(dir, dirlock.LockDirectoryName)))
	other := dirlock.New(dir, nil)
	require.NoError(t, other.TryLock())
	t.Cleanup(func() { _ = other.Unlock() })
}

// A writer that lost its lock commits nothing: the new holder may have passed
// the same precondition.
func TestLostLockCommitsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	setup := func(t *testing.T) (*skill.Store, string) {
		t.Helper()
		s := newStore(t)
		created := create(t, s, "demo")
		cur, err := s.WriteFile(ctx, "demo", "references/a.md", []byte("one"), skill.Precondition{IfMatch: created.Hash})
		require.NoError(t, err)
		skill.SetBeforeHeldCheck(s, func() { loseLock(t, s, "demo") })
		return s, cur.Hash
	}
	unchanged := func(t *testing.T, s *skill.Store, hash string) {
		t.Helper()
		got, err := s.Read("demo")
		require.NoError(t, err)
		require.Equal(t, hash, got.Hash)
		entries, err := os.ReadDir(filepath.Join(s.Dir("demo"), "references"))
		require.NoError(t, err)
		require.Len(t, entries, 1, "a temporary file stayed")
	}

	t.Run("write", func(t *testing.T) {
		t.Parallel()
		s, hash := setup(t)
		_, err := s.WriteFile(ctx, "demo", "references/a.md", []byte("two"), skill.Precondition{IfMatch: hash})
		require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
		unchanged(t, s, hash)
	})
	t.Run("delete skill", func(t *testing.T) {
		t.Parallel()
		s, hash := setup(t)
		err := s.DeleteSkill(ctx, "demo", skill.Precondition{IfMatch: hash})
		require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
		unchanged(t, s, hash)
	})
}

// Two writers with the same hash: one wins and the other one gets a conflict.
// Two stores on one directory stand for two instances.
func TestTwoWritersRace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "skills")
	a, b := skill.NewStore(dir), skill.NewStore(dir)
	created := create(t, a, "demo")

	for round := range 20 {
		cur, err := a.Read("demo")
		require.NoError(t, err)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, st := range []*skill.Store{a, b} {
			wg.Go(func() {
				_, errs[i] = st.WriteFile(ctx, "demo", "references/r.md", []byte(strings.Repeat("x", i+1)+string(rune('a'+round))), skill.Precondition{IfMatch: cur.Hash})
			})
		}
		wg.Wait()
		wins := 0
		for _, err := range errs {
			if err == nil {
				wins++
				continue
			}
			var conflict *skill.ConflictError
			require.ErrorAs(t, err, &conflict)
		}
		require.Equal(t, 1, wins, "round %d: %v", round, errs)
	}
	_ = created
}

// A reader that meets a skill during a multi-file save sees a complete version.
func TestReaderNeverSeesAHalfWrittenFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	cur := create(t, s, "demo")
	big := strings.Repeat("0123456789abcdef\n", 4000)
	// The file exists before the reads start, so every complete version has it.
	cur, err := s.WriteFile(ctx, "demo", "references/big.md", []byte(big), skill.Precondition{IfMatch: cur.Hash})
	require.NoError(t, err)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writeErr error
	wg.Go(func() {
		hash := cur.Hash
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			next, err := s.WriteFile(ctx, "demo", "references/big.md", []byte(strings.ReplaceAll(big, "0", string(rune('a'+i%20)))), skill.Precondition{IfMatch: hash})
			if err != nil {
				writeErr = err
				return
			}
			hash = next.Hash
		}
	})
	changing := 0
	for range 200 {
		sk, err := s.Read("demo")
		require.NoError(t, err)
		if !sk.Valid() {
			// A read that meets a write in all its attempts says so and asks for a new try.
			// Any other issue shows a half-written file.
			if len(sk.Issues) == 1 && sk.Issues[0].Rule == skill.RuleChanging {
				changing++
				continue
			}
			t.Fatalf("a reader saw an invalid skill: %v", sk.Issues)
		}
		data, err := sk.Text("references/big.md")
		if errors.Is(err, skill.ErrFileChanged) {
			// A write replaced the file after the read.
			changing++
			continue
		}
		require.NoError(t, err, "a complete version has references/big.md")
		require.Len(t, data, len(big))
		// Every version is big with one letter for each zero, and big starts
		// with a zero: the first byte names the version that the read must hold.
		require.Equal(t, strings.ReplaceAll(big, "0", string(data[0])), string(data), "a reader saw a mix of two versions")
		require.Equal(t, skill.HashFiles(sk.Files), sk.Hash)
	}
	close(stop)
	wg.Wait()
	require.NoError(t, writeErr, "the writes stopped before the reads ended")
	require.Less(t, changing, 200, "no read got one version of the skill")
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
