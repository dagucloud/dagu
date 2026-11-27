// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/stretchr/testify/require"
)

// buildZip reads the skills and writes their ZIP.
func buildZip(t *testing.T, s *skill.Store, names ...string) []byte {
	t.Helper()
	skills, err := s.DownloadSkills(context.Background(), names)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, skill.WriteZip(context.Background(), &buf, skills))
	return buf.Bytes()
}

func openZip(t *testing.T, data []byte) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	return zr
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, data, 0o644))
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
	writeFile(t, filepath.Join(s.Dir("données"), "SKILL.md"), []byte("not a skill"))

	t.Run("content equals the files", func(t *testing.T) {
		zr := openZip(t, buildZip(t, s, "beta", "données", "alpha", "alpha"))
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
			require.True(t, f.Modified.Equal(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)), f.Name)
			require.Equal(t, os.FileMode(0o644), f.Mode(), f.Name)
			require.False(t, f.NonUTF8, f.Name)
			require.True(t, filepath.IsLocal(f.Name) && !strings.Contains(f.Name, `\`), f.Name)
			rc, err := f.Open()
			require.NoError(t, err)
			got, err := io.ReadAll(rc)
			require.NoError(t, err)
			_ = rc.Close()
			want, err := os.ReadFile(filepath.Join(skill.Root(s), filepath.FromSlash(f.Name)))
			require.NoError(t, err)
			require.Equal(t, want, got, f.Name)
		}
		require.Equal(t, []string{"alpha/SKILL.md", "alpha/references/r.md", "beta/SKILL.md", "beta/assets/img.bin", "données/SKILL.md"}, names)
	})
	t.Run("deterministic", func(t *testing.T) {
		first := buildZip(t, s, "alpha", "beta")
		require.NoError(t, os.Chtimes(filepath.Join(s.Dir("alpha"), "SKILL.md"), time.Unix(5, 0), time.Unix(5, 0)))
		require.NoError(t, os.Chmod(filepath.Join(s.Dir("beta"), "SKILL.md"), 0o600))
		require.Equal(t, first, buildZip(t, s, "beta", "alpha"))
	})
	t.Run("unknown name gives no result", func(t *testing.T) {
		_, err := s.DownloadSkills(context.Background(), []string{"alpha", "nope"})
		require.ErrorIs(t, err, skill.ErrNotFound)
		require.Contains(t, err.Error(), "nope")
	})
}

// A skill with errors can be downloaded, but a file that is hidden, a link,
// a hard link or above the size limit never enters the ZIP.
func TestZipLeavesOutWhatASkillMustNotHold(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	dir := s.Dir("broken")
	writeFile(t, filepath.Join(dir, "references", "x.md"), []byte("x"))
	writeFile(t, filepath.Join(dir, "assets", ".gitkeep"), nil)
	writeFile(t, filepath.Join(dir, "references", ".env"), []byte("hidden"))
	writeFile(t, filepath.Join(dir, ".env"), []byte("hidden"))
	writeFile(t, filepath.Join(dir, ".git", "config"), []byte("hidden"))
	writeFile(t, filepath.Join(dir, "assets", "big.bin"), bytes.Repeat([]byte{7}, skill.MaxFileBytes+1))
	outside := filepath.Join(t.TempDir(), "secret")
	writeFile(t, outside, []byte("secret"))
	require.NoError(t, os.Link(outside, filepath.Join(dir, "assets", "hard.bin")))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "assets", "soft.bin")))

	zr := openZip(t, buildZip(t, s, "broken"))
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	require.Equal(t, []string{"broken/assets/.gitkeep", "broken/references/x.md"}, names)
}

// The limits apply before the first byte, so a request that breaks one gets
// no ZIP.
func TestZipLimits(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	create(t, s, "alpha")
	refused := func(names ...string) {
		t.Helper()
		_, err := s.DownloadSkills(context.Background(), names)
		require.ErrorIs(t, err, skill.ErrDownloadRefused, "%v", names)
	}

	many := make([]string, skill.MaxDownloadSkills+1)
	for i := range many {
		many[i] = fmt.Sprintf("s%03d", i)
	}
	refused(many...)
	refused("alpha", "Alpha")
	refused("c:")

	// Sparse files give the size without the disk space.
	for i := range skill.MaxDownloadBytes/skill.MaxFileBytes + 1 {
		path := filepath.Join(s.Dir("huge"), "assets", fmt.Sprintf("%03d.bin", i))
		writeFile(t, path, nil)
		require.NoError(t, os.Truncate(path, skill.MaxFileBytes))
	}
	refused("huge")
	refused("alpha", "huge")
}

// A file that changes after the check stops the ZIP before any byte of it, and
// the ZIP has no end, so a client cannot take it for a whole ZIP. The file is
// random and above the 4 KiB buffer of the ZIP writer, so a write of its
// header or first bytes before the check would reach the output.
func TestZipStopsAtAChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newStore(t)
	outside := t.TempDir()
	const rel = "assets/data.bin"
	data := make([]byte, 1<<20)
	_, err := rand.Read(data)
	require.NoError(t, err)
	for name, change := range map[string]func(dir string){
		"edited": func(dir string) {
			edited := bytes.Clone(data)
			edited[len(edited)-1]++
			writeFile(t, filepath.Join(dir, rel), edited)
		},
		"grown": func(dir string) {
			writeFile(t, filepath.Join(dir, rel), append(bytes.Clone(data), 0))
		},
		"replaced-by-a-link": func(dir string) {
			copied := filepath.Join(outside, "data.bin")
			writeFile(t, copied, data)
			require.NoError(t, os.Remove(filepath.Join(dir, rel)))
			require.NoError(t, os.Symlink(copied, filepath.Join(dir, rel)))
		},
		"folder-replaced-by-a-link": func(dir string) {
			copied := filepath.Join(outside, "copy")
			writeFile(t, filepath.Join(copied, "SKILL.md"), []byte("x"))
			writeFile(t, filepath.Join(copied, rel), data)
			require.NoError(t, os.Rename(dir, filepath.Join(outside, "gone")))
			require.NoError(t, os.Symlink(copied, dir))
		},
		"deleted": func(dir string) {
			require.NoError(t, os.RemoveAll(filepath.Join(dir, "assets")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			sk := create(t, s, name)
			_, err := s.WriteFile(ctx, name, rel, data, skill.Precondition{IfMatch: sk.Hash})
			require.NoError(t, err)
			skills, err := s.DownloadSkills(context.Background(), []string{name})
			require.NoError(t, err)
			change(s.Dir(name))

			var out bytes.Buffer
			require.Error(t, skill.WriteZip(ctx, &out, skills))
			require.False(t, bytes.Contains(out.Bytes(), []byte(name+"/"+rel)), "no byte of the changed file")
			_, err = zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			require.Error(t, err)
		})
	}
}

func TestZipStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	create(t, s, "alpha")
	skills, err := s.DownloadSkills(context.Background(), []string{"alpha"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	require.ErrorIs(t, skill.WriteZip(ctx, &out, skills), context.Canceled)
	require.Zero(t, out.Len())
	_, err = s.DownloadSkills(ctx, []string{"alpha"})
	require.ErrorIs(t, err, context.Canceled)
}
