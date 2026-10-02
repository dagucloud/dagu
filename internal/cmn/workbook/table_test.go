// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeJSONRejectsTrailingText(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"[]]", "{} }", "[1] [2]"} {
		_, err := decodeJSON(bad)
		require.Error(t, err, bad)
	}
}

func TestWriterColumnsKeepColonsAndDropRowNumber(t *testing.T) {
	t.Parallel()
	table, err := DecodeRows(`[{"Time: start": "09:00", "_row": 2, "b": 1}]`, `["Time: start", "_row", "b"]`)
	require.NoError(t, err)
	assert.Equal(t, []string{"Time: start", "b"}, table.Columns)
	assert.Equal(t, [][]any{{"09:00", float64(1)}}, table.Rows)

	table, err = DecodeRows([]any{map[string]any{"Time: start": "x"}}, []any{"Time: start"})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{"x"}}, table.Rows)
}

func TestLoadTableJSONLKeepsOrderAndReportsLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "in.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{\"z\": 1, \"a\": 2}\n\n{\"z\": 3, \"a\": 4}\n"), 0o600))
	table, err := LoadTable(path, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, table.Columns, "JSONL keys keep their text order")
	assert.Len(t, table.Rows, 2)

	bad := filepath.Join(dir, "bad.jsonl")
	require.NoError(t, os.WriteFile(bad, []byte("{\"a\": 1}\n\n\nnot json\n"), 0o600))
	_, err = LoadTable(bad, "", nil)
	require.ErrorContains(t, err, "line 4 is not valid JSON")

	long := filepath.Join(dir, "long.jsonl")
	require.NoError(t, os.WriteFile(long, []byte("{\"a\": \""+strings.Repeat("x", 65<<20)+"\"}\n"), 0o600))
	_, err = LoadTable(long, "", nil)
	require.ErrorContains(t, err, "token too long")
}

func TestLoadTableCSVRejectsDuplicateHeaders(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "dup.csv")
	require.NoError(t, os.WriteFile(path, []byte("id,name,id\n1,a,2\n"), 0o600))
	_, err := LoadTable(path, "", nil)
	require.ErrorContains(t, err, `duplicate header "id" in columns 1 and 3`)
}

func TestForeachAggregateIsUnwrapped(t *testing.T) {
	t.Parallel()
	aggregate := `{"summary": {"total": 2, "succeeded": 1, "failed": 1}, "items": [{"index": 0, "key": "a", "status": "succeeded"}], "outputs": [{"order_id": "a", "status": 200}]}`
	rows, err := DecodeUpdateRows(aggregate)
	require.NoError(t, err)
	assert.Equal(t, []Row{{"order_id": "a", "status": float64(200)}}, rows)
	table, err := DecodeRows(aggregate, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"order_id", "status"}, table.Columns)
}

func TestForeachAggregateKeepsKeyOrder(t *testing.T) {
	t.Parallel()
	aggregate := `{"summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [{"index": 0, "key": "a", "status": "succeeded"}], "outputs": [{"zeta": 1, "alpha": 2}]}`
	table, err := DecodeRows(aggregate, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"zeta", "alpha"}, table.Columns, "the order of the collected object is kept")

	// An ordinary row that happens to have these field names is not an
	// aggregate: its summary is not the foreach shape.
	plain := `{"summary": "quarterly", "items": 3, "outputs": [1, 2]}`
	rows, err := DecodeUpdateRows(plain)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "quarterly", rows[0]["summary"])

	// A row with the exact foreach fields plus one of its own is a row too:
	// the aggregate has nothing but those three fields.
	extra := `{"order_id": 42, "summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [], "outputs": []}`
	table, err = DecodeRows(extra, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1)
	assert.ElementsMatch(t, []string{"order_id", "summary", "items", "outputs"}, table.Columns)

	// A field differing only in case is another field, not the outputs.
	cased := `{"summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [], "outputs": [{"a": 1}], "Outputs": [{"b": 2}]}`
	table, err = DecodeRows(cased, nil)
	require.NoError(t, err)
	require.Len(t, table.Rows, 1, "four fields make it a row, not an aggregate")
	assert.Contains(t, table.Columns, "Outputs")
}

func TestForeachAggregateWithEscapedKeyKeepsOrder(t *testing.T) {
	t.Parallel()
	// JSON may spell a key with escapes; the decoded name is what counts,
	// so the outputs key here is spelled with a \u escape.
	aggregate := `{"summary": {"total": 1, "succeeded": 1, "failed": 0}, "items": [], "out\u0070uts": [{"z": 1, "a": 2}]}`
	table, err := DecodeRows(aggregate, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, table.Columns)
}
