// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// archiveEntry is one entry of a test archive. A non-empty link makes the
// entry a symbolic link to that target. A file entry has mode 0o644 unless
// mode is set.
type archiveEntry struct {
	name string
	link string
	body string
	mode fs.FileMode
}

var testArchiveFormats = []string{"tar", "tar.gz", "zip"}

// writeTestArchive writes entries in order to path in the given format.
func writeTestArchive(t *testing.T, path, format string, entries []archiveEntry) {
	t.Helper()
	var buf bytes.Buffer
	switch format {
	case "tar", "tar.gz":
		var w io.Writer = &buf
		var gz *gzip.Writer
		if format == "tar.gz" {
			gz = gzip.NewWriter(&buf)
			w = gz
		}
		tw := tar.NewWriter(w)
		for _, e := range entries {
			hdr := &tar.Header{Name: e.name, Mode: tarMode(e.mode), Typeflag: tar.TypeReg, Size: int64(len(e.body))}
			if e.link != "" {
				hdr = &tar.Header{Name: e.name, Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: e.link}
			}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("write header %s: %v", e.name, err)
			}
			if e.link == "" {
				if _, err := tw.Write([]byte(e.body)); err != nil {
					t.Fatalf("write %s: %v", e.name, err)
				}
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatalf("close tar: %v", err)
		}
		if gz != nil {
			if err := gz.Close(); err != nil {
				t.Fatalf("close gzip: %v", err)
			}
		}
	case "zip":
		zw := zip.NewWriter(&buf)
		for _, e := range entries {
			hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
			body := e.body
			hdr.SetMode(cmp.Or(e.mode, 0o644))
			if e.link != "" {
				hdr.SetMode(os.ModeSymlink | 0o777)
				body = e.link
			}
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				t.Fatalf("create %s: %v", e.name, err)
			}
			if _, err := w.Write([]byte(body)); err != nil {
				t.Fatalf("write %s: %v", e.name, err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("close zip: %v", err)
		}
	default:
		t.Fatalf("unknown format %s", format)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

// tarMode returns mode as the Unix mode bits of a tar header, 0o644 when zero.
func tarMode(mode fs.FileMode) int64 {
	if mode == 0 {
		return 0o644
	}
	bits := int64(mode.Perm())
	if mode&fs.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if mode&fs.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}

// extractTestArchive extracts source into destination with default options.
func extractTestArchive(t *testing.T, source, destination string) error {
	t.Helper()
	exec, err := newExecutor(context.Background(), ir.Step{
		Name:     "extract",
		Commands: []ir.CommandEntry{{Command: opExtract}},
		ExecutorConfig: ir.ExecutorConfig{
			Type:   executorType,
			Config: map[string]any{"source": source, "destination": destination},
		},
	})
	if err != nil {
		t.Fatalf("newExecutor: %v", err)
	}
	exec.SetStdout(io.Discard)
	return exec.Run(context.Background())
}

func requireSymlinks(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}
}

// An entry may name a path through a link that an earlier entry created. Each
// link below points inside the destination when read from the directory its
// name suggests, but not from the directory it really lands in, so following
// the chain would write outside the destination.
func TestExtractRejectsLinkChainsOutOfDestination(t *testing.T) {
	requireSymlinks(t)
	chains := map[string][]archiveEntry{
		"parent link through a link to the destination": {
			{name: "a", link: "."},
			{name: "a/b", link: ".."},
			{name: "a/b/escaped.txt", body: "outside"},
		},
		"nested link through a link to the destination": {
			{name: "a", link: "."},
			{name: "a/a/a/z", link: "../../.."},
			{name: "a/a/a/z/escaped.txt", body: "outside"},
		},
	}
	for name, entries := range chains {
		for _, format := range testArchiveFormats {
			t.Run(name+"/"+format, func(t *testing.T) {
				tempDir := t.TempDir()
				// The destination is three levels down, so every chain above
				// would land inside tempDir.
				dest := filepath.Join(tempDir, "x", "y", "out")
				source := filepath.Join(tempDir, "evil."+format)
				writeTestArchive(t, source, format, entries)

				err := extractTestArchive(t, source, dest)

				if !errors.Is(err, ErrCorrupted) {
					t.Fatalf("expected ErrCorrupted, got %v", err)
				}
				for _, dir := range []string{tempDir, filepath.Join(tempDir, "x"), filepath.Join(tempDir, "x", "y")} {
					if _, err := os.Lstat(filepath.Join(dir, "escaped.txt")); err == nil {
						t.Fatalf("a file was written outside the destination in %s", dir)
					}
				}
			})
		}
	}
}

// Links that stay inside the destination keep working, including a file
// written through a link to a directory of the archive.
func TestExtractKeepsLinksInsideDestination(t *testing.T) {
	requireSymlinks(t)
	entries := []archiveEntry{
		{name: "lib/tool.sh", body: "echo tool"},
		{name: "bin/tool", link: "../lib/tool.sh"},
		{name: "current", link: "lib"},
		{name: "current/extra.txt", body: "extra"},
	}
	for _, format := range testArchiveFormats {
		t.Run(format, func(t *testing.T) {
			tempDir := t.TempDir()
			dest := filepath.Join(tempDir, "out")
			source := filepath.Join(tempDir, "ok."+format)
			writeTestArchive(t, source, format, entries)

			if err := extractTestArchive(t, source, dest); err != nil {
				t.Fatalf("extract: %v", err)
			}
			target, err := os.Readlink(filepath.Join(dest, "bin", "tool"))
			if err != nil || target != filepath.Join("..", "lib", "tool.sh") {
				t.Fatalf("bin/tool links to %q (%v), want ../lib/tool.sh", target, err)
			}
			got, err := os.ReadFile(filepath.Join(dest, "bin", "tool"))
			if err != nil || string(got) != "echo tool" {
				t.Fatalf("read through bin/tool: %q (%v)", got, err)
			}
			got, err = os.ReadFile(filepath.Join(dest, "lib", "extra.txt"))
			if err != nil || string(got) != "extra" {
				t.Fatalf("read lib/extra.txt: %q (%v)", got, err)
			}
		})
	}
}

// A link the destination already holds may point outside it. Neither a file
// written through that link nor a link entry whose target passes through it
// may reach outside.
func TestExtractRefusesExistingLinksOut(t *testing.T) {
	requireSymlinks(t)
	cases := map[string]struct {
		entry archiveEntry
		want  error
	}{
		"file through the link": {archiveEntry{name: "ext/escaped.txt", body: "outside"}, ErrDestination},
		"link through the link": {archiveEntry{name: "escaped.txt", link: "ext/escaped.txt"}, ErrCorrupted},
	}
	for name, tc := range cases {
		for _, format := range testArchiveFormats {
			t.Run(name+"/"+format, func(t *testing.T) {
				tempDir := t.TempDir()
				dest := filepath.Join(tempDir, "out")
				outside := filepath.Join(tempDir, "outside")
				for _, dir := range []string{dest, outside} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatalf("mkdir %s: %v", dir, err)
					}
				}
				if err := os.Symlink(filepath.Join("..", "outside"), filepath.Join(dest, "ext")); err != nil {
					t.Fatalf("symlink: %v", err)
				}
				source := filepath.Join(tempDir, "evil."+format)
				writeTestArchive(t, source, format, []archiveEntry{tc.entry})

				err := extractTestArchive(t, source, dest)

				if !errors.Is(err, tc.want) {
					t.Fatalf("expected %v, got %v", tc.want, err)
				}
				for _, path := range []string{filepath.Join(outside, "escaped.txt"), filepath.Join(dest, "escaped.txt")} {
					if _, err := os.Lstat(path); err == nil {
						t.Fatalf("%s was created", path)
					}
				}
			})
		}
	}
}

