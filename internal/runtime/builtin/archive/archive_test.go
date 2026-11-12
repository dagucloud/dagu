// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/mholt/archives"
)

func TestNewExecutorRequiresCommandMessage(t *testing.T) {
	t.Parallel()

	_, err := newExecutor(context.Background(), ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Type: executorType},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), "field 'command': archive: configuration error: command must specify archive operation"; got != want {
		t.Fatalf("unexpected error:\nwant: %s\n got: %s", want, got)
	}
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected error to wrap ErrConfig, got %v", err)
	}
}

func TestRunExtractZip(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	sourceArchive := filepath.Join(tempDir, "sample.zip")
	makeZip(t, sourceArchive, map[string]string{
		"greetings/hello.txt": "hello world",
	})

	destDir := filepath.Join(tempDir, "out")

	step := ir.Step{
		Name:     "extract-zip",
		Commands: []ir.CommandEntry{{Command: opExtract}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
			Config: map[string]any{
				"source":      sourceArchive,
				"destination": destDir,
				"overwrite":   true,
			},
		},
	}

	exec, err := newExecutor(context.Background(), step)
	if err != nil {
		t.Fatalf("newExecutor: %v", err)
	}

	buf := &bytes.Buffer{}
	exec.SetStdout(buf)

	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var result extractionResult
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.FilesExtracted != 1 {
		t.Fatalf("expected 1 file extracted, got %d", result.FilesExtracted)
	}

	extracted, err := os.ReadFile(filepath.Join(destDir, "greetings", "hello.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(extracted) != "hello world" {
		t.Fatalf("unexpected file contents %q", extracted)
	}
}

func TestRunCreateTarGzAndList(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "data")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	destArchive := filepath.Join(tempDir, "bundle.tar.gz")

	createStep := ir.Step{
		Name:     "create-tar",
		Commands: []ir.CommandEntry{{Command: opCreate}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
			Config: map[string]any{
				"source":      sourceDir,
				"destination": destArchive,
				"format":      "tar.gz",
			},
		},
	}

	createExec, err := newExecutor(context.Background(), createStep)
	if err != nil {
		t.Fatalf("newExecutor(create): %v", err)
	}

	createOut := &bytes.Buffer{}
	createExec.SetStdout(createOut)

	if err := createExec.Run(context.Background()); err != nil {
		t.Fatalf("create Run: %v", err)
	}

	fs, err := archives.FileSystem(context.Background(), destArchive, nil)
	if err != nil {
		t.Fatalf("FileSystem: %v", err)
	}

	checkFile := func(path, expected string) {
		f, err := fs.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != expected {
			t.Fatalf("unexpected contents for %s: %q", path, data)
		}
	}

	checkFile("data/a.txt", "alpha")
	checkFile("data/b.txt", "beta")

	listStep := ir.Step{
		Name:     "list-archive",
		Commands: []ir.CommandEntry{{Command: opList}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: executorType,
			Config: map[string]any{
				"source": destArchive,
			},
		},
	}
	listExec, err := newExecutor(context.Background(), listStep)
	if err != nil {
		t.Fatalf("newExecutor(list): %v", err)
	}
	listBuf := &bytes.Buffer{}
	listExec.SetStdout(listBuf)

	if err := listExec.Run(context.Background()); err != nil {
		t.Fatalf("list Run: %v", err)
	}

	var listRes listResult
	if err := json.Unmarshal(listBuf.Bytes(), &listRes); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	if listRes.TotalFiles == 0 {
		t.Fatalf("expected list entries, got zero")
	}
}

func makeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, contents := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(contents)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

// Relative source and destination paths name files under the step working
// directory, not the Dagu process directory.
func TestArchiveRelativePathsUseWorkingDir(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "data", "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	run := func(op string, cfg map[string]any) {
		t.Helper()
		step := ir.Step{
			Name:           op,
			Commands:       []ir.CommandEntry{{Command: op}},
			ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
		}
		dag := &ir.DAG{Name: "archive-test", WorkingDir: workDir, WorkingDirExplicit: true}
		ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
		ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
		exec, err := newExecutor(ctx, step)
		if err != nil {
			t.Fatalf("newExecutor(%s): %v", op, err)
		}
		exec.SetStdout(io.Discard)
		if err := exec.Run(ctx); err != nil {
			t.Fatalf("%s Run: %v", op, err)
		}
	}

	run(opCreate, map[string]any{"source": "data", "destination": "out/data.tar.gz"})
	if _, err := os.Stat(filepath.Join(workDir, "out", "data.tar.gz")); err != nil {
		t.Fatalf("archive not created under the working directory: %v", err)
	}

	run(opExtract, map[string]any{"source": "out/data.tar.gz", "destination": "restored"})
	got, err := os.ReadFile(filepath.Join(workDir, "restored", "data", "a.txt"))
	if err != nil {
		t.Fatalf("extracted file not under the working directory: %v", err)
	}
	if string(got) != "alpha" {
		t.Fatalf("extracted content = %q, want %q", got, "alpha")
	}
}

// Archiving the working directory itself keeps its entries at the archive
// root, as with source "." before relative paths resolved.
func TestArchiveCreateDotSourceKeepsRootEntries(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	destArchive := filepath.Join(t.TempDir(), "dot.tar.gz")

	step := ir.Step{
		Name:     "create-dot",
		Commands: []ir.CommandEntry{{Command: opCreate}},
		ExecutorConfig: ir.ExecutorConfig{
			Type:   executorType,
			Config: map[string]any{"source": ".", "destination": destArchive},
		},
	}
	dag := &ir.DAG{Name: "archive-test", WorkingDir: workDir, WorkingDirExplicit: true}
	ctx := runtime.NewContext(context.Background(), dag, "run-1", "")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))
	exec, err := newExecutor(ctx, step)
	if err != nil {
		t.Fatalf("newExecutor: %v", err)
	}
	out := &bytes.Buffer{}
	exec.SetStdout(out)
	if err := exec.Run(ctx); err != nil {
		t.Fatalf("create Run: %v", err)
	}

	fsys, err := archives.FileSystem(context.Background(), destArchive, nil)
	if err != nil {
		t.Fatalf("FileSystem: %v", err)
	}
	if _, err := fs.Stat(fsys, "a.txt"); err != nil {
		t.Fatalf("a.txt not at the archive root: %v", err)
	}

	var result struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if result.Source != "." {
		t.Fatalf("output source = %q, want the path as written", result.Source)
	}
}
