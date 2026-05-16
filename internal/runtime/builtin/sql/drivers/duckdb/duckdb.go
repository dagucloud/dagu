// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package duckdb provides the DuckDB driver for the SQL executor.
package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	sqlexec "github.com/dagucloud/dagu/internal/runtime/builtin/sql"
)

// DuckDBDriver implements the Driver interface for DuckDB.
type DuckDBDriver struct{}

// Name returns the driver name.
func (d *DuckDBDriver) Name() string {
	return "duckdb"
}

// SupportsAdvisoryLock returns false as DuckDB doesn't support PostgreSQL advisory locks.
func (d *DuckDBDriver) SupportsAdvisoryLock() bool {
	return false
}

// AcquireAdvisoryLock is not supported for DuckDB.
func (d *DuckDBDriver) AcquireAdvisoryLock(_ context.Context, _ *sql.DB, _ string) (func() error, error) {
	return nil, fmt.Errorf("advisory locks are not supported for DuckDB")
}

// ConvertNamedParams converts named parameters to DuckDB positional format.
func (d *DuckDBDriver) ConvertNamedParams(query string, params map[string]any) (string, []any, error) {
	return sqlexec.ConvertNamedToPositional(query, params, "?")
}

// PlaceholderFormat returns the DuckDB placeholder format.
func (d *DuckDBDriver) PlaceholderFormat() string {
	return "?"
}

// QuoteIdentifier quotes a table or column name for DuckDB.
func (d *DuckDBDriver) QuoteIdentifier(name string) string {
	return sqlexec.QuoteIdentifier(name)
}

// BuildInsertQuery generates a multi-row INSERT statement for DuckDB.
// DuckDB supports SQLite-style INSERT OR IGNORE / INSERT OR REPLACE syntax.
func (d *DuckDBDriver) BuildInsertQuery(table string, columns []string, rowCount int, onConflict, conflictTarget string, updateColumns []string) string {
	var sb strings.Builder

	switch onConflict {
	case "ignore":
		sb.WriteString("INSERT OR IGNORE INTO ")
	case "replace":
		sb.WriteString("INSERT OR REPLACE INTO ")
	default:
		sb.WriteString("INSERT INTO ")
	}

	sb.WriteString(d.QuoteIdentifier(table))
	sb.WriteString(" (")
	for i, col := range columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(d.QuoteIdentifier(col))
	}
	sb.WriteString(") VALUES ")

	for i := range rowCount {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for j := range columns {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("?")
		}
		sb.WriteString(")")
	}

	return sb.String()
}

func init() {
	sqlexec.RegisterDriver(&DuckDBDriver{})
}
