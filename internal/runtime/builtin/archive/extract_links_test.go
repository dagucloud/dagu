// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// archiveEntry is one entry of a test archive. A non-empty link makes the
// entry a symbolic link to that target.
type archiveEntry struct {
	name string
	link string
	body string
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
			hdr := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
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
			hdr.SetMode(0o644)
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
