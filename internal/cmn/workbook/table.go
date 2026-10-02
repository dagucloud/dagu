// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Table is writer input: column names and rows of values in column order.
type Table struct {
	Columns []string
	Rows    [][]any
}

// DecodeRows reads rows as a writer receives them: a JSON string, a list of
// objects, or a list of arrays. Objects keep the key order of the JSON text
// when there is one; otherwise keys are sorted. columns, when given, selects
// and orders the fields written, and may itself be a JSON array string or a
// comma-separated list.
func DecodeRows(value any, columns any) (Table, error) {
	order, err := columnOrder(columns)
	if err != nil {
		return Table{}, err
	}
	var jsonOrder []string
	if text, ok := value.(string); ok {
		trimmed := strings.TrimSpace(text)
		if !looksLikeJSON(trimmed) {
			return Table{}, fmt.Errorf("rows must be a JSON array of objects or arrays")
		}
		decoded, err := decodeJSON(trimmed)
		if err != nil {
			return Table{}, fmt.Errorf("rows: %w", err)
		}
		jsonOrder = jsonKeyOrder(trimmed)
		value = decoded
	}
	list, ok := value.([]any)
	if !ok {
		if value == nil {
			return Table{}, fmt.Errorf("rows must be a list")
		}
		list = []any{value}
	}
	if len(list) == 0 {
		return Table{Columns: order, Rows: [][]any{}}, nil
	}
	switch list[0].(type) {
	case map[string]any:
		return objectsToTable(list, order, jsonOrder)
	case []any:
		return arraysToTable(list, order)
	default:
		return Table{}, fmt.Errorf("rows[0] must be an object or an array")
	}
}

// RowsToTable builds a Table from typed rows in header order; columns,
// when given, selects the fields written.
func RowsToTable(rows []Row, headers []string, columns []string) Table {
	order := columns
	if len(order) == 0 {
		order = headers
	}
	table := Table{Columns: append([]string(nil), order...), Rows: make([][]any, 0, len(rows))}
	for _, row := range rows {
		values := make([]any, len(order))
		for i, name := range order {
			values[i] = row[name]
		}
		table.Rows = append(table.Rows, values)
	}
	return table
}

func columnOrder(columns any) ([]string, error) {
	if columns == nil {
		return nil, nil
	}
	selects, err := ParseColumns(columns)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(selects))
	for _, s := range selects {
		names = append(names, s.Source)
	}
	return names, nil
}

func objectsToTable(list []any, order, jsonOrder []string) (Table, error) {
	if len(order) == 0 {
		seen := map[string]bool{}
		for _, key := range jsonOrder {
			if key != RowNumberKey && !seen[key] {
				seen[key] = true
				order = append(order, key)
			}
		}
		var extra []string
		for i, item := range list {
			obj, ok := item.(map[string]any)
			if !ok {
				return Table{}, fmt.Errorf("rows[%d] must be an object like the first row", i)
			}
			for key := range obj {
				if key != RowNumberKey && !seen[key] {
					seen[key] = true
					extra = append(extra, key)
				}
			}
		}
		sort.Strings(extra)
		order = append(order, extra...)
	}
	table := Table{Columns: order, Rows: make([][]any, 0, len(list))}
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return Table{}, fmt.Errorf("rows[%d] must be an object like the first row", i)
		}
		values := make([]any, len(order))
		for j, name := range order {
			values[j] = obj[name]
		}
		table.Rows = append(table.Rows, values)
	}
	return table, nil
}

func arraysToTable(list []any, order []string) (Table, error) {
	width := 0
	rows := make([][]any, 0, len(list))
	for i, item := range list {
		arr, ok := item.([]any)
		if !ok {
			return Table{}, fmt.Errorf("rows[%d] must be an array like the first row", i)
		}
		width = max(width, len(arr))
		rows = append(rows, arr)
	}
	if len(order) == 0 {
		for c := 1; c <= width; c++ {
			name, _ := excelize.ColumnNumberToName(c)
			order = append(order, name)
		}
	}
	for i := range rows {
		if len(rows[i]) < len(order) {
			padded := make([]any, len(order))
			copy(padded, rows[i])
			rows[i] = padded
		} else if len(rows[i]) > len(order) {
			rows[i] = rows[i][:len(order)]
		}
	}
	return Table{Columns: order, Rows: rows}, nil
}

// LoadTable reads rows from a json, jsonl, or csv file. An empty format is
// taken from the extension. CSV files may start with a UTF-8 byte order
// mark; their first line is the header.
func LoadTable(path, format string, columns any) (Table, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Table{}, fmt.Errorf("input: %w", err)
	}
	data = []byte(strings.TrimPrefix(string(data), "\xEF\xBB\xBF"))
	switch format {
	case "json":
		return DecodeRows(string(data), columns)
	case "jsonl", "ndjson":
		var list []any
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		scanner.Buffer(make([]byte, 1<<20), 64<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var v any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				return Table{}, fmt.Errorf("input: line %d: %w", len(list)+1, err)
			}
			list = append(list, v)
		}
		if len(list) == 0 {
			return DecodeRows("[]", columns)
		}
		return DecodeRows(list, columns)
	case "csv":
		return csvToTable(strings.NewReader(string(data)), columns)
	default:
		return Table{}, fmt.Errorf("input format %q is not json, jsonl, or csv", format)
	}
}

func csvToTable(r io.Reader, columns any) (Table, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return Table{}, fmt.Errorf("input: %w", err)
	}
	if len(records) == 0 {
		return DecodeRows("[]", columns)
	}
	headers := records[0]
	list := make([]any, 0, len(records)-1)
	for _, rec := range records[1:] {
		obj := make(map[string]any, len(headers))
		for i, h := range headers {
			if i < len(rec) {
				obj[h] = csvValue(rec[i])
			}
		}
		list = append(list, obj)
	}
	order, err := columnOrder(columns)
	if err != nil {
		return Table{}, err
	}
	if len(order) == 0 {
		order = headers
	}
	return objectsToTable(list, order, nil)
}

// csvValue keeps CSV fields as text, since a CSV carries no types; the
// writer turns numeric-looking text into numbers only when a column type
// says so.
func csvValue(s string) any {
	return s
}
