// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec052_file holds black-box conformance tests for
// Spec 052: File Actions.
package spec052_file_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestFileWriteLive(t *testing.T) {
	t.Run("writes a file and reports its path and byte count", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_basic.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Operation string `json:"operation"`
			Bytes     int64  `json:"bytes"`
			Created   bool   `json:"created"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.Equal(t, "write", out.Operation)
		require.Equal(t, int64(len("hello world")), out.Bytes)
		require.True(t, out.Created)
		dagu.ExpectFileContent("hello.txt", "hello world")
	})

	t.Run("an absolute with.path is written directly, unlike artifact.write's relative-only paths", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		absPath := dagu.ProjectPath("abs.txt")
		result := dagu.RunWithEnv([]string{"ABS_PATH=" + absPath}, "start", "write_absolute_path.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("abs.txt", "absolute content")
	})

	t.Run("dry_run reports the intended write without creating the file", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_dry_run.yaml")
		result.ExpectExitCode(0)

		var out struct {
			DryRun  bool `json:"dryRun"`
			Created bool `json:"created"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.True(t, out.DryRun)
		require.False(t, out.Created)
		dagu.ExpectNoFile("dry.txt")
	})

	t.Run("without overwrite, writing to an existing path fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_existing_without_overwrite.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("file exists")
	})

	t.Run("overwrite: true replaces an existing file", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_overwrite.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "second-longer", lastStepStdout(t, result.Stdout()))
	})
}

func TestFileReadLive(t *testing.T) {
	t.Run("format raw (the default) streams the file's exact bytes", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "read_raw_default.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "hello", lastStepStdout(t, result.Stdout()))
	})

	t.Run("format json wraps the content with metadata", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "read_json_format.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Operation string `json:"operation"`
			Type      string `json:"type"`
			Size      int64  `json:"size"`
			Mode      string `json:"mode"`
			Content   string `json:"content"`
			Bytes     int64  `json:"bytes"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.Equal(t, "read", out.Operation)
		require.Equal(t, "file", out.Type)
		require.Equal(t, int64(5), out.Size)
		require.Equal(t, "hello", out.Content)
		require.Equal(t, int64(5), out.Bytes)
		require.True(t, strings.HasPrefix(out.Mode, "-rw-r-----"), "expected mode to reflect with.mode: 0640, got %q", out.Mode)
	})

	t.Run("max_bytes rejects a file larger than the limit", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "read_max_bytes_exceeded.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("exceeds max_bytes 5")
	})

	t.Run("reading a directory fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "read_directory_fails.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("cannot read a directory")
	})
}

func TestFileStatLive(t *testing.T) {
	t.Run("missing_ok reports a missing path as exists: false instead of failing", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "stat_missing_ok.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Exists bool `json:"exists"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.False(t, out.Exists)
	})

	t.Run("without missing_ok, a missing path fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "stat_without_missing_ok_fails.yaml")
		result.ExpectNonZeroExitCode()
	})
}

func TestFileCopyLive(t *testing.T) {
	t.Run("copying a directory without recursive fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "copy_dir_without_recursive_fails.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("recursive is required to copy a directory")
	})

	t.Run("recursive copies a directory tree, preserving content", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "copy_dir_recursive_succeeds.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "a", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a destination inside the source directory is rejected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "copy_destination_inside_source_fails.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("destination must not be inside source")
	})
}

func TestFileMoveLive(t *testing.T) {
	t.Run("moves a file, removing the source", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "move_basic.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Exists bool `json:"exists"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.False(t, out.Exists, "expected the source to no longer exist after the move")
	})

	t.Run("without overwrite, an existing destination fails the move", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "move_destination_exists_fails.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("destination exists")
	})
}

func TestFileDeleteLive(t *testing.T) {
	t.Run("deleting a directory without recursive fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "delete_dir_without_recursive_fails.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("recursive is required to delete a directory")
	})

	t.Run("recursive deletes a directory and its contents", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "delete_dir_recursive_succeeds.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Exists bool `json:"exists"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.False(t, out.Exists)
	})

	t.Run("missing_ok reports a missing path as deleted: false instead of failing", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "delete_missing_ok.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Deleted bool `json:"deleted"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.False(t, out.Deleted)
	})
}

func TestFileMkdirLive(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "mkdir_basic.yaml")
	result.ExpectExitCode(0)

	var out struct {
		Created bool `json:"created"`
	}
	require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
	require.True(t, out.Created)
}

func TestFileListLive(t *testing.T) {
	t.Run("pattern and recursive filter to matching files across subdirectories", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "list_pattern_recursive.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Files   int64 `json:"files"`
			Entries []struct {
				Path string `json:"path"`
			} `json:"entries"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.EqualValues(t, 2, out.Files)
		require.Len(t, out.Entries, 2)
		require.Equal(t, "a/one.txt", out.Entries[0].Path)
		require.Equal(t, "b/three.txt", out.Entries[1].Path)
	})

	t.Run("include_dirs surfaces directory entries alongside files", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "list_include_dirs.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Entries []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"entries"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))

		var sawDir bool
		for _, entry := range out.Entries {
			if entry.Path == "a" {
				require.Equal(t, "directory", entry.Type)
				sawDir = true
			}
		}
		require.True(t, sawDir, "expected the \"a\" directory to be listed")
	})

	t.Run("without recursive, only direct children are listed", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "list_non_recursive_default.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Entries []struct {
				Path string `json:"path"`
			} `json:"entries"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.Len(t, out.Entries, 1)
		require.Equal(t, "top.txt", out.Entries[0].Path)
	})
}

// TestFileValidation proves the file executor's build-time validation:
// like data.convert/data.pick, outputs.write, and artifact.write/read/list
// (Specs 049-051), it registers a real step validator, so every one of
// these configuration errors is caught by dagu validate itself.
func TestFileValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		errStr string
	}{
		{name: "file.write without with.content", file: "write_missing_content.yaml", errStr: "content is required for write"},
		{name: "file.read without with.path", file: "read_missing_path.yaml", errStr: "path is required for read"},
		{name: "file.stat without with.path", file: "stat_missing_path.yaml", errStr: "path is required for stat"},
		{name: "file.delete without with.path", file: "delete_missing_path.yaml", errStr: "path is required for delete"},
		{name: "file.mkdir without with.path", file: "mkdir_missing_path.yaml", errStr: "path is required for mkdir"},
		{name: "file.list without with.path", file: "list_missing_path.yaml", errStr: "path is required for list"},
		{name: "file.copy without with.destination", file: "copy_missing_destination.yaml", errStr: "destination is required for copy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.errStr)
		})
	}
}
