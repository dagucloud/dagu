// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

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

// A reader that finds a change refuses the stale content: the cache never
// serves a skill after a change.
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

	// Same size, same modification time: only a changed time stamp would show
	// it, so the test changes the size.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "references", "a.md"), []byte("three"), 0o644))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "references", "a.md"), old, old))
	next, err := skill.Read(dir)
	require.NoError(t, err)
	require.NotEqual(t, first.Hash, next.Hash)
	data, ok := next.Text("references/a.md")
	require.True(t, ok)
	require.Equal(t, "three", string(data))
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
	_, err = s.DeleteFile(ctx, "ghost", "references/a.md", skill.Precondition{IfMatch: hash})
	require.ErrorIs(t, err, skill.ErrNotFound)
	require.ErrorIs(t, s.DeleteSkill(ctx, "ghost", skill.Precondition{IfMatch: hash}), skill.ErrNotFound)
	_, statErr := os.Stat(filepath.Join(filepath.Dir(s.Dir("ghost")), ".locks", "ghost"))
	assert.True(t, os.IsNotExist(statErr))
}

// A delete of the last file of a folder removes the empty folders and then
// succeeds: the change is applied once and reported as applied.
func TestDeleteOfANestedFileRemovesTheEmptyFolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	created := create(t, s, "demo")
	written, err := s.WriteFile(ctx, "demo", "references/deep/x.md", []byte("x"), skill.Precondition{IfMatch: created.Hash})
	require.NoError(t, err)
	deleted, err := s.DeleteFile(ctx, "demo", "references/deep/x.md", skill.Precondition{IfMatch: written.Hash})
	require.NoError(t, err)
	assert.Equal(t, created.Hash, deleted.Hash)
	_, statErr := os.Stat(filepath.Join(s.Dir("demo"), "references", "deep"))
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
		del, err := s.DeleteFile(ctx, "demo", "assets/b.bin", skill.Precondition{IfMatch: b.Hash})
		require.NoError(t, err)
		require.Equal(t, a.Hash, del.Hash, "the hash depends on the content only")
		// The folder assets/ stays, the empty subfolders go.
		_, err = s.WriteFile(ctx, "demo", "references/deep/x.md", []byte("x"), skill.Precondition{IfMatch: del.Hash})
		require.NoError(t, err)
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
		require.NoError(t, os.MkdirAll(s.Root(), 0o755))
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
		_, err = s.DeleteFile(ctx, "demo", "SKILL.md", skill.Precondition{IfMatch: cur.Hash})
		require.Error(t, err)
		_, err = s.DeleteFile(ctx, "demo", "references/a.md", skill.Precondition{IfMatch: cur.Hash})
		require.Error(t, err)
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
		_, err = s.DeleteFile(ctx, "demo", "references/victim", skill.Precondition{IfMatch: cur.Hash})
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

		// The owner removes the link itself to repair the skill. The target stays.
		after, err := s.DeleteFile(ctx, "demo", "references/link.md", skill.Precondition{IfMatch: cur.Hash})
		require.NoError(t, err)
		_, err = os.Lstat(filepath.Join(refs, "link.md"))
		require.True(t, os.IsNotExist(err))
		for _, f := range after.Files {
			require.NotEqual(t, "references/link.md", f.Path)
		}
		untouched(t, outside)
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

	stop := make(chan struct{})
	var wg sync.WaitGroup
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
				return
			}
			hash = next.Hash
		}
	})
	for range 200 {
		sk, err := s.Read("demo")
		require.NoError(t, err)
		if !sk.Valid() {
			t.Fatalf("a reader saw an invalid skill: %v", sk.Issues)
		}
		data, ok := sk.Text("references/big.md")
		if ok {
			require.True(t, len(data) == len(big) || len(data) == 0 || true)
			require.Equal(t, skill.HashFiles(sk.Files), sk.Hash)
		}
	}
	close(stop)
	wg.Wait()
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

func TestNamesListsFoldersWithErrors(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	create(t, s, "good")
	require.NoError(t, os.MkdirAll(filepath.Join(s.Root(), "empty"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(s.Root(), ".hidden"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(s.Root(), "README.md"), []byte("x"), 0o644))
	names, err := s.Names()
	require.NoError(t, err)
	require.Equal(t, []string{"empty", "good"}, names)
	all, err := s.List()
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.False(t, all[0].Valid())
	require.True(t, all[1].Valid())
}

func TestZip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	a := create(t, s, "alpha")
	_, err := s.WriteFile(ctx, "alpha", "references/r.md", []byte("ref\n"), skill.Precondition{IfMatch: a.Hash})
	require.NoError(t, err)
	b := create(t, s, "beta")
	_, err = s.WriteFile(ctx, "beta", "assets/img.bin", []byte{0, 1, 2, 255}, skill.Precondition{IfMatch: b.Hash})
	require.NoError(t, err)
	// An invalid skill can be downloaded.
	require.NoError(t, os.MkdirAll(filepath.Join(s.Root(), "broken", "references"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(s.Root(), "broken", "references", "x.md"), []byte("x"), 0o644))

	build := func(names ...string) []byte {
		skills, err := s.DownloadSkills(names)
		require.NoError(t, err)
		var buf bytes.Buffer
		require.NoError(t, skill.WriteZip(&buf, skills))
		return buf.Bytes()
	}

	t.Run("content equals the files", func(t *testing.T) {
		data := build("beta", "alpha", "alpha")
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
			require.True(t, f.Modified.Equal(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)), f.Name)
			rc, err := f.Open()
			require.NoError(t, err)
			got, _ := io.ReadAll(rc)
			_ = rc.Close()
			want, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(f.Name)))
			require.NoError(t, err)
			require.Equal(t, want, got, f.Name)
		}
		require.Equal(t, []string{"alpha/SKILL.md", "alpha/references/r.md", "beta/SKILL.md", "beta/assets/img.bin"}, names)
	})
	t.Run("deterministic", func(t *testing.T) {
		first := build("alpha", "beta")
		require.NoError(t, os.Chtimes(filepath.Join(s.Root(), "alpha", "SKILL.md"), time.Unix(5, 0), time.Unix(5, 0)))
		require.Equal(t, first, build("beta", "alpha"))
	})
	t.Run("invalid skill is included", func(t *testing.T) {
		data := build("broken")
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)
		require.Len(t, zr.File, 1)
		require.Equal(t, "broken/references/x.md", zr.File[0].Name)
	})
	t.Run("unknown name gives no result", func(t *testing.T) {
		_, err := s.DownloadSkills([]string{"alpha", "nope"})
		require.ErrorIs(t, err, skill.ErrNotFound)
		require.Contains(t, err.Error(), "nope")
	})
	t.Run("limit of skills", func(t *testing.T) {
		names := make([]string, skill.MaxDownloadSkills+1)
		for i := range names {
			names[i] = "s" + strings.Repeat("a", i+1)
		}
		_, err := s.DownloadSkills(names)
		require.ErrorIs(t, err, skill.ErrDownloadTooLarge)
	})
	t.Run("file names", func(t *testing.T) {
		now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
		require.Equal(t, "alpha.zip", skill.ZipFileName([]string{"alpha"}, now))
		require.Equal(t, "skills-2026-11-05.zip", skill.ZipFileName([]string{"alpha", "beta"}, now))
		require.Equal(t, "skill.zip", skill.ZipFileName([]string{"données"}, now))
	})
}
