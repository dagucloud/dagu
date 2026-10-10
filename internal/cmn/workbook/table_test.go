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
	"golang.org/x/text/encoding/japanese"
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
	table, err := LoadTable(path, LoadOptions{Format: "", Columns: nil})
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "a"}, table.Columns, "JSONL keys keep their text order")
	assert.Len(t, table.Rows, 2)

	bad := filepath.Join(dir, "bad.jsonl")
	require.NoError(t, os.WriteFile(bad, []byte("{\"a\": 1}\n\n\nnot json\n"), 0o600))
	_, err = LoadTable(bad, LoadOptions{Format: "", Columns: nil})
	require.ErrorContains(t, err, "line 4 is not valid JSON")

	long := filepath.Join(dir, "long.jsonl")
	require.NoError(t, os.WriteFile(long, []byte("{\"a\": \""+strings.Repeat("x", 65<<20)+"\"}\n"), 0o600))
	_, err = LoadTable(long, LoadOptions{Format: "", Columns: nil})
	require.ErrorContains(t, err, "token too long")
}

func TestLoadTableCSVRejectsDuplicateHeaders(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "dup.csv")
	require.NoError(t, os.WriteFile(path, []byte("id,name,id\n1,a,2\n"), 0o600))
	_, err := LoadTable(path, LoadOptions{Format: "", Columns: nil})
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

func TestLoadTableEncodingsAndDelimiter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sjis, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("商品;数量\nりんご;3\n"))
	require.NoError(t, err)
	path := filepath.Join(dir, "in.csv")
	require.NoError(t, os.WriteFile(path, sjis, 0o600))
	table, err := LoadTable(path, LoadOptions{Encoding: EncodingShiftJIS, Delimiter: ';'})
	require.NoError(t, err)
	assert.Equal(t, []string{"商品", "数量"}, table.Columns)
	assert.Equal(t, [][]any{{"りんご", "3"}}, table.Rows)

	// Without the encoding the bytes are read as UTF-8 and the header is
	// mangled, which is a data error, not a crash.
	table, err = LoadTable(path, LoadOptions{Delimiter: ';'})
	require.NoError(t, err)
	assert.NotEqual(t, []string{"商品", "数量"}, table.Columns)

	bom := filepath.Join(dir, "bom.csv")
	require.NoError(t, os.WriteFile(bom, []byte("\xEF\xBB\xBFa,b\n1,2\n"), 0o600))
	table, err = LoadTable(bom, LoadOptions{Encoding: EncodingUTF8BOM})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, table.Columns, "the byte order mark is dropped")

	for _, name := range []string{"utf-8", "UTF8", "utf-8-bom", "shift_jis", "Shift-JIS", "sjis", "cp932", "windows-31j", "ms932", ""} {
		_, err := ParseEncoding(name)
		assert.NoError(t, err, name)
	}
	_, err = ParseEncoding("latin1")
	require.ErrorContains(t, err, `unknown encoding "latin1": use utf-8, utf-8-bom, or shift_jis`)
}

// Rows text that still holds a ${...} reference after substitution names the
// reference instead of reporting invalid JSON.
func TestRowsReportUnresolvedReference(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ rows, ref string }{
		{`[{"_row": ${foreach.row._row}, "s": "done"}]`, "${foreach.row._row}"},
		{`[{${foreach.row.key}: 1}]`, "${foreach.row.key}"},
		{`${steps.read.outputs.rows}`, "${steps.read.outputs.rows}"},
		{`[{"a": 1}] ${steps.more.outputs.rows}`, "${steps.more.outputs.rows}"},
	} {
		want := "rows: " + tc.ref + " was not resolved; check that the value it names exists"
		_, err := DecodeUpdateRows(tc.rows)
		require.EqualError(t, err, want, tc.rows)
		_, err = DecodeRows(tc.rows, nil)
		require.EqualError(t, err, want, tc.rows)
	}

	// Text that is more than a reference, or a ${ that never closes, keeps
	// the generic error.
	for _, tc := range []struct{ rows, want string }{
		{`note ${x}`, "rows must be a JSON array of objects"},
		{`[${foo, "x": 1}]`, "rows: invalid JSON: invalid character '$' looking for beginning of value"},
	} {
		_, err := DecodeUpdateRows(tc.rows)
		require.EqualError(t, err, tc.want, tc.rows)
	}

	// A reference inside a JSON string is data, not an error.
	rows, err := DecodeUpdateRows(`[{"note": "${HOME}"}]`)
	require.NoError(t, err)
	assert.Equal(t, []Row{{"note": "${HOME}"}}, rows)
}
