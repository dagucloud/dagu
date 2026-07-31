// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package postgres provides the PostgreSQL driver for the SQL executor.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	sqlexec "github.com/dagucloud/dagu/v2/internal/runtime/builtin/sql"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
)

const (
	// advisoryLockReleaseTimeout is the timeout for releasing advisory locks.
	advisoryLockReleaseTimeout = 30 * time.Second
)

// PostgresDriver implements the Driver interface for PostgreSQL.
type PostgresDriver struct{}

// Name returns the driver name.
func (d *PostgresDriver) Name() string {
	return "postgres"
}

// Connect establishes a connection to PostgreSQL.
func (d *PostgresDriver) Connect(ctx context.Context, cfg *sqlexec.Config) (*sql.DB, func() error, error) {
	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	return db, nil, nil
}

// SupportsAdvisoryLock returns true as PostgreSQL supports advisory locks.
func (d *PostgresDriver) SupportsAdvisoryLock() bool {
	return true
}

// AcquireAdvisoryLock acquires a named advisory lock in PostgreSQL.
func (d *PostgresDriver) AcquireAdvisoryLock(ctx context.Context, db *sql.DB, lockName string) (func() error, error) {
	// Convert lock name to a 64-bit integer using FNV hash
	h := fnv.New64a()
	h.Write([]byte(lockName))
	lockID := int64(h.Sum64())

	// Acquire the advisory lock (blocks until acquired)
	_, err := db.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockID)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire advisory lock %q: %w", lockName, err)
	}

	// Return release function with timeout to prevent indefinite blocking
	release := func() error {
		releaseCtx, cancel := context.WithTimeout(context.Background(), advisoryLockReleaseTimeout)
		defer cancel()
		_, err := db.ExecContext(releaseCtx, "SELECT pg_advisory_unlock($1)", lockID)
		if err != nil {
			return fmt.Errorf("failed to release advisory lock %q: %w", lockName, err)
		}
		return nil
	}

	return release, nil
}

// ConvertNamedParams converts named parameters to PostgreSQL positional format.
func (d *PostgresDriver) ConvertNamedParams(query string, params map[string]any) (string, []any, error) {
	return sqlexec.ConvertNamedToPositional(query, params, "$")
}

// PlaceholderFormat returns the PostgreSQL placeholder format.
func (d *PostgresDriver) PlaceholderFormat() string {
	return "$"
}

// QuoteIdentifier quotes a table or column name for PostgreSQL.
// This handles reserved words and special characters by wrapping in double quotes.
func (d *PostgresDriver) QuoteIdentifier(name string) string {
	return sqlexec.QuoteIdentifier(name)
}

// BuildInsertQuery generates a multi-row INSERT statement for PostgreSQL.
// If onConflict is "replace" and conflictTarget is provided, generates a proper UPSERT.
func (d *PostgresDriver) BuildInsertQuery(table string, columns []string, rowCount int, onConflict, conflictTarget string, updateColumns []string) string {
	var sb strings.Builder

	sb.WriteString("INSERT INTO ")
	sb.WriteString(d.QuoteIdentifier(table))
	sb.WriteString(" (")
	for i, col := range columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(d.QuoteIdentifier(col))
	}
	sb.WriteString(") VALUES ")

	paramIdx := 1
	for i := range rowCount {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for j := range columns {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("$%d", paramIdx))
			paramIdx++
		}
		sb.WriteString(")")
	}

	// ON CONFLICT handling
	switch onConflict {
	case "ignore":
		sb.WriteString(" ON CONFLICT DO NOTHING")
	case "replace":
		if conflictTarget != "" {
			// Proper UPSERT with ON CONFLICT DO UPDATE
			sb.WriteString(" ON CONFLICT (")
			sb.WriteString(conflictTarget) // User provides quoted if needed, e.g., "id" or "(user_id, org_id)"
			sb.WriteString(") DO UPDATE SET ")

			// Determine which columns to update
			colsToUpdate := updateColumns
			if len(colsToUpdate) == 0 {
				// Default: update all columns except conflict target columns
				conflictCols := sqlexec.ParseConflictTarget(conflictTarget)
				for _, col := range columns {
					if !sqlexec.Contains(conflictCols, col) {
						colsToUpdate = append(colsToUpdate, col)
					}
				}
			}

			// Generate SET clause
			for i, col := range colsToUpdate {
				if i > 0 {
					sb.WriteString(", ")
				}
				quotedCol := d.QuoteIdentifier(col)
				sb.WriteString(fmt.Sprintf("%s = EXCLUDED.%s", quotedCol, quotedCol))
			}
		} else {
			// No conflict target provided, fall back to DO NOTHING
			sb.WriteString(" ON CONFLICT DO NOTHING")
		}
	}

	return sb.String()
}

func init() {
	sqlexec.RegisterDriver(&PostgresDriver{})
}
