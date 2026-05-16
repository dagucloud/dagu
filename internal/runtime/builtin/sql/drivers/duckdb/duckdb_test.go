// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package duckdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDuckDBDriver_Name(t *testing.T) {
	t.Parallel()
	driver := &DuckDBDriver{}
	assert.Equal(t, "duckdb", driver.Name())
}

func TestDuckDBDriver_PlaceholderFormat(t *testing.T) {
	t.Parallel()
	driver := &DuckDBDriver{}
	assert.Equal(t, "?", driver.PlaceholderFormat())
}

func TestDuckDBDriver_SupportsAdvisoryLock(t *testing.T) {
	t.Parallel()
	driver := &DuckDBDriver{}
	assert.False(t, driver.SupportsAdvisoryLock())
}

func TestDuckDBDriver_AcquireAdvisoryLock(t *testing.T) {
	t.Parallel()
	driver := &DuckDBDriver{}
	_, err := driver.AcquireAdvisoryLock(nil, nil, "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestDuckDBDriver_BuildInsertQuery(t *testing.T) {
	t.Parallel()
	driver := &DuckDBDriver{}

	tests := []struct {
		name           string
		table          string
		columns        []string
		rowCount       int
		onConflict     string
		conflictTarget string
		updateColumns  []string
		want           string
	}{
		{
			name:       "single row",
			table:      "users",
			columns:    []string{"name", "age"},
			rowCount:   1,
			onConflict: "error",
			want:       `INSERT INTO "users" ("name", "age") VALUES (?, ?)`,
		},
		{
			name:       "multiple rows",
			table:      "users",
			columns:    []string{"name", "age"},
			rowCount:   3,
			onConflict: "error",
			want:       `INSERT INTO "users" ("name", "age") VALUES (?, ?), (?, ?), (?, ?)`,
		},
		{
			name:       "with ignore",
			table:      "users",
			columns:    []string{"id", "name"},
			rowCount:   2,
			onConflict: "ignore",
			want:       `INSERT OR IGNORE INTO "users" ("id", "name") VALUES (?, ?), (?, ?)`,
		},
		{
			name:       "with replace",
			table:      "users",
			columns:    []string{"id", "name"},
			rowCount:   1,
			onConflict: "replace",
			want:       `INSERT OR REPLACE INTO "users" ("id", "name") VALUES (?, ?)`,
		},
		{
			name:       "single column",
			table:      "items",
			columns:    []string{"value"},
			rowCount:   2,
			onConflict: "error",
			want:       `INSERT INTO "items" ("value") VALUES (?), (?)`,
		},
		{
			name:       "reserved word table name",
			table:      "order",
			columns:    []string{"select", "from"},
			rowCount:   1,
			onConflict: "error",
			want:       `INSERT INTO "order" ("select", "from") VALUES (?, ?)`,
		},
		{
			name:           "conflict target ignored by shorthand replace",
			table:          "users",
			columns:        []string{"id", "name", "email"},
			rowCount:       1,
			onConflict:     "replace",
			conflictTarget: "id",
			updateColumns:  []string{"name"},
			want:           `INSERT OR REPLACE INTO "users" ("id", "name", "email") VALUES (?, ?, ?)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := driver.BuildInsertQuery(tt.table, tt.columns, tt.rowCount, tt.onConflict, tt.conflictTarget, tt.updateColumns)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDuckDBDriver_QuoteIdentifier(t *testing.T) {
	t.Parallel()
	driver := &DuckDBDriver{}

	tests := []struct {
		name string
		want string
	}{
		{"users", `"users"`},
		{"order", `"order"`},
		{`table"name`, `"table""name"`},
		{"CamelCase", `"CamelCase"`},
		{"with spaces", `"with spaces"`},
		{"special!@#chars", `"special!@#chars"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := driver.QuoteIdentifier(tt.name)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDuckDBDriver_ConvertNamedParams(t *testing.T) {
	t.Parallel()
	driver := &DuckDBDriver{}

	tests := []struct {
		name       string
		query      string
		params     map[string]any
		wantQuery  string
		wantParams []any
		wantErr    bool
	}{
		{
			name:       "single parameter",
			query:      "SELECT * FROM users WHERE id = :id",
			params:     map[string]any{"id": 123},
			wantQuery:  "SELECT * FROM users WHERE id = ?",
			wantParams: []any{123},
		},
		{
			name:       "multiple parameters",
			query:      "SELECT * FROM users WHERE name = :name AND status = :status",
			params:     map[string]any{"name": "Alice", "status": "active"},
			wantQuery:  "SELECT * FROM users WHERE name = ? AND status = ?",
			wantParams: []any{"Alice", "active"},
		},
		{
			name:       "repeated parameter",
			query:      "SELECT * FROM users WHERE id = :id OR parent_id = :id",
			params:     map[string]any{"id": 123},
			wantQuery:  "SELECT * FROM users WHERE id = ? OR parent_id = ?",
			wantParams: []any{123, 123},
		},
		{
			name:       "no parameters in query",
			query:      "SELECT * FROM users",
			params:     map[string]any{"id": 123},
			wantQuery:  "SELECT * FROM users",
			wantParams: nil,
		},
		{
			name:    "missing parameter",
			query:   "SELECT * FROM users WHERE id = :id",
			params:  map[string]any{"other": 123},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotQuery, gotParams, err := driver.ConvertNamedParams(tt.query, tt.params)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantQuery, gotQuery)
			assert.Equal(t, tt.wantParams, gotParams)
		})
	}
}
