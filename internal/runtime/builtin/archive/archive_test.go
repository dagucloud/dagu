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
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/mholt/archives"
)

func TestNewExecutorRequiresCommandMessage(t *testing.T) {
	t.Parallel()

	_, err := newExecutor(context.Background(), core.Step{
		ExecutorConfig: core.ExecutorConfig{Type: executorType},
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

	step := core.Step{
		Name:     "extract-zip",
		Commands: []core.CommandEntry{{Command: opExtract}},
		ExecutorConfig: core.ExecutorConfig{
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

	createStep := core.Step{
		Name:     "create-tar",
		Commands: []core.CommandEntry{{Command: opCreate}},
		ExecutorConfig: core.ExecutorConfig{
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

	listStep := core.Step{
		Name:     "list-archive",
		Commands: []core.CommandEntry{{Command: opList}},
		ExecutorConfig: core.ExecutorConfig{
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