// On Windows, a link target with a volume name but no leading separator, such
// as "C:foo", is not absolute to filepath.IsAbs, yet resolves from the working
// directory of that volume, outside the destination.
func TestExtractRejectsVolumeRelativeLinkTarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("volume names exist only on Windows")
	}
	for _, format := range testArchiveFormats {
		t.Run(format, func(t *testing.T) {
			tempDir := t.TempDir()
			dest := filepath.Join(tempDir, "out")
			source := filepath.Join(tempDir, "volume."+format)
			writeTestArchive(t, source, format, []archiveEntry{{name: "escaped", link: "C:foo"}})

			err := extractTestArchive(t, source, dest)

			if !errors.Is(err, ErrCorrupted) {
				t.Fatalf("expected ErrCorrupted, got %v", err)
			}
			if _, err := os.Lstat(filepath.Join(dest, "escaped")); err == nil {
				t.Fatal("a link was created")
			}
		})
	}
}

// Without overwrite, an existing link counts as an existing path, even one
// that leads outside the destination, so a link entry of the same name does
// not replace it.
func TestExtractKeepsExistingLinkWithoutOverwrite(t *testing.T) {
	requireSymlinks(t)
	want := filepath.Join("..", "outside")
	for _, format := range testArchiveFormats {
		t.Run(format, func(t *testing.T) {
			tempDir := t.TempDir()
			dest := filepath.Join(tempDir, "out")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.Symlink(want, filepath.Join(dest, "ext")); err != nil {
				t.Fatalf("symlink: %v", err)
			}
			source := filepath.Join(tempDir, "replace."+format)
			writeTestArchive(t, source, format, []archiveEntry{{name: "ext", link: "."}})

			err := extractTestArchive(t, source, dest)

			if !errors.Is(err, ErrDestination) {
				t.Fatalf("expected ErrDestination, got %v", err)
			}
			got, err := os.Readlink(filepath.Join(dest, "ext"))
			if err != nil || got != want {
				t.Fatalf("ext links to %q (%v), want %q", got, err, want)
			}
		})
	}
}

// A link is stored with its target in clean form, so an entry extracted
// later cannot redirect it. Kept as written, "b/../x" would resolve outside
// once "b" becomes a link to the destination.
func TestExtractLinksCannotBeRedirected(t *testing.T) {
	requireSymlinks(t)
	entries := []archiveEntry{
		{name: "x", body: "inside"},
		{name: "a", link: "b/../x"},
		{name: "b", link: "."},
	}
	for _, format := range testArchiveFormats {
		t.Run(format, func(t *testing.T) {
			tempDir := t.TempDir()
			dest := filepath.Join(tempDir, "out")
			if err := os.WriteFile(filepath.Join(tempDir, "x"), []byte("outside"), 0o600); err != nil {
				t.Fatalf("write outside file: %v", err)
			}
			source := filepath.Join(tempDir, "late."+format)
			writeTestArchive(t, source, format, entries)

			if err := extractTestArchive(t, source, dest); err != nil {
				t.Fatalf("extract: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dest, "a"))
			if err != nil || string(got) != "inside" {
				t.Fatalf("read through a: %q (%v), want inside", got, err)
			}
		})
	}
}
