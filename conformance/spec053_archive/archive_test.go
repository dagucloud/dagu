// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec053_archive holds black-box conformance tests for
// Spec 053: Archive Actions.
package spec053_archive_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestArchiveLive(t *testing.T) {
	t.Run("creates a zip from a single file and reports files/bytes added", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("source.txt", "hello archive")

		result := dagu.Run("start", "roundtrip_file.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "hello archive", lastStepStdout(t, result.Stdout()))
	})

	t.Run("creates a tar.gz from a directory and preserves its structure on extract", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("srcdir/nested/deep.txt", "deep content")

		result := dagu.Run("start", "roundtrip_dir.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "deep content", lastStepStdout(t, result.Stdout()))
	})

	t.Run("with.format overrides the format the destination extension would suggest", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("a.txt", "x")

		result := dagu.Run("start", "explicit_format_override.yaml")
		result.ExpectExitCode(0)

		data, err := os.ReadFile(dagu.ProjectPath("out.dat")) // #nosec G304 -- fixed test path.
		require.NoError(t, err)
		require.True(t, bytes.HasPrefix(data, []byte("PK")), "expected out.dat to be a real zip file (PK magic bytes) despite its .dat extension")
	})

	t.Run("strip_components drops leading path components on extract", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("srcdir/nested/deep.txt", "deep")

		result := dagu.Run("start", "strip_components.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "deep", lastStepStdout(t, result.Stdout()))
	})

	t.Run("preserve_paths: false flattens every extracted file into the destination root", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("srcdir/nested/deep.txt", "deep")

		result := dagu.Run("start", "preserve_paths_false.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "deep", lastStepStdout(t, result.Stdout()))
	})

	t.Run("include filters which archived files are written on extract", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("srcdir/keep.txt", "a")
		dagu.WriteFile("srcdir/skip.log", "b")

		result := dagu.Run("start", "include_filter_extract.yaml")
		result.ExpectExitCode(0)

		var skipStat struct {
			Exists bool `json:"exists"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &skipStat))
		require.False(t, skipStat.Exists, "expected skip.log to have been filtered out of the extraction")
	})

	t.Run("without overwrite, extracting into an already-populated destination fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("a.txt", "a")

		result := dagu.Run("start", "overwrite_protection.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("exists (overwrite disabled)")
	})

	t.Run("create with dry_run reports counts without writing the archive file", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("a.txt", "hello")

		result := dagu.Run("start", "create_dry_run.yaml")
		result.ExpectExitCode(0)

		var out struct {
			FilesAdded    int64 `json:"filesAdded"`
			BytesArchived int64 `json:"bytesArchived"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.EqualValues(t, 1, out.FilesAdded)
		require.EqualValues(t, 5, out.BytesArchived)
	})

	t.Run("extract with dry_run reports counts without creating the destination directory", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("a.txt", "hello")

		result := dagu.Run("start", "extract_dry_run.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectNoFile("extracted")

		var out struct {
			FilesExtracted int64 `json:"filesExtracted"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.EqualValues(t, 1, out.FilesExtracted)
	})

	t.Run("include filters which entries archive.list reports", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("srcdir/keep.txt", "a")
		dagu.WriteFile("srcdir/skip.log", "b")

		result := dagu.Run("start", "list_include_filter.yaml")
		result.ExpectExitCode(0)

		var out struct {
			TotalFiles int `json:"totalFiles"`
			Files      []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		require.NoError(t, json.Unmarshal([]byte(lastStepStdout(t, result.Stdout())), &out))
		require.Equal(t, 1, out.TotalFiles)
		require.Equal(t, "srcdir/keep.txt", out.Files[0].Path)
	})

	t.Run("extracting a nonexistent archive fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "source_not_found_extract.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("source not found")
	})

	t.Run("extracting a file that is not a real archive fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("garbage.zip", "not an archive at all, just plain text")

		result := dagu.Run("start", "format_detection_failure.yaml")
		result.ExpectNonZeroExitCode()
	})

	// A hand-crafted zip (built with Go's own archive/zip, not
	// archive.create, since dagu's own creation path never produces an
	// entry name like this) with an entry named "../escape.txt" proves
	// extraction refuses to write outside with.destination, rather than
	// trusting the archive's own path.
	t.Run("an archive entry that would escape the destination directory is rejected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		dagu.WriteFile("evil.zip", evilZipBytes(t))

		result := dagu.Run("start", "zip_slip_protection.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("escapes destination")

		escapedPath := filepath.Join(filepath.Dir(dagu.ProjectPath(".")), "escape.txt")
		_, err := os.Stat(escapedPath) // #nosec G304 -- fixed test path derived from the isolated project root.
		require.Truef(t, os.IsNotExist(err), "expected no file at %s, the path the malicious entry tried to escape to", escapedPath)
	})
}

// TestArchiveValidation proves the archive executor's validation is split
// across two layers: with.source (and with.strip_components' minimum) are
// enforced by the registered JSON Schema, so dagu validate catches them;
// with.destination's requirement for create and with.password's
// restriction to extract/list are custom checks the registered step
// validator does not run, so they surface only at runtime.
func TestArchiveValidation(t *testing.T) {
	t.Parallel()

	t.Run("with.source missing fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "missing_source.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`missing properties: ["source"]`)
	})

	t.Run("a negative with.strip_components fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "negative_strip_components.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("minimum")
	})

	t.Run("with.destination missing for archive.create fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "missing_destination_for_create.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "missing_destination_for_create.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("destination is required for create")
	})

	t.Run("with.password on archive.create fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "password_with_create.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "password_with_create.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("password is only supported for extract/list operations")
	})
}
