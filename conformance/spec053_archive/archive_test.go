// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec053_archive_test covers archive action wiring through the CLI.
package spec053_archive_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.WriteFile("source.txt", "hello archive")
	dagu.Run("start", "roundtrip.yaml").ExpectExitCode(0)
	dagu.ExpectFileContent("extracted/source.txt", "hello archive")
	dagu.ExpectNoFile("preview.zip")

	data, err := os.ReadFile(dagu.ProjectPath("list.json"))
	require.NoError(t, err)
	var out struct {
		Operation  string `json:"operation"`
		TotalFiles int    `json:"totalFiles"`
		Files      []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(data, &out))
	require.Equal(t, "list", out.Operation)
	require.Equal(t, 1, out.TotalFiles)
	require.Len(t, out.Files, 1)
	require.Equal(t, "source.txt", out.Files[0].Path)
}

func TestArchiveValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ command, file, message string }{
		{"validate", "missing_source.yaml", "source"},
		{"validate", "negative_strip_components.yaml", "minimum"},
		{"validate", "unknown_field.yaml", `unexpected additional properties ["destinations"]; did you mean "destination" instead of "destinations"?`},
		{"start", "missing_destination_for_create.yaml", "destination is required for create"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run(tc.command, tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}

// Relative paths resolve under the step working directory, and extraction
// without a destination writes into it, not into the process directory.
func TestArchiveRelativeWorkingDir(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.WriteFile("work/data/a.txt", "alpha")
	dagu.Run("start", "relative_working_dir.yaml").ExpectExitCode(0)
	dagu.ExpectFileContent("work/data/a.txt", "alpha")
	_, err := os.Stat(dagu.ProjectPath("work/out/data.tar.gz"))
	require.NoError(t, err)
	dagu.ExpectNoFile("out")
	dagu.ExpectNoFile("data")
}
