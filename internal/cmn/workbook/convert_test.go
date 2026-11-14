// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/japanese"
)

func exportBook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "商品", "Qty", "Price", "Done", "When")
	setRow(t, f, "Sheet1", "A2", "りんご", 3, 1.5, true, "2026-10-01")
	setRow(t, f, "Sheet1", "A3", "pear, \"ripe\"", nil, 2, false, nil)
	return saveBook(t, f, "export.xlsx")
}

func TestConvertWritesCSVJSONAndJSONL(t *testing.T) {
	t.Parallel()
	path := exportBook(t)
	dir := filepath.Dir(path)

	csvOut := filepath.Join(dir, "out.csv")
	result, err := Convert(context.Background(), path, ConvertOptions{Output: csvOut})
	require.NoError(t, err)
	assert.Equal(t, &ConvertResult{Path: csvOut, Format: "csv", Count: 2, Sheet: "Sheet1", Range: "Sheet1!A1:E3", Warnings: []string{}}, result)
	data, err := os.ReadFile(csvOut)
	require.NoError(t, err)
	assert.Equal(t, "商品,Qty,Price,Done,When\nりんご,3,1.5,true,2026-10-01\n\"pear, \"\"ripe\"\"\",,2,false,\n", string(data))

	jsonOut := filepath.Join(dir, "out.json")
	result, err = Convert(context.Background(), path, ConvertOptions{Output: jsonOut, Columns: []ColumnSelect{{Source: "Qty", As: "qty"}, {Source: "商品", As: "name"}}})
	require.NoError(t, err)
	assert.Equal(t, "json", result.Format)
	data, err = os.ReadFile(jsonOut)
	require.NoError(t, err)
	assert.Equal(t, "[\n{\"qty\":3,\"name\":\"りんご\"},\n{\"qty\":null,\"name\":\"pear, \\\"ripe\\\"\"}\n]\n", string(data), "keys follow the column order and _row is not written")
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, float64(3), decoded[0]["qty"])

	jsonlOut := filepath.Join(dir, "out.ndjson")
	result, err = Convert(context.Background(), path, ConvertOptions{Output: jsonlOut, Format: ConvertJSONL, Types: map[string]ColumnType{"Qty": TypeString}})
	require.NoError(t, err)
	assert.Equal(t, "jsonl", result.Format, "an explicit format wins over the extension")
	data, err = os.ReadFile(jsonlOut)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, `{"商品":"りんご","Qty":"3","Price":1.5,"Done":true,"When":"2026-10-01"}`, lines[0])
	assert.Equal(t, `{"商品":"pear, \"ripe\"","Qty":null,"Price":2,"Done":false,"When":null}`, lines[1])

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 4, "no temporary files are left")
}

func TestConvertShiftJISAndDelimiter(t *testing.T) {
	t.Parallel()
	path := exportBook(t)
	out := filepath.Join(filepath.Dir(path), "sjis.csv")
	_, err := Convert(context.Background(), path, ConvertOptions{Output: out, Encoding: EncodingShiftJIS, Delimiter: ';', Range: "A1:B2"})
	require.NoError(t, err)
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.False(t, strings.HasPrefix(string(raw), "\xEF\xBB\xBF"))
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(raw)
	require.NoError(t, err)
	assert.Equal(t, "商品;Qty\nりんご;3\n", string(decoded))
	assert.NotEqual(t, []byte("商品"), raw[:len("商品")], "the bytes are not UTF-8")

	bom := filepath.Join(filepath.Dir(path), "bom.csv")
	_, err = Convert(context.Background(), path, ConvertOptions{Output: bom, Encoding: EncodingUTF8BOM, Range: "A1:A1"})
	require.NoError(t, err)
	raw, err = os.ReadFile(bom)
	require.NoError(t, err)
	assert.Equal(t, "\xEF\xBB\xBF商品\n", string(raw))

	// The exported file reads back through LoadTable with the same options.
	table, err := LoadTable(out, LoadOptions{Encoding: EncodingShiftJIS, Delimiter: ';'})
	require.NoError(t, err)
	assert.Equal(t, []string{"商品", "Qty"}, table.Columns)
}

func TestConvertReadsEveryRowAndReportsErrors(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	setRow(t, f, "Sheet1", "A1", "n")
	for r := 2; r <= DefaultMaxRows+2; r++ {
		setRow(t, f, "Sheet1", cellName(1, r), r)
	}
	path := saveBook(t, f, "big.xlsx")
	out := filepath.Join(filepath.Dir(path), "big.jsonl")
	result, err := Convert(context.Background(), path, ConvertOptions{Output: out})
	require.NoError(t, err)
	assert.Equal(t, DefaultMaxRows+1, result.Count, "no row cap applies to a file export")
	assert.Empty(t, result.Warnings)

	_, err = Convert(context.Background(), path, ConvertOptions{Output: filepath.Join(filepath.Dir(path), "out.txt")})
	require.ErrorContains(t, err, `output extension ".txt" is not json, jsonl, or csv; set format`)
	_, err = ParseConvertFormat("xml", "out.xml")
	require.ErrorContains(t, err, "format must be json, jsonl, or csv")
	text := exportBook(t)
	_, err = Convert(context.Background(), text, ConvertOptions{Output: out, Types: map[string]ColumnType{"商品": TypeNumber}})
	require.ErrorContains(t, err, `export.xlsx Sheet1!A2: expected number, found "りんご"`)
	_, err = Convert(context.Background(), path, ConvertOptions{Output: filepath.Join(filepath.Dir(path), "missing", "out.csv")})
	require.Error(t, err, "a missing output directory is an error")
	_, err = Convert(context.Background(), filepath.Join(t.TempDir(), "none.xlsx"), ConvertOptions{Output: out})
	var missing *NotFoundError
	require.ErrorAs(t, err, &missing)
}
