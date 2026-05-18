// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build cgo && ((darwin && (amd64 || arm64)) || (linux && (amd64 || arm64)) || (windows && amd64))

package duckdb

import (
	"context"
	"database/sql"
	"fmt"

	sqlexec "github.com/dagucloud/dagu/internal/runtime/builtin/sql"

	_ "github.com/duckdb/duckdb-go/v2" // DuckDB database/sql driver
)

// Connect establishes a connection to DuckDB.
func (d *DuckDBDriver) Connect(_ context.Context, cfg *sqlexec.Config) (*sql.DB, func() error, error) {
	dsn := cfg.DSN
	if dsn == ":memory:" {
		dsn = ""
	}

	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open duckdb connection: %w", err)
	}

	return db, nil, nil
}
