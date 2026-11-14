// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePublishesProblems(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx",
		[]any{"Invoice No", "Amount", "Status"},
		[]any{"INV-1", 10, "Done"},
		[]any{"INV-1", "N/A", "Pending"},
		[]any{"INV-3", 30, ""},
	)
	te, err := newTestExecutor(t, dir, opValidate, map[string]any{
		"path":      "orders.xlsx",
		"required":  []any{"Invoice No", "Nope"},
		"not_blank": "Status",
		"unique":    "Invoice No",
		"types":     map[string]any{"Amount": "number"},
		"allowed":   map[string]any{"Status": []any{"Done", "Open"}},
	})
	require.NoError(t, err)
	require.NoError(t, te.exec.Run(context.Background()))

	outputs := te.exec.GetOutputs()
	assert.Equal(t, false, outputs["ok"])
	assert.Equal(t, 5, outputs["count"])
	assert.Equal(t, 3, outputs["rows"])
	assert.Equal(t, "Sheet1", outputs["sheet"])
	assert.Equal(t, "Sheet1!A1:C4", outputs["range"])
	assert.Equal(t, false, outputs["truncated"])
	problems, ok := outputs["problems"].([]workbook.Problem)
	require.True(t, ok)
	require.Len(t, problems, 5)
	assert.Equal(t, workbook.ProblemMissingColumn, problems[0].Code)
	assert.Equal(t, workbook.Problem{Code: workbook.ProblemType, Sheet: "Sheet1", Cell: "B3", Row: 3, Column: "Amount", Message: `expected number, found "N/A"`}, problems[1])
	assert.Equal(t, "Validated 3 rows in orders.xlsx Sheet1!A1:C4: 5 problems\n", te.stdout.String())
	assert.Contains(t, te.stderr.String(), "problem: Sheet1!B3: expected number, found \"N/A\"\n")
	assert.Contains(t, te.stderr.String(), `problem: Sheet1: column "Nope" not found; headers present: Invoice No, Amount, Status`)
	assert.Contains(t, te.stderr.String(), `problem: Sheet1!C3: value "Pending" is not one of Done, Open`)
}

func TestValidateOnProblemFailAndClean(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeBook(t, dir, "orders.xlsx",
		[]any{"Invoice No", "Amount"},
		[]any{"INV-1", 10},
		[]any{"INV-1", 20},
	)
	failing, err := newTestExecutor(t, dir, opValidate, map[string]any{"path": "orders.xlsx", "unique": []any{"Invoice No"}, "on_problem": "fail"})
	require.NoError(t, err)
	err = failing.exec.Run(context.Background())
	require.ErrorContains(t, err, "1 problem found in orders.xlsx Sheet1")
	assert.Equal(t, 1, failing.exec.ExitCode())
	assert.Nil(t, failing.exec.GetOutputs(), "a failed attempt publishes nothing")
	assert.Equal(t, "problem: Sheet1!A3: duplicate value \"INV-1\"; first at row 2\n", failing.stderr.String(), "the problems are still listed")
	assert.Empty(t, failing.stdout.String())

	clean, err := newTestExecutor(t, dir, opValidate, map[string]any{"path": "orders.xlsx", "required": []any{"Invoice No"}, "types": map[string]any{"Amount": "integer"}, "on_problem": "fail"})
	require.NoError(t, err)
	require.NoError(t, clean.exec.Run(context.Background()))
	outputs := clean.exec.GetOutputs()
	assert.Equal(t, true, outputs["ok"])
	assert.Equal(t, 0, outputs["count"])
	assert.Equal(t, []workbook.Problem{}, outputs["problems"])
	assert.Equal(t, "Validated 2 rows in orders.xlsx Sheet1!A1:B3: no problems\n", clean.stdout.String())
	assert.Empty(t, clean.stderr.String())
}

func TestValidateFitsProblemsToTheBudget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rows := [][]any{{"n"}}
	for range 40 {
		rows = append(rows, []any{"x"})
	}
	writeBook(t, dir, "many.xlsx", rows...)
	te, err := newTestExecutor(t, dir, opValidate, map[string]any{"path": "many.xlsx", "types": map[string]any{"n": "number"}, "max_problems": 30},
		func(dag *ir.DAG) { dag.MaxOutputSize = outputMargin + 1500 })
	require.NoError(t, err)
	require.NoError(t, te.exec.Run(context.Background()))
	outputs := te.exec.GetOutputs()
	assert.Equal(t, 40, outputs["count"], "every problem is counted")
	problems := outputs["problems"].([]workbook.Problem)
	assert.Less(t, len(problems), 30, "the budget cuts below max_problems")
	assert.NotEmpty(t, problems)
	assert.Equal(t, true, outputs["truncated"])
	assert.Contains(t, te.stderr.String(), "warning: problems truncated to")
	assert.Equal(t, 30, strings.Count(te.stderr.String(), "problem: "), "every kept problem is listed on stderr")
}
