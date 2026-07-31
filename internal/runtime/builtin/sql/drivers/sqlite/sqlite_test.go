// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	sqlexec "github.com/dagucloud/dagu/v2/internal/runtime/builtin/sql"
	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteDriver_Name(t *testing.T) {
	t.Parallel()
	driver := &SQLiteDriver{}
	assert.Equal(t, "sqlite", driver.Name())
}

func TestSQLiteDriver_PlaceholderFormat(t *testing.T) {
	t.Parallel()
	driver := &SQLiteDriver{}
	assert.Equal(t, "?", driver.PlaceholderFormat())
}

func TestSQLiteDriver_SupportsAdvisoryLock(t *testing.T) {
	t.Parallel()
	driver := &SQLiteDriver{}
	assert.False(t, driver.SupportsAdvisoryLock())
}

func TestSQLiteDriver_AcquireAdvisoryLock(t *testing.T) {
	t.Parallel()
	driver := &SQLiteDriver{}
	_, err := driver.AcquireAdvisoryLock(nil, nil, "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestSQLiteDriver_BuildInsertQuery(t *testing.T) {
	t.Parallel()
	driver := &SQLiteDriver{}

	tests := []struct {
		name           string
		table          string
		columns        []string
		rowCount       int
		onConflict     string
		conflictTarget string   // ignored by SQLite but kept for interface compatibility
		updateColumns  []string // ignored by SQLite but kept for interface compatibility
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := driver.BuildInsertQuery(tt.table, tt.columns, tt.rowCount, tt.onConflict, tt.conflictTarget, tt.updateColumns)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSQLiteDriver_QuoteIdentifier(t *testing.T) {
	t.Parallel()
	driver := &SQLiteDriver{}

	tests := []struct {
		name string
		want string
	}{
		{"users", `"users"`},
		{"order", `"order"`},                     // Reserved word
		{`table"name`, `"table""name"`},          // Contains quote
		{"CamelCase", `"CamelCase"`},             // Preserves case
		{"with spaces", `"with spaces"`},         // Contains spaces
		{"special!@#chars", `"special!@#chars"`}, // Special characters
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := driver.QuoteIdentifier(tt.name)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSQLiteDriver_ConvertNamedParams(t *testing.T) {
	t.Parallel()
	driver := &SQLiteDriver{}

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
			wantErr:    false,
		},
		{
			name:       "multiple parameters",
			query:      "SELECT * FROM users WHERE name = :name AND status = :status",
			params:     map[string]any{"name": "Alice", "status": "active"},
			wantQuery:  "SELECT * FROM users WHERE name = ? AND status = ?",
			wantParams: []any{"Alice", "active"},
			wantErr:    false,
		},
		{
			name:       "repeated parameter",
			query:      "SELECT * FROM users WHERE id = :id OR parent_id = :id",
			params:     map[string]any{"id": 123},
			wantQuery:  "SELECT * FROM users WHERE id = ? OR parent_id = ?",
			wantParams: []any{123, 123}, // Each ? needs its own parameter
			wantErr:    false,
		},
		{
			name:       "no parameters in query",
			query:      "SELECT * FROM users",
			params:     map[string]any{"id": 123},
			wantQuery:  "SELECT * FROM users",
			wantParams: nil,
			wantErr:    false,
		},
		{
			name:       "missing parameter",
			query:      "SELECT * FROM users WHERE id = :id",
			params:     map[string]any{"other": 123},
			wantQuery:  "",
			wantParams: nil,
			wantErr:    true,
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

func TestIsMemoryDB(t *testing.T) {
	t.Parallel()
	tests := []struct {
		dsn  string
		want bool
	}{
		{":memory:", true},
		{"mode=memory", true},
		{"file:./test.db?mode=memory", true},
		{"./test.db", false},
		{"file:./test.db", false},
		{"file:./test.db?mode=rwc", false},
	}

	for _, tt := range tests {
		t.Run(tt.dsn, func(t *testing.T) {
			t.Parallel()
			got := isMemoryDB(tt.dsn)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractDBPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "plain file path",
			dsn:  "./test.db",
			want: "./test.db",
		},
		{
			name: "file prefix",
			dsn:  "file:./test.db",
			want: "./test.db",
		},
		{
			name: "file prefix with params",
			dsn:  "file:./test.db?mode=rwc",
			want: "./test.db",
		},
		{
			name: "plain path with params",
			dsn:  "./test.db?_busy_timeout=5000",
			want: "./test.db",
		},
		{
			name: "absolute path",
			dsn:  "/var/data/test.db",
			want: "/var/data/test.db",
		},
		{
			name: "absolute path with file prefix",
			dsn:  "file:/var/data/test.db",
			want: "/var/data/test.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractDBPath(tt.dsn)
			assert.Equal(t, tt.want, got)
		})
	}
}

// newTestDriver creates a new SQLiteDriver for testing.
func newTestDriver() *SQLiteDriver {
	return &SQLiteDriver{
		locks: make(map[string]*flock.Flock),
	}
}

// TestSQLiteDriver_Connect tests the Connect function with various configurations.
func TestSQLiteDriver_Connect(t *testing.T) {
	t.Parallel()

	t.Run("memory database", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		driver := newTestDriver()

		cfg := &sqlexec.Config{
			DSN: ":memory:",
		}

		db, cleanup, err := driver.Connect(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, db)

		// Verify connection works
		_, err = db.ExecContext(ctx, "SELECT 1")
		require.NoError(t, err)

		// Cleanup
		if cleanup != nil {
			_ = cleanup()
		}
		_ = db.Close()
	})

	t.Run("shared memory mode", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		driver := newTestDriver()

		cfg := &sqlexec.Config{
			DSN:          ":memory:",
			SharedMemory: true,
		}

		db, cleanup, err := driver.Connect(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, db)

		// Verify connection works
		_, err = db.ExecContext(ctx, "CREATE TABLE shared_test (id INTEGER)")
		require.NoError(t, err)

		if cleanup != nil {
			_ = cleanup()
		}
		_ = db.Close()
	})

	t.Run("file database", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		driver := newTestDriver()

		dbPath := filepath.Join(t.TempDir(), "test.db")
		cfg := &sqlexec.Config{
			DSN: dbPath,
		}

		db, cleanup, err := driver.Connect(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, db)

		// Verify connection works
		_, err = db.ExecContext(ctx, "CREATE TABLE file_test (id INTEGER)")
		require.NoError(t, err)

		if cleanup != nil {
			_ = cleanup()
		}
		_ = db.Close()
	})

	t.Run("file lock enabled", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		driver := newTestDriver()

		dbPath := filepath.Join(t.TempDir(), "locked.db")
		cfg := &sqlexec.Config{
			DSN:      dbPath,
			FileLock: true,
		}

		db, cleanup, err := driver.Connect(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, db)
		require.NotNil(t, cleanup, "cleanup function should be set when FileLock is enabled")

		// Verify connection works
		_, err = db.ExecContext(ctx, "SELECT 1")
		require.NoError(t, err)

		// Release lock
		err = cleanup()
		require.NoError(t, err)

		_ = db.Close()
	})

	t.Run("file lock not for memory db", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		driver := newTestDriver()

		cfg := &sqlexec.Config{
			DSN:      ":memory:",
			FileLock: true, // Should be ignored for memory databases
		}

		db, cleanup, err := driver.Connect(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, db)
		// No cleanup for memory databases even with FileLock
		assert.Nil(t, cleanup)

		_ = db.Close()
	})
}

// TestSQLiteDriver_Connect_ConcurrentLock tests that file locking prevents concurrent access.
func TestSQLiteDriver_Connect_ConcurrentLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	driver := newTestDriver()

	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	cfg := &sqlexec.Config{
		DSN:      dbPath,
		FileLock: true,
	}

	// First connection should succeed
	db1, cleanup1, err := driver.Connect(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, db1)
	require.NotNil(t, cleanup1)

	// Second connection should fail (database is locked)
	_, _, err = driver.Connect(ctx, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locked")

	// Release first lock
	err = cleanup1()
	require.NoError(t, err)
	_ = db1.Close()

	// Now third connection should succeed
	db3, cleanup3, err := driver.Connect(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, db3)

	if cleanup3 != nil {
		_ = cleanup3()
	}
	_ = db3.Close()
}
