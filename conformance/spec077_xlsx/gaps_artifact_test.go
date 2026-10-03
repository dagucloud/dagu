// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestXlsxArtifacts covers artifact: true on the writers: the saved file is
// copied under xlsx/<step>/ in the run's artifacts directory, its relative
// path is published as artifact, the copy is readable through
// context.paths.artifacts_dir, a dry run copies nothing, and a DAG whose
// artifacts are disabled fails the step.
func TestXlsxArtifacts(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_artifact.yaml").ExpectExitCode(0)
	dagu.ExpectTextFileContent("artifacts.txt", "xlsx/report/totals.xlsx\nxlsx/fill/filled.xlsx\nxlsx/export/totals.csv\n")

	var back []map[string]any
	readJSON(t, dagu, "back.json", &back)
	require.Len(t, back, 2, "the copy under the artifacts directory holds the rows written")
	require.Equal(t, "Acme", back[0]["customer"])
	require.Equal(t, float64(12.5), back[1]["total"])

	copies, err := filepath.Glob(dagu.ProjectPath(filepath.Join("artifacts", "*", "*", "*", "*", "xlsx", "*", "*")))
	require.NoError(t, err)
	var names []string
	for _, copy := range copies {
		rel, err := filepath.Rel(dagu.ProjectPath("artifacts"), copy)
		require.NoError(t, err)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		names = append(names, strings.Join(parts[4:], "/"))
	}
	require.ElementsMatch(t, []string{"xlsx/report/totals.xlsx", "xlsx/fill/filled.xlsx", "xlsx/export/totals.csv"}, names,
		"one copy per published artifact and none from the dry-run step")

	disabled := harness.NewRunner(t)
	result := disabled.Run("start", "artifact_disabled.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("artifact requires artifact storage")
	disabled.ExpectNoFile("totals.xlsx")
}

// TestXlsxWriteCellsForeach runs the spec's template-fill example: one
// write_cells per order into its own output file, each kept as an
// artifact.
func TestXlsxWriteCellsForeach(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "write_cells_foreach.yaml").ExpectExitCode(0)

	var filled struct {
		Summary struct {
			Total     int `json:"total"`
			Succeeded int `json:"succeeded"`
		} `json:"summary"`
		Outputs []struct {
			Artifact string `json:"artifact"`
		} `json:"outputs"`
	}
	readJSON(t, dagu, "filled.json", &filled)
	require.Equal(t, 2, filled.Summary.Total)
	require.Equal(t, 2, filled.Summary.Succeeded)
	require.Len(t, filled.Outputs, 2)
	require.Equal(t, "xlsx/fill/invoice-INV-1.xlsx", filled.Outputs[0].Artifact)
	require.Equal(t, "xlsx/fill/invoice-INV-2.xlsx", filled.Outputs[1].Artifact)

	for invoice, want := range map[string]struct {
		customer string
		amount   float64
	}{"INV-1": {"Acme", 100}, "INV-2": {"Beta", 250}} {
		read := dagu.Run("xlsx", "read", filepath.Join("out", "invoice-"+invoice+".xlsx"), "--header", "false", "--format", "json")
		read.ExpectExitCode(0)
		var result struct {
			Rows []map[string]any `json:"rows"`
		}
		require.NoError(t, json.Unmarshal([]byte(read.Stdout()), &result), read.Stdout())
		require.Len(t, result.Rows, 3, invoice)
		require.Equal(t, want.customer, result.Rows[0]["B"], invoice)
		require.Equal(t, want.amount, result.Rows[1]["B"], invoice)
		require.InDelta(t, want.amount*1.1, result.Rows[2]["B"], 0.001, "%s: the formula has a cached value", invoice)
	}
	template := dagu.Run("xlsx", "read", filepath.Join("templates", "invoice.xlsx"), "--header", "false", "--format", "json")
	template.ExpectExitCode(0)
	require.Contains(t, template.Stdout(), `"Customer"`)
	require.NotContains(t, template.Stdout(), "Acme", "the template is left as it was")
}
