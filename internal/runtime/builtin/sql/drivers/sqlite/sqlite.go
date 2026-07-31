// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sqlite provides the SQLite driver for the SQL executor.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	sqlexec "github.com/dagucloud/dagu/v2/internal/runtime/builtin/sql"
	"github.com/gofrs/flock"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// SQLiteDriver implements the Driver interface for SQLite.
type SQLiteDriver struct {
	// Track file locks to ensure proper cleanup
	locks   map[string]*flock.Flock
	locksMu sync.Mutex
}

var sqliteDriverInstance = &SQLiteDriver{
	locks: make(map[string]*flock.Flock),
}

// Name returns the driver name.
func (d *SQLiteDriver) Name() string {
	return "sqlite"
}

// Connect establishes a connection to SQLite.
func (d *SQLiteDriver) Connect(ctx context.Context, cfg *sqlexec.Config) (*sql.DB, func() error, error) {
	dsn := cfg.DSN

	// Convert :memory: to shared cache mode if SharedMemory is enabled.
	// This allows multiple steps in a DAG to share the same in-memory database.
	// Without this, each connection to :memory: creates a separate isolated database.
	if cfg.SharedMemory && dsn == ":memory:" {
		dsn = "file::memory:?cache=shared"
	}

	var cleanup func() error

	// Handle file locking if requested
	if cfg.FileLock && !isMemoryDB(dsn) {
		dbPath := extractDBPath(dsn)
		lockPath := dbPath + ".lock"

		// Hold mutex across entire lock creation, acquisition, and storage
		// to prevent race conditions with concurrent Connect() calls
		d.locksMu.Lock()
		fl := flock.New(lockPath)

		locked, err := fl.TryLock()
		if err != nil {
			d.locksMu.Unlock()
			return nil, nil, fmt.Errorf("failed to acquire file lock: %w", err)
		}
		if !locked {
			d.locksMu.Unlock()
			return nil, nil, fmt.Errorf("database is locked by another process")
		}

		d.locks[lockPath] = fl
		d.locksMu.Unlock()

		cleanup = func() error {
			d.locksMu.Lock()
			defer d.locksMu.Unlock()
			if fl, ok := d.locks[lockPath]; ok {
				delete(d.locks, lockPath)
				return fl.Unlock()
			}
			return nil
		}
	}

	// Open SQLite connection
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		if cleanup != nil {
			_ = cleanup()
		}
		return nil, nil, fmt.Errorf("failed to open sqlite connection: %w", err)
	}

	// Set pragmas for robustness
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			if cleanup != nil {
				_ = cleanup()
			}
			return nil, nil, fmt.Errorf("failed to set pragma %q: %w", pragma, err)
		}
	}

	// Return cleanup function for file lock release.
	// Note: db.Close() is handled by ConnectionManager.closeInternal(),
	// not by this cleanup function, to avoid double-close.
	return db, cleanup, nil
}

// SupportsAdvisoryLock returns false as SQLite doesn't support advisory locks.
func (d *SQLiteDriver) SupportsAdvisoryLock() bool {
	return false
}

// AcquireAdvisoryLock is not supported for SQLite.
func (d *SQLiteDriver) AcquireAdvisoryLock(_ context.Context, _ *sql.DB, _ string) (func() error, error) {
	return nil, fmt.Errorf("advisory locks are not supported for SQLite")
}

// ConvertNamedParams converts named parameters to SQLite positional format.
func (d *SQLiteDriver) ConvertNamedParams(query string, params map[string]any) (string, []any, error) {
	return sqlexec.ConvertNamedToPositional(query, params, "?")
}

// PlaceholderFormat returns the SQLite placeholder format.
func (d *SQLiteDriver) PlaceholderFormat() string {
	return "?"
}

// QuoteIdentifier quotes a table or column name for SQLite.
// This handles reserved words and special characters by wrapping in double quotes.
func (d *SQLiteDriver) QuoteIdentifier(name string) string {
	return sqlexec.QuoteIdentifier(name)
}

// BuildInsertQuery generates a multi-row INSERT statement for SQLite.
// Note: SQLite uses INSERT OR IGNORE/REPLACE which doesn't need conflictTarget or updateColumns,
// but we accept these parameters for interface compatibility with PostgreSQL.
func (d *SQLiteDriver) BuildInsertQuery(table string, columns []string, rowCount int, onConflict, conflictTarget string, updateColumns []string) string {
	var sb strings.Builder

	// SQLite uses INSERT OR IGNORE / INSERT OR REPLACE
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

// isMemoryDB checks if the DSN is for an in-memory database.
func isMemoryDB(dsn string) bool {
	return dsn == ":memory:" || strings.Contains(dsn, "mode=memory")
}

// extractDBPath extracts the file path from a SQLite DSN.
func extractDBPath(dsn string) string {
	// Handle "file:" prefix
	if after, ok := strings.CutPrefix(dsn, "file:"); ok {
		path := after
		// Remove query parameters
		if idx := strings.Index(path, "?"); idx >= 0 {
			path = path[:idx]
		}
		return path
	}
	// Plain file path
	if before, _, ok := strings.Cut(dsn, "?"); ok {
		return before
	}
	return dsn
}

func init() {
	sqlexec.RegisterDriver(sqliteDriverInstance)
}
