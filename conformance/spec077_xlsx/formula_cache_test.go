// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestXlsxWritersInvalidateFormulaCaches(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	for _, name := range []string{"cells", "update", "append"} {
		f := excelize.NewFile()
		require.NoError(t, f.SetSheetRow("Sheet1", "A1", &[]any{"amount", "total", "id"}))
		require.NoError(t, f.SetSheetRow("Sheet1", "A2", &[]any{2, 5, "one"}))
		require.NoError(t, f.SetSheetRow("Sheet1", "A3", &[]any{3, nil, "two"}))
		// SetCellFormula keeps the pre-existing value as its cache.
		require.NoError(t, f.SetCellFormula("Sheet1", "B2", "SUM(A2:A4)"))
		_, err := f.NewSheet("Summary")
		require.NoError(t, err)
		require.NoError(t, f.SetSheetRow("Summary", "A1", &[]any{"doubled", "kept"}))
		require.NoError(t, f.SetSheetRow("Summary", "A2", &[]any{10, 7}))
		require.NoError(t, f.SetCellFormula("Summary", "A2", "Sheet1!B2*2"))
		// Reads nothing the edits change, and cannot be evaluated: only its
		// cache gives a value.
		require.NoError(t, f.SetCellFormula("Summary", "B2", "UNSUPPORTED(1)"))
		require.NoError(t, f.SaveAs(dagu.ProjectPath(name+".xlsx")))
		require.NoError(t, f.Close())
	}
	dagu.Run("start", "formula_cache.yaml").ExpectExitCode(0)
	var out map[string]readOut
	readJSON(t, dagu, "out.json", &out)
	for _, name := range []string{"cells", "update", "append"} {
		require.NotEmpty(t, out[name].Rows, name)
		require.Equal(t, float64(23), out[name].Rows[0]["total"], name)
		require.Contains(t, out[name].Warnings, "Sheet1!B2: formula had no cached value; evaluated")
		require.Len(t, out[name+"_summary"].Rows, 1, name)
		require.Equal(t, float64(46), out[name+"_summary"].Rows[0]["doubled"], name)
		require.Equal(t, "7", fmt.Sprint(out[name+"_summary"].Rows[0]["kept"]), "%s: an unrelated formula keeps its cache", name)
	}
}
