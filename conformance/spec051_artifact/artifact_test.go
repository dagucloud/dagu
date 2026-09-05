// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec051_artifact holds black-box conformance tests for
// Spec 051: Artifact Actions.
package spec051_artifact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestArtifactWriteLive(t *testing.T) {
	t.Run("writes a file and reports its path and byte count", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_basic.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Operation string `json:"operation"`
			Path      string `json:"path"`
			Bytes     int64  `json:"bytes"`
			Created   bool   `json:"created"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.Equal(t, "write", out.Operation)
		require.Equal(t, "hello.txt", out.Path)
		require.Equal(t, int64(len("hello world")), out.Bytes)
		require.True(t, out.Created)
	})

	t.Run("without overwrite, writing to an existing path fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_existing_without_overwrite.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("file exists")
	})

	t.Run("overwrite: true replaces an existing artifact", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "write_overwrite_succeeds.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "second-longer", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a path escaping the artifact directory with .. is rejected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "path_escape_dotdot.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("artifact path must not contain ..")
	})

	t.Run("an absolute path is rejected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "path_escape_absolute.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("artifact path must be relative")
	})
}

func TestArtifactReadLive(t *testing.T) {
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
			Path      string `json:"path"`
			Exists    bool   `json:"exists"`
			Type      string `json:"type"`
			Size      int64  `json:"size"`
			Mode      string `json:"mode"`
			Content   string `json:"content"`
			Bytes     int64  `json:"bytes"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.Equal(t, "read", out.Operation)
		require.Equal(t, "data.txt", out.Path)
		require.True(t, out.Exists)
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

func TestArtifactListLive(t *testing.T) {
	t.Run("pattern and recursive filter to matching files across subdirectories", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "list_pattern_recursive.yaml")
		result.ExpectExitCode(0)

		var out struct {
			Files   int64 `json:"files"`
			Entries []struct {
				Path string `json:"path"`
				Type string `json:"type"`
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

// TestArtifactValidation proves the artifact executor's build-time
// validation: like data.convert/data.pick and outputs.write (Specs 049
// and 050), it registers a real step validator, so every one of these
// configuration errors is caught by dagu validate itself.
func TestArtifactValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		errStr string
	}{
		{
			name:   "artifact.write without with.content",
			file:   "write_missing_content.yaml",
			errStr: "content is required for write",
		},
		{
			name:   "artifact.read without with.path",
			file:   "read_missing_path.yaml",
			errStr: "path is required for read",
		},
		{
			name:   "with.overwrite true and with.atomic false together",
			file:   "overwrite_atomic_conflict.yaml",
			errStr: "overwrite requires atomic writes",
		},
		{
			name:   "artifacts.enabled: false conflicts with a step using an artifact action",
			file:   "artifacts_disabled_conflict.yaml",
			errStr: "artifact actions require artifacts.enabled to be true",
		},
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
