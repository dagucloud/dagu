// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func validationBook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "Invoice No", "Amount", "Status", "Due")
	setRow(t, f, "Sheet1", "A2", "INV-1", 10, "Done", "2026-10-01")
	setRow(t, f, "Sheet1", "A3", "INV-2", "N/A", "Pending", "")
	setRow(t, f, "Sheet1", "A4", "INV-1", 30, "", "2026-10-03")
	setRow(t, f, "Sheet1", "A5", "", 40, "Open", "soon")
	return saveBook(t, f, "validate.xlsx")
}

func TestValidateFindsEveryKindOfProblem(t *testing.T) {
	t.Parallel()
	path := validationBook(t)
	result, err := Validate(context.Background(), path, ValidateOptions{
		Required: []string{"Invoice No", "Nope"},
		NotBlank: []string{"Invoice No", "Status", "Nope"}, // Nope is reported once
		Unique:   []string{"invoice no"},                   // loose match
		Types:    map[string]ColumnType{"Amount": TypeNumber, "Due": TypeDate},
		Allowed:  map[string][]any{"Status": {"Done", "Open"}},
	})
	require.NoError(t, err)
	assert.False(t, result.OK)
	assert.Equal(t, 4, result.Rows)
	assert.Equal(t, "Sheet1!A1:D5", result.Range)
	assert.Equal(t, []string{"Invoice No", "Amount", "Status", "Due"}, result.Headers)

	byCode := map[ProblemCode][]string{}
	for _, p := range result.Problems {
		byCode[p.Code] = append(byCode[p.Code], p.String())
	}
	assert.Equal(t, []string{`Sheet1: column "Nope" not found; headers present: Invoice No, Amount, Status, Due`}, byCode[ProblemMissingColumn])
	assert.Equal(t, []string{"Sheet1!C4: Status is blank", "Sheet1!A5: Invoice No is blank"}, byCode[ProblemBlank])
	assert.Equal(t, []string{`Sheet1!B3: expected number, found "N/A"`, `Sheet1!D5: expected date, found "soon"`}, byCode[ProblemType])
	assert.Equal(t, []string{`Sheet1!A4: duplicate value "INV-1"; first at row 2`}, byCode[ProblemDuplicate])
	assert.Equal(t, []string{`Sheet1!C3: value "Pending" is not one of Done, Open`}, byCode[ProblemNotAllowed])
	assert.Equal(t, 7, result.Count)
	assert.Len(t, result.Problems, 7)
	assert.False(t, result.Truncated)
	assert.Equal(t, Problem{Code: ProblemDuplicate, Sheet: "Sheet1", Cell: "A4", Row: 4, Column: "Invoice No", Message: `duplicate value "INV-1"; first at row 2`}, result.Problems[4])
}

func TestValidateCleanSheetIsOK(t *testing.T) {
	t.Parallel()
	path := validationBook(t)
	result, err := Validate(context.Background(), path, ValidateOptions{
		Range:    "A1:D2",
		Required: []string{"Invoice No", "Amount"},
		NotBlank: []string{"Status"},
		Types:    map[string]ColumnType{"Amount": TypeInteger},
	})
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, 0, result.Count)
	assert.Equal(t, 1, result.Rows)
	assert.Equal(t, []Problem{}, result.Problems, "an OK result still carries an empty list")
}

func TestValidateCapsProblemsAndUsesAliases(t *testing.T) {
	t.Parallel()
	path := validationBook(t)
	result, err := Validate(context.Background(), path, ValidateOptions{
		Columns:     []ColumnSelect{{Source: "Invoice No", As: "invoice"}, {Source: "Amount", As: "amount"}},
		NotBlank:    []string{"invoice"},
		Types:       map[string]ColumnType{"amount": TypeNumber},
		MaxProblems: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Count)
	require.Len(t, result.Problems, 1)
	assert.True(t, result.Truncated)
	assert.Equal(t, []string{"invoice", "amount"}, result.Headers)
	assert.Equal(t, "Amount", result.Problems[0].Column, "problems name the header as read")
	assert.Equal(t, "B3", result.Problems[0].Cell)
}

func TestValidateEmptySheetReportsEveryColumnMissing(t *testing.T) {
	t.Parallel()
	path := saveBook(t, excelize.NewFile(), "empty.xlsx")
	result, err := Validate(context.Background(), path, ValidateOptions{Required: []string{"a"}, Unique: []string{"a", "b"}})
	require.NoError(t, err)
	assert.False(t, result.OK)
	assert.Equal(t, 2, result.Count, "each name once")
	assert.Equal(t, "Sheet1!A1:A1", result.Range)

	_, err = Validate(context.Background(), filepath.Join(t.TempDir(), "none.xlsx"), ValidateOptions{Required: []string{"a"}})
	var missing *NotFoundError
	require.ErrorAs(t, err, &missing)
}

func TestFitJSONKeepsTheLongestFittingPrefix(t *testing.T) {
	t.Parallel()
	problems := make([]Problem, 50)
	for i := range problems {
		problems[i] = Problem{Code: ProblemBlank, Sheet: "S", Cell: "A1", Column: "c", Message: "x"}
	}
	all, cut := FitJSON(problems, 0)
	assert.Len(t, all, 50)
	assert.False(t, cut)
	some, cut := FitJSON(problems, 500)
	assert.True(t, cut)
	assert.NotEmpty(t, some)
	assert.Less(t, len(some), 50)
	assert.LessOrEqual(t, encodedSize(some), 500)
	assert.Greater(t, encodedSize(problems[:len(some)+1]), 500)
}
