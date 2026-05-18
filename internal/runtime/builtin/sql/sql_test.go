// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sql_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlexec "github.com/dagucloud/dagu/internal/runtime/builtin/sql"
	// Import drivers for testing
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/sql/drivers/duckdb"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/sql/drivers/postgres"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/sql/drivers/sqlite"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  map[string]any
		wantErr bool
		check   func(*testing.T, *sqlexec.Config)
	}{
		{
			name: "basic config",
			config: map[string]any{
				"dsn": "postgres://localhost/test",
			},
			wantErr: false,
			check: func(t *testing.T, cfg *sqlexec.Config) {
				assert.Equal(t, "postgres://localhost/test", cfg.DSN)
				assert.Equal(t, "jsonl", cfg.OutputFormat)
				assert.Equal(t, 60, cfg.Timeout)
			},
		},
		{
			name: "full config",
			config: map[string]any{
				"dsn":             "postgres://localhost/test",
				"timeout":         120,
				"transaction":     true,
				"isolation_level": "serializable",
				"output_format":   "csv",
				"headers":         true,
				"max_rows":        1000,
			},
			wantErr: false,
			check: func(t *testing.T, cfg *sqlexec.Config) {
				assert.Equal(t, 120, cfg.Timeout)
				assert.True(t, cfg.Transaction)
				assert.Equal(t, "serializable", cfg.IsolationLevel)
				assert.Equal(t, "csv", cfg.OutputFormat)
				assert.True(t, cfg.Headers)
				assert.Equal(t, 1000, cfg.MaxRows)
			},
		},
		{
			name:    "missing dsn",
			config:  map[string]any{},
			wantErr: true,
		},
		{
			name: "invalid output format",
			config: map[string]any{
				"dsn":           "postgres://localhost/test",
				"output_format": "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := sqlexec.ParseConfig(context.Background(), tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestDuckDBConfigSchemaRegistered(t *testing.T) {
	t.Parallel()

	err := core.ValidateExecutorConfig("duckdb", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid duckdb config")

	err = core.ValidateExecutorConfig("duckdb", map[string]any{
		"dsn":           ":memory:",
		"output_format": "jsonl",
	})
	require.NoError(t, err)
}

func TestConvertNamedToPositional(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		params      map[string]any
		placeholder string
		wantQuery   string
		wantParams  []any
		wantErr     bool
	}{
		{
			name:        "single parameter",
			query:       "SELECT * FROM users WHERE id = :id",
			params:      map[string]any{"id": 123},
			placeholder: "$",
			wantQuery:   "SELECT * FROM users WHERE id = $1",
			wantParams:  []any{123},
			wantErr:     false,
		},
		{
			name:        "multiple parameters",
			query:       "SELECT * FROM users WHERE name = :name AND status = :status",
			params:      map[string]any{"name": "Alice", "status": "active"},
			placeholder: "$",
			wantQuery:   "SELECT * FROM users WHERE name = $1 AND status = $2",
			wantParams:  []any{"Alice", "active"},
			wantErr:     false,
		},
		{
			name:        "repeated parameter",
			query:       "SELECT * FROM users WHERE id = :id OR parent_id = :id",
			params:      map[string]any{"id": 123},
			placeholder: "$",
			wantQuery:   "SELECT * FROM users WHERE id = $1 OR parent_id = $1",
			wantParams:  []any{123},
			wantErr:     false,
		},
		{
			name:        "question mark placeholder",
			query:       "SELECT * FROM users WHERE id = :id",
			params:      map[string]any{"id": 123},
			placeholder: "?",
			wantQuery:   "SELECT * FROM users WHERE id = ?",
			wantParams:  []any{123},
			wantErr:     false,
		},
		{
			name:        "missing parameter",
			query:       "SELECT * FROM users WHERE id = :id",
			params:      map[string]any{"other": 123},
			placeholder: "$",
			wantErr:     true,
		},
		{
			name:        "no parameters in query",
			query:       "SELECT * FROM users",
			params:      map[string]any{"id": 123},
			placeholder: "$",
			wantQuery:   "SELECT * FROM users",
			wantParams:  nil,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotQuery, gotParams, err := sqlexec.ConvertNamedToPositional(tt.query, tt.params, tt.placeholder)
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

func TestExtractParamNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "single param",
			query: "SELECT * FROM users WHERE id = :id",
			want:  []string{"id"},
		},
		{
			name:  "multiple params",
			query: "SELECT * FROM users WHERE name = :name AND status = :status",
			want:  []string{"name", "status"},
		},
		{
			name:  "repeated params",
			query: "SELECT * FROM users WHERE id = :id OR parent_id = :id",
			want:  []string{"id"},
		},
		{
			name:  "no params",
			query: "SELECT * FROM users",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := sqlexec.ExtractParamNames(tt.query)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJSONLWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewJSONLWriter(&buf, "null")

	err := writer.WriteHeader([]string{"id", "name", "amount"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{1, "Alice", 100.5})
	require.NoError(t, err)

	err = writer.WriteRow([]any{2, "Bob", nil})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, `"id":1`)
	assert.Contains(t, output, `"name":"Alice"`)
	assert.Contains(t, output, `"amount":100.5`)
	assert.Contains(t, output, `"name":"Bob"`)
	assert.Contains(t, output, `"amount":null`)
}

func TestCSVWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewCSVWriter(&buf, "NULL", true)

	err := writer.WriteHeader([]string{"id", "name"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{1, "Alice"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{2, nil})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "id,name")
	assert.Contains(t, output, "1,Alice")
	assert.Contains(t, output, "2,NULL")
}

func TestJSONWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewJSONWriter(&buf, "null")

	err := writer.WriteHeader([]string{"id", "name"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{1, "Alice"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{2, "Bob"})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, `"id": 1`)
	assert.Contains(t, output, `"name": "Alice"`)
	assert.Contains(t, output, `"name": "Bob"`)
	// Should be an array
	assert.True(t, output[0] == '[')
}

func TestDriverRegistry(t *testing.T) {
	t.Parallel()

	// Test that drivers are registered
	postgres, ok := sqlexec.GetDriver("postgres")
	assert.True(t, ok)
	assert.NotNil(t, postgres)
	assert.Equal(t, "postgres", postgres.Name())

	sqlite, ok := sqlexec.GetDriver("sqlite")
	assert.True(t, ok)
	assert.NotNil(t, sqlite)
	assert.Equal(t, "sqlite", sqlite.Name())

	duckdb, ok := sqlexec.GetDriver("duckdb")
	assert.True(t, ok)
	assert.NotNil(t, duckdb)
	assert.Equal(t, "duckdb", duckdb.Name())

	// Test unknown driver
	_, ok = sqlexec.GetDriver("unknown")
	assert.False(t, ok)
}

func TestSanitizeIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		wantErr bool
	}{
		{"users", false},
		{"user_table", false},
		{"schema.table", false},
		{"Table123", false},
		{"123table", true},
		{"user-table", true},
		{"user;table", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_wantErr_%v", tt.input, tt.wantErr), func(t *testing.T) {
			t.Parallel()

			_, err := sqlexec.SanitizeIdentifier(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "all params provided",
			query:   "SELECT * FROM users WHERE id = :id AND name = :name",
			params:  map[string]any{"id": 1, "name": "Alice"},
			wantErr: false,
		},
		{
			name:    "missing param",
			query:   "SELECT * FROM users WHERE id = :id AND name = :name",
			params:  map[string]any{"id": 1},
			wantErr: true,
		},
		{
			name:    "extra params ok",
			query:   "SELECT * FROM users WHERE id = :id",
			params:  map[string]any{"id": 1, "extra": "ignored"},
			wantErr: false,
		},
		{
			name:    "no params needed",
			query:   "SELECT * FROM users",
			params:  map[string]any{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := sqlexec.ValidateParams(tt.query, tt.params)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Helper function to create an executor via the registry
func newSQLiteExecutor(t *testing.T, ctx context.Context, step core.Step) (executor.Executor, error) {
	t.Helper()

	exec, err := executor.NewExecutor(ctx, step)
	if err == nil {
		attachExecutorCleanup(t, exec)
	}
	return exec, err
}

func attachExecutorCleanup(t *testing.T, exec executor.Executor) {
	t.Helper()

	t.Cleanup(func() {
		require.NoError(t, executor.CloseExecutor(exec))
	})
}

func TestDuckDBExecutor_InMemory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	step := core.Step{
		Name: "test-duckdb",
		ExecutorConfig: core.ExecutorConfig{
			Type: "duckdb",
			Config: map[string]any{
				"dsn": ":memory:",
			},
		},
		Script: `
			CREATE TABLE people (id INTEGER, name VARCHAR);
			INSERT INTO people VALUES (1, 'Alice'), (2, 'Bob');
			SELECT id, name FROM people ORDER BY id;
		`,
	}

	exec, err := executor.NewExecutor(ctx, step)
	require.NoError(t, err)
	attachExecutorCleanup(t, exec)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, `"id":1`)
	assert.Contains(t, output, `"name":"Alice"`)
	assert.Contains(t, output, `"id":2`)
	assert.Contains(t, output, `"name":"Bob"`)
}

func TestDuckDBExecutor_ImportCSV(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "import.duckdb")
	csvPath := filepath.Join(tmpDir, "users.csv")
	require.NoError(t, os.WriteFile(csvPath, []byte("id,name\n1,Alice\n2,Bob\n"), 0600))

	setupStep := core.Step{
		Name: "setup-duckdb",
		ExecutorConfig: core.ExecutorConfig{
			Type: "duckdb",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: `CREATE TABLE users (id INTEGER, name VARCHAR);`,
	}

	setupExec, err := executor.NewExecutor(ctx, setupStep)
	require.NoError(t, err)
	attachExecutorCleanup(t, setupExec)
	require.NoError(t, setupExec.Run(ctx))

	importStep := core.Step{
		Name: "import-users",
		ExecutorConfig: core.ExecutorConfig{
			Type: "duckdb",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file": csvPath,
					"table":      "users",
					"format":     "csv",
				},
			},
		},
	}

	importExec, err := executor.NewExecutor(ctx, importStep)
	require.NoError(t, err)
	attachExecutorCleanup(t, importExec)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)
	require.NoError(t, importExec.Run(ctx))
	assert.Contains(t, stderr.String(), `"rows_imported":2`)

	queryStep := core.Step{
		Name: "query-users",
		ExecutorConfig: core.ExecutorConfig{
			Type: "duckdb",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: `SELECT id, name FROM users ORDER BY id;`,
	}

	queryExec, err := executor.NewExecutor(ctx, queryStep)
	require.NoError(t, err)
	attachExecutorCleanup(t, queryExec)

	var stdout bytes.Buffer
	queryExec.SetStdout(&stdout)
	require.NoError(t, queryExec.Run(ctx))
	assert.Contains(t, stdout.String(), `"id":1`)
	assert.Contains(t, stdout.String(), `"name":"Alice"`)
	assert.Contains(t, stdout.String(), `"id":2`)
	assert.Contains(t, stdout.String(), `"name":"Bob"`)
}

func TestSQLiteExecutor_InMemory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create a step with SQLite config
	step := core.Step{
		Name: "test-sqlite",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": ":memory:",
			},
		},
		Script: `
			CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT);
			INSERT INTO test (name) VALUES ('Alice'), ('Bob');
			SELECT * FROM test;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
}

func TestSQLiteExecutor_FileDB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a step with SQLite file database
	step := core.Step{
		Name: "test-sqlite-file",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": "file:" + dbPath + "?mode=rwc",
			},
		},
		Script: `
			CREATE TABLE IF NOT EXISTS counter (id INTEGER PRIMARY KEY, value INTEGER);
			INSERT INTO counter (value) VALUES (42);
			SELECT value FROM counter;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "42")

	// Verify file was created
	_, err = os.Stat(dbPath)
	assert.NoError(t, err)
}

func TestSQLiteExecutor_Transaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	step := core.Step{
		Name: "test-transaction",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":         ":memory:",
				"transaction": true,
			},
		},
		Script: `
			CREATE TABLE accounts (id INTEGER PRIMARY KEY, balance INTEGER);
			INSERT INTO accounts (balance) VALUES (100), (200);
			UPDATE accounts SET balance = balance - 50 WHERE id = 1;
			UPDATE accounts SET balance = balance + 50 WHERE id = 2;
			SELECT SUM(balance) as total FROM accounts;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// Total should be 300 (100 + 200, unchanged by transfer)
	output := stdout.String()
	assert.Contains(t, output, "300")
}

func TestSQLiteExecutor_OutputFormats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name         string
		outputFormat string
		headers      bool
		contains     []string
		notContains  []string
	}{
		{
			name:         "jsonl",
			outputFormat: "jsonl",
			contains:     []string{`"id":1`, `"name":"Alice"`},
		},
		{
			name:         "csv with headers",
			outputFormat: "csv",
			headers:      true,
			contains:     []string{"id,name", "1,Alice"},
		},
		{
			name:         "csv without headers",
			outputFormat: "csv",
			headers:      false,
			contains:     []string{"1,Alice"},
			notContains:  []string{"id,name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			step := core.Step{
				Name: "test-format",
				ExecutorConfig: core.ExecutorConfig{
					Type: "sqlite",
					Config: map[string]any{
						"dsn":           ":memory:",
						"output_format": tt.outputFormat,
						"headers":       tt.headers,
					},
				},
				Script: `
					CREATE TABLE test (id INTEGER, name TEXT);
					INSERT INTO test VALUES (1, 'Alice');
					SELECT * FROM test;
				`,
			}

			exec, err := newSQLiteExecutor(t, ctx, step)
			require.NoError(t, err)

			var stdout bytes.Buffer
			exec.SetStdout(&stdout)

			err = exec.Run(ctx)
			require.NoError(t, err)

			output := stdout.String()
			for _, s := range tt.contains {
				assert.Contains(t, output, s)
			}
			for _, s := range tt.notContains {
				assert.NotContains(t, output, s)
			}
		})
	}
}

func TestSQLiteExecutor_MaxRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	step := core.Step{
		Name: "test-maxrows",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":      ":memory:",
				"max_rows": 2,
			},
		},
		Script: `
			CREATE TABLE numbers (n INTEGER);
			INSERT INTO numbers VALUES (1), (2), (3), (4), (5);
			SELECT n FROM numbers ORDER BY n;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	// Should only have 2 rows
	assert.Contains(t, output, `"n":1`)
	assert.Contains(t, output, `"n":2`)
	assert.NotContains(t, output, `"n":3`)
}

// --- Additional Unit Tests for Extended Coverage ---

func TestParseConfig_IsolationLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{"read_committed", "read_committed", false},
		{"repeatable_read", "repeatable_read", false},
		{"serializable", "serializable", false},
		{"default", "default", false},
		{"empty is valid", "", false},
		{"invalid", "invalid_level", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := map[string]any{
				"dsn":             "postgres://localhost/test",
				"isolation_level": tt.level,
			}
			_, err := sqlexec.ParseConfig(context.Background(), config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseConfig_Streaming(t *testing.T) {
	t.Parallel()

	config := map[string]any{
		"dsn":         "postgres://localhost/test",
		"streaming":   true,
		"output_file": "/tmp/output.jsonl",
	}

	cfg, err := sqlexec.ParseConfig(context.Background(), config)
	require.NoError(t, err)
	assert.True(t, cfg.Streaming)
	assert.Equal(t, "/tmp/output.jsonl", cfg.OutputFile)
}

func TestParseConfig_AdvisoryLock(t *testing.T) {
	t.Parallel()

	config := map[string]any{
		"dsn":           "postgres://localhost/test",
		"advisory_lock": "my_pipeline_lock",
	}

	cfg, err := sqlexec.ParseConfig(context.Background(), config)
	require.NoError(t, err)
	assert.Equal(t, "my_pipeline_lock", cfg.AdvisoryLock)
}

func TestParseConfig_FileLock(t *testing.T) {
	t.Parallel()

	config := map[string]any{
		"dsn":       "file:./test.db",
		"file_lock": true,
	}

	cfg, err := sqlexec.ParseConfig(context.Background(), config)
	require.NoError(t, err)
	assert.True(t, cfg.FileLock)
}

func TestParseConfig_Import(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  map[string]any
		wantErr bool
		check   func(*testing.T, *sqlexec.Config)
	}{
		{
			name: "basic import config",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file": "/path/to/data.csv",
					"table":      "users",
				},
			},
			wantErr: false,
			check: func(t *testing.T, cfg *sqlexec.Config) {
				require.NotNil(t, cfg.Import)
				assert.Equal(t, "/path/to/data.csv", cfg.Import.InputFile)
				assert.Equal(t, "users", cfg.Import.Table)
				assert.Equal(t, "csv", cfg.Import.Format)       // auto-detected
				assert.Equal(t, 1000, cfg.Import.BatchSize)     // default
				assert.Equal(t, "error", cfg.Import.OnConflict) // default
			},
		},
		{
			name: "full import config",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file":  "/path/to/data.tsv",
					"table":       "users",
					"format":      "tsv",
					"has_header":  true,
					"delimiter":   "\t",
					"columns":     []string{"name", "age"},
					"null_values": []string{"NA", "N/A"},
					"batch_size":  500,
					"on_conflict": "ignore",
					"skip_rows":   1,
					"max_rows":    1000,
					"dry_run":     true,
				},
			},
			wantErr: false,
			check: func(t *testing.T, cfg *sqlexec.Config) {
				require.NotNil(t, cfg.Import)
				assert.Equal(t, "tsv", cfg.Import.Format)
				require.NotNil(t, cfg.Import.HasHeader)
				assert.True(t, *cfg.Import.HasHeader)
				assert.Equal(t, "\t", cfg.Import.Delimiter)
				assert.Equal(t, []string{"name", "age"}, cfg.Import.Columns)
				assert.Equal(t, []string{"NA", "N/A"}, cfg.Import.NullValues)
				assert.Equal(t, 500, cfg.Import.BatchSize)
				assert.Equal(t, "ignore", cfg.Import.OnConflict)
				assert.Equal(t, 1, cfg.Import.SkipRows)
				assert.Equal(t, 1000, cfg.Import.MaxRows)
				assert.True(t, cfg.Import.DryRun)
			},
		},
		{
			name: "import with jsonl format",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file": "/path/to/data.jsonl",
					"table":      "users",
				},
			},
			wantErr: false,
			check: func(t *testing.T, cfg *sqlexec.Config) {
				require.NotNil(t, cfg.Import)
				assert.Equal(t, "jsonl", cfg.Import.Format) // auto-detected
			},
		},
		{
			name: "missing input_file",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"table": "users",
				},
			},
			wantErr: true,
		},
		{
			name: "missing table",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file": "/path/to/data.csv",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid format",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file": "/path/to/data.csv",
					"table":      "users",
					"format":     "xml",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid on_conflict",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file":  "/path/to/data.csv",
					"table":       "users",
					"on_conflict": "crash",
				},
			},
			wantErr: true,
		},
		{
			name: "negative batch_size",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file": "/path/to/data.csv",
					"table":      "users",
					"batch_size": -1,
				},
			},
			wantErr: true,
		},
		{
			name: "on_conflict replace",
			config: map[string]any{
				"dsn": "file:./test.db",
				"import": map[string]any{
					"input_file":  "/path/to/data.csv",
					"table":       "users",
					"on_conflict": "replace",
				},
			},
			wantErr: false,
			check: func(t *testing.T, cfg *sqlexec.Config) {
				require.NotNil(t, cfg.Import)
				assert.Equal(t, "replace", cfg.Import.OnConflict)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := sqlexec.ParseConfig(context.Background(), tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// --- Integration Tests for CSV/JSONL Import ---

func TestSQLiteExecutor_ImportCSV(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create CSV file
	csvPath := filepath.Join(tmpDir, "users.csv")
	csvContent := "name,age,city\nAlice,30,NYC\nBob,25,LA\nCharlie,35,Chicago\n"
	require.NoError(t, os.WriteFile(csvPath, []byte(csvContent), 0o644))

	// Create database file
	dbPath := filepath.Join(tmpDir, "test.db")

	// First create the table
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "CREATE TABLE users (name TEXT, age INTEGER, city TEXT);",
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Now import the CSV
	importStep := core.Step{
		Name: "import-users",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file": csvPath,
					"table":      "users",
					"format":     "csv",
					"has_header": true,
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)

	err = importExec.Run(ctx)
	require.NoError(t, err)

	// Check metrics
	metricsOutput := stderr.String()
	assert.Contains(t, metricsOutput, `"rows_imported":3`)
	assert.Contains(t, metricsOutput, `"status":"completed"`)

	// Verify imported data
	verifyStep := core.Step{
		Name: "verify",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "SELECT * FROM users ORDER BY name;",
	}

	verifyExec, err := newSQLiteExecutor(t, ctx, verifyStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	verifyExec.SetStdout(&stdout)
	require.NoError(t, verifyExec.Run(ctx))

	output := stdout.String()
	assert.Contains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
	assert.Contains(t, output, "Charlie")
}

func TestSQLiteExecutor_ImportCSV_NoHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create CSV file without header
	csvPath := filepath.Join(tmpDir, "data.csv")
	csvContent := "Alice,30,NYC\nBob,25,LA\n"
	require.NoError(t, os.WriteFile(csvPath, []byte(csvContent), 0o644))

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create table
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "CREATE TABLE users (name TEXT, age INTEGER, city TEXT);",
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Import with explicit columns
	importStep := core.Step{
		Name: "import",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file": csvPath,
					"table":      "users",
					"has_header": false,
					"columns":    []string{"name", "age", "city"},
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)
	require.NoError(t, importExec.Run(ctx))

	assert.Contains(t, stderr.String(), `"rows_imported":2`)
}

func TestSQLiteExecutor_ImportJSONL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create JSONL file
	jsonlPath := filepath.Join(tmpDir, "users.jsonl")
	jsonlContent := `{"name":"Alice","age":30,"city":"NYC"}
{"name":"Bob","age":25,"city":"LA"}
`
	require.NoError(t, os.WriteFile(jsonlPath, []byte(jsonlContent), 0o644))

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create table
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "CREATE TABLE users (name TEXT, age INTEGER, city TEXT);",
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Import JSONL
	importStep := core.Step{
		Name: "import",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file": jsonlPath,
					"table":      "users",
					"columns":    []string{"name", "age", "city"},
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)
	require.NoError(t, importExec.Run(ctx))

	assert.Contains(t, stderr.String(), `"rows_imported":2`)
}

func TestSQLiteExecutor_ImportWithTransaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create CSV file
	csvPath := filepath.Join(tmpDir, "data.csv")
	csvContent := "value\n1\n2\n3\n"
	require.NoError(t, os.WriteFile(csvPath, []byte(csvContent), 0o644))

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create table
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "CREATE TABLE numbers (value INTEGER);",
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Import with transaction
	importStep := core.Step{
		Name: "import",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":         dbPath,
				"transaction": true,
				"import": map[string]any{
					"input_file": csvPath,
					"table":      "numbers",
					"has_header": true,
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)
	require.NoError(t, importExec.Run(ctx))

	assert.Contains(t, stderr.String(), `"rows_imported":3`)
}

func TestSQLiteExecutor_ImportDryRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create CSV file
	csvPath := filepath.Join(tmpDir, "data.csv")
	csvContent := "name\nAlice\nBob\n"
	require.NoError(t, os.WriteFile(csvPath, []byte(csvContent), 0o644))

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create table
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "CREATE TABLE users (name TEXT);",
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Import with dry run
	importStep := core.Step{
		Name: "import",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file": csvPath,
					"table":      "users",
					"has_header": true,
					"dry_run":    true,
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)
	require.NoError(t, importExec.Run(ctx))

	// Should show rows imported (in dry run mode, counted but not inserted)
	assert.Contains(t, stderr.String(), `"rows_imported":2`)

	// Verify table is empty (dry run didn't insert)
	verifyStep := core.Step{
		Name: "verify",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "SELECT COUNT(*) as cnt FROM users;",
	}

	verifyExec, err := newSQLiteExecutor(t, ctx, verifyStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	verifyExec.SetStdout(&stdout)
	require.NoError(t, verifyExec.Run(ctx))

	// Count should be 0 because dry run doesn't insert
	assert.Contains(t, stdout.String(), `"cnt":0`)
}

func TestSQLiteExecutor_ImportIgnoreConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create table with unique constraint
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: `
			CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
			INSERT INTO users VALUES (1, 'Existing');
		`,
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Create CSV with conflicting id
	csvPath := filepath.Join(tmpDir, "data.csv")
	csvContent := "id,name\n1,Alice\n2,Bob\n"
	require.NoError(t, os.WriteFile(csvPath, []byte(csvContent), 0o644))

	// Import with ignore
	importStep := core.Step{
		Name: "import",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file":  csvPath,
					"table":       "users",
					"has_header":  true,
					"on_conflict": "ignore",
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)
	require.NoError(t, importExec.Run(ctx))

	// Verify id=1 still has original name (Existing), and id=2 was inserted
	verifyStep := core.Step{
		Name: "verify",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "SELECT * FROM users ORDER BY id;",
	}

	verifyExec, err := newSQLiteExecutor(t, ctx, verifyStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	verifyExec.SetStdout(&stdout)
	require.NoError(t, verifyExec.Run(ctx))

	output := stdout.String()
	assert.Contains(t, output, "Existing") // Original row preserved
	assert.Contains(t, output, "Bob")      // New row inserted
}

func TestSQLiteExecutor_ImportMaxRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create CSV file with many rows
	csvPath := filepath.Join(tmpDir, "data.csv")
	csvContent := "value\n1\n2\n3\n4\n5\n"
	require.NoError(t, os.WriteFile(csvPath, []byte(csvContent), 0o644))

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create table
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "CREATE TABLE numbers (value INTEGER);",
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Import with max_rows limit
	importStep := core.Step{
		Name: "import",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file": csvPath,
					"table":      "numbers",
					"has_header": true,
					"max_rows":   3,
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)
	require.NoError(t, importExec.Run(ctx))

	// Should only import 3 rows
	assert.Contains(t, stderr.String(), `"rows_imported":3`)
}

func TestSQLiteExecutor_ImportSkipRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create CSV file
	csvPath := filepath.Join(tmpDir, "data.csv")
	csvContent := "name\nAlice\nBob\nCharlie\n"
	require.NoError(t, os.WriteFile(csvPath, []byte(csvContent), 0o644))

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create table
	setupStep := core.Step{
		Name: "setup",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "CREATE TABLE users (name TEXT);",
	}

	setupExec, err := newSQLiteExecutor(t, ctx, setupStep)
	require.NoError(t, err)
	require.NoError(t, setupExec.Run(ctx))

	// Import with skip_rows
	importStep := core.Step{
		Name: "import",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
				"import": map[string]any{
					"input_file": csvPath,
					"table":      "users",
					"has_header": true,
					"skip_rows":  1, // Skip first data row (Alice)
				},
			},
		},
	}

	importExec, err := newSQLiteExecutor(t, ctx, importStep)
	require.NoError(t, err)

	var stderr bytes.Buffer
	importExec.SetStderr(&stderr)
	require.NoError(t, importExec.Run(ctx))

	// Should import 2 rows (skipped 1)
	assert.Contains(t, stderr.String(), `"rows_imported":2`)
	assert.Contains(t, stderr.String(), `"rows_skipped":1`)

	// Verify Alice was skipped
	verifyStep := core.Step{
		Name: "verify",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: "SELECT * FROM users ORDER BY name;",
	}

	verifyExec, err := newSQLiteExecutor(t, ctx, verifyStep)
	require.NoError(t, err)

	var stdout bytes.Buffer
	verifyExec.SetStdout(&stdout)
	require.NoError(t, verifyExec.Run(ctx))

	output := stdout.String()
	assert.NotContains(t, output, "Alice")
	assert.Contains(t, output, "Bob")
	assert.Contains(t, output, "Charlie")
}

func TestNewResultWriter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
	}{
		{"jsonl format", "jsonl"},
		{"json format", "json"},
		{"csv format", "csv"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			writer := sqlexec.NewResultWriter(&buf, tt.format, "null", true)
			require.NotNil(t, writer)

			err := writer.WriteHeader([]string{"col1", "col2"})
			require.NoError(t, err)

			err = writer.WriteRow([]any{1, "test"})
			require.NoError(t, err)

			err = writer.Close()
			require.NoError(t, err)

			assert.NotEmpty(t, buf.String())
		})
	}
}

func TestCSVWriter_SpecialCharacters(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewCSVWriter(&buf, "NULL", true)

	err := writer.WriteHeader([]string{"id", "description"})
	require.NoError(t, err)

	// Test with comma in value
	err = writer.WriteRow([]any{1, "Hello, World"})
	require.NoError(t, err)

	// Test with newline in value
	err = writer.WriteRow([]any{2, "Line1\nLine2"})
	require.NoError(t, err)

	// Test with quote in value
	err = writer.WriteRow([]any{3, `Say "Hello"`})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	// CSV should properly escape these
	assert.Contains(t, output, "id,description")
}

func TestJSONLWriter_AllTypes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewJSONLWriter(&buf, "null")

	err := writer.WriteHeader([]string{"int_col", "float_col", "bool_col", "str_col", "nil_col"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{42, 3.14, true, "hello", nil})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, `"int_col":42`)
	assert.Contains(t, output, `"float_col":3.14`)
	assert.Contains(t, output, `"bool_col":true`)
	assert.Contains(t, output, `"str_col":"hello"`)
	assert.Contains(t, output, `"nil_col":null`)
}

func TestSQLiteExecutor_NamedParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Named params work with single queries (command), not multi-statement scripts
	step := core.Step{
		Name: "test-named-params",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": ":memory:",
				"params": map[string]any{
					"value1": 42,
					"value2": 100,
				},
			},
		},
		Commands: []core.CommandEntry{
			{Command: "SELECT :value1 as v1, :value2 as v2"},
		},
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, `"v1":42`)
	assert.Contains(t, output, `"v2":100`)
}

func TestSQLiteExecutor_Command(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	step := core.Step{
		Name: "test-command",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": ":memory:",
			},
		},
		Commands: []core.CommandEntry{
			{Command: "SELECT 1 as result, 'hello' as message"},
		},
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, `"result":1`)
	assert.Contains(t, output, `"message":"hello"`)
}

func TestSQLiteExecutor_NullHandling(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "null_handling.db")

	step := core.Step{
		Name: "test-null-handling",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":         dbPath,
				"null_string": "NULL",
			},
		},
		Script: `
			CREATE TABLE test (id INTEGER, nullable_int INTEGER, nullable_text TEXT);
			INSERT INTO test VALUES (1, NULL, NULL);
			INSERT INTO test VALUES (2, 42, 'hello');
			SELECT * FROM test ORDER BY id;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, `"nullable_int":null`)
	assert.Contains(t, output, `"nullable_text":null`)
	assert.Contains(t, output, `"nullable_int":42`)
	assert.Contains(t, output, `"nullable_text":"hello"`)
}

func TestSQLiteExecutor_Timeout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	step := core.Step{
		Name: "test-timeout",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":     ":memory:",
				"timeout": 30,
			},
		},
		Script: "SELECT 1 as result;",
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, `"result":1`)
}

func TestSQLiteExecutor_Pragma(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	step := core.Step{
		Name: "test-pragma",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": ":memory:",
			},
		},
		Script: "PRAGMA table_info('sqlite_master');",
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// PRAGMA should return table structure info
	output := stdout.String()
	assert.NotEmpty(t, output)
}

func TestSQLiteExecutor_EmptyResult(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	step := core.Step{
		Name: "test-empty-result",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": ":memory:",
			},
		},
		Script: `
			CREATE TABLE empty_table (id INTEGER);
			SELECT * FROM empty_table;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// Empty result should not have any rows
	output := stdout.String()
	assert.Empty(t, output)
}

func TestSQLiteExecutor_InsertReturnsAffected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "insert_affected.db")

	step := core.Step{
		Name: "test-insert",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": dbPath,
			},
		},
		Script: `
			CREATE TABLE test (id INTEGER PRIMARY KEY);
			INSERT INTO test VALUES (1), (2), (3);
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// Metrics should show rows_affected
	metrics := stderr.String()
	assert.Contains(t, metrics, `"rows_affected":3`)
}

// --- Connection Manager Tests ---

func TestConnectionManager_NewAndClose(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn":             ":memory:",
		"maxOpenConns":    5,
		"maxIdleConns":    2,
		"connMaxLifetime": 300,
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	require.NotNil(t, driver)

	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)
	require.NotNil(t, cm)

	// Verify accessors
	assert.NotNil(t, cm.DB())
	assert.NotNil(t, cm.Driver())
	assert.NotNil(t, cm.Config())
	assert.Equal(t, "sqlite", cm.Driver().Name())

	// Close should succeed
	err = cm.Close()
	assert.NoError(t, err)
}

func TestConnectionManager_RefCounting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn": ":memory:",
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	require.NotNil(t, driver)

	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)

	// Initial refCount is 1

	// Acquire twice (refCount = 3)
	cm.Acquire()
	cm.Acquire()

	// First release (refCount = 2)
	err = cm.Release()
	assert.NoError(t, err)

	// Connection should still be usable
	_, err = cm.DB().ExecContext(ctx, "SELECT 1")
	assert.NoError(t, err)

	// Second release (refCount = 1)
	err = cm.Release()
	assert.NoError(t, err)

	// Still usable
	_, err = cm.DB().ExecContext(ctx, "SELECT 1")
	assert.NoError(t, err)

	// Third release (refCount = 0) - connection closed
	err = cm.Release()
	assert.NoError(t, err)

	// Connection should now be closed
	err = cm.DB().PingContext(ctx)
	assert.Error(t, err) // Should fail because connection is closed
}

func TestConnectionManager_AcquireRelease_Concurrent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn": ":memory:",
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)

	// Simulate concurrent acquire/release
	var wg sync.WaitGroup
	numGoroutines := 10

	for range numGoroutines {
		wg.Go(func() {
			cm.Acquire()
			// Intentional sleep to simulate concurrent work under contention.
			time.Sleep(10 * time.Millisecond)
			_ = cm.Release()
		})
	}

	wg.Wait()

	// Final release for the initial refCount
	err = cm.Release()
	assert.NoError(t, err)
}

func TestTransaction_BeginCommitRollback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "transaction.db")

	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn": dbPath,
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)
	defer cm.Close()

	db := cm.DB()

	// Create a table
	_, err = db.ExecContext(ctx, "CREATE TABLE test (id INTEGER PRIMARY KEY, value INTEGER)")
	require.NoError(t, err)

	// Test commit
	t.Run("commit", func(t *testing.T) {
		tx, err := sqlexec.BeginTransaction(ctx, db, "")
		require.NoError(t, err)
		require.NotNil(t, tx)
		require.NotNil(t, tx.Tx())

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO test (value) VALUES (100)")
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify committed
		var value int
		err = db.QueryRowContext(ctx, "SELECT value FROM test WHERE value = 100").Scan(&value)
		assert.NoError(t, err)
		assert.Equal(t, 100, value)
	})

	// Test rollback
	t.Run("rollback", func(t *testing.T) {
		tx, err := sqlexec.BeginTransaction(ctx, db, "")
		require.NoError(t, err)

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO test (value) VALUES (999)")
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify rolled back
		var count int
		err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM test WHERE value = 999").Scan(&count)
		assert.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestTransaction_IsolationLevels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn": ":memory:",
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)
	defer cm.Close()

	db := cm.DB()

	tests := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{"empty", "", false},
		{"default", "default", false},
		{"read_committed", "read_committed", false},
		{"repeatable_read", "repeatable_read", false},
		{"serializable", "serializable", false},
		{"invalid", "invalid_level", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := sqlexec.BeginTransaction(ctx, db, tt.level)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, tx)

			// Clean up
			_ = tx.Rollback()
		})
	}
}

func TestGetQueryExecutor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn": ":memory:",
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)
	defer cm.Close()

	db := cm.DB()

	t.Run("without transaction", func(t *testing.T) {
		qe := sqlexec.GetQueryExecutor(db, nil)
		require.NotNil(t, qe)

		// Should be able to execute queries
		rows, err := qe.QueryContext(ctx, "SELECT 1")
		require.NoError(t, err)
		rows.Close()
	})

	t.Run("with transaction", func(t *testing.T) {
		tx, err := sqlexec.BeginTransaction(ctx, db, "")
		require.NoError(t, err)

		qe := sqlexec.GetQueryExecutor(db, tx)
		require.NotNil(t, qe)

		// Should be able to execute queries
		rows, err := qe.QueryContext(ctx, "SELECT 1")
		require.NoError(t, err)
		rows.Close()

		_ = tx.Rollback()
	})
}

func TestConnectionManager_FixedConnectionPoolDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Connection pool settings are now fixed defaults (not configurable per-step)
	// In non-worker mode: 1 connection per step
	// In worker mode: global pool manager handles pooling
	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn": ":memory:",
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)
	defer cm.Close()

	// Verify db stats show fixed defaults are applied
	stats := cm.DB().Stats()
	assert.Equal(t, 1, stats.MaxOpenConnections, "should use fixed default of 1 max open connection")

	// Connection should be usable
	_, err = cm.DB().ExecContext(ctx, "SELECT 1")
	assert.NoError(t, err)
}

func TestConnectionManager_DoubleClose(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn": ":memory:",
	})
	require.NoError(t, err)

	driver, ok := sqlexec.GetDriver("sqlite")
	require.True(t, ok)
	cm, err := sqlexec.NewConnectionManager(ctx, driver, cfg)
	require.NoError(t, err)

	// First close
	err = cm.Close()
	assert.NoError(t, err)

	// Second close - should not panic
	// Note: SQLite in-memory DB may not return error on double close
	// The important thing is that it doesn't panic
	_ = cm.Close()
}

// --- Additional Coverage Tests for Result Writers and Params ---

func TestConvertPositionalParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		params      []any
		placeholder string
		wantErr     bool
	}{
		{
			name:        "nil params",
			query:       "SELECT * FROM users",
			params:      nil,
			placeholder: "?",
			wantErr:     false,
		},
		{
			name:        "correct count with ?",
			query:       "SELECT * FROM users WHERE id = ? AND name = ?",
			params:      []any{1, "Alice"},
			placeholder: "?",
			wantErr:     false,
		},
		{
			name:        "correct count with $N",
			query:       "SELECT * FROM users WHERE id = $1 AND name = $2",
			params:      []any{1, "Alice"},
			placeholder: "$",
			wantErr:     false,
		},
		{
			name:        "mismatch count with ?",
			query:       "SELECT * FROM users WHERE id = ?",
			params:      []any{1, 2}, // Too many params
			placeholder: "?",
			wantErr:     true,
		},
		{
			name:        "mismatch count with $N",
			query:       "SELECT * FROM users WHERE id = $1 AND name = $2",
			params:      []any{1}, // Not enough params
			placeholder: "$",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := sqlexec.ConvertPositionalParams(tt.query, tt.params, tt.placeholder)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.params != nil {
				assert.Equal(t, tt.params, result)
			}
		})
	}
}

func TestResultWriter_JSONTypes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewJSONLWriter(&buf, "null")

	err := writer.WriteHeader([]string{"bytes", "time", "null_str", "null_int", "null_float", "null_bool", "null_time"})
	require.NoError(t, err)

	// Test all type branches in convertValue
	testTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	err = writer.WriteRow([]any{
		[]byte("byte data"),
		testTime,
		sqlNullString("valid string"),
		sqlNullInt64(42),
		sqlNullFloat64(3.14),
		sqlNullBool(true),
		sqlNullTime(testTime),
	})
	require.NoError(t, err)

	// Test invalid (null) values for sql.Null types
	err = writer.WriteRow([]any{
		nil,
		nil,
		sqlNullStringInvalid(),
		sqlNullInt64Invalid(),
		sqlNullFloat64Invalid(),
		sqlNullBoolInvalid(),
		sqlNullTimeInvalid(),
	})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, `"bytes":"byte data"`)
	assert.Contains(t, output, `"time":"2024-01-15T10:30:00Z"`)
	assert.Contains(t, output, `"null_str":"valid string"`)
	assert.Contains(t, output, `"null_int":42`)
	assert.Contains(t, output, `"null_float":3.14`)
	assert.Contains(t, output, `"null_bool":true`)
	assert.Contains(t, output, `:null`)
}

func TestResultWriter_CSVTypes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewCSVWriter(&buf, "NULL", true)

	err := writer.WriteHeader([]string{"int", "int64", "float", "bool", "time", "bytes", "str"})
	require.NoError(t, err)

	testTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	err = writer.WriteRow([]any{
		int(42),
		int64(123456),
		float64(3.14159),
		true,
		testTime,
		[]byte("bytes"),
		"string",
	})
	require.NoError(t, err)

	// Test sql.Null* types for CSV
	err = writer.WriteRow([]any{
		sqlNullInt64(99),
		sqlNullInt64Invalid(),
		sqlNullFloat64(2.71),
		sqlNullBool(false),
		sqlNullTime(testTime),
		sqlNullString("null str"),
		sqlNullStringInvalid(),
	})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "123456")
	assert.Contains(t, output, "3.14159")
	assert.Contains(t, output, "true")
	assert.Contains(t, output, "NULL")
}

func TestResultWriter_JSONWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writer := sqlexec.NewJSONWriter(&buf, "null")

	err := writer.WriteHeader([]string{"id", "name"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{1, "Alice"})
	require.NoError(t, err)

	err = writer.WriteRow([]any{2, "Bob"})
	require.NoError(t, err)

	err = writer.Close()
	require.NoError(t, err)

	output := buf.String()
	// JSON array format (pretty printed with spaces)
	assert.Contains(t, output, "[")
	assert.Contains(t, output, "]")
	assert.Contains(t, output, `"id": 1`)
	assert.Contains(t, output, `"name": "Alice"`)
}

func TestSanitizeIdentifier_Extended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		identifier string
		wantErr    bool
	}{
		{"valid simple", "users", false},
		{"valid with underscore", "user_table", false},
		{"valid with dot", "schema.table", false},
		{"valid mixed case", "UserTable", false},
		{"valid alphanumeric", "user123", false},
		{"empty string", "", true},
		{"starts with digit", "123table", true},
		{"contains space", "user table", true},
		{"contains hyphen", "user-table", true},
		{"contains semicolon", "users;", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := sqlexec.SanitizeIdentifier(tt.identifier)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.identifier, result)
		})
	}
}

func TestGetPositionalParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Test with positional params
	cfg, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn":    ":memory:",
		"params": []any{1, 2, 3},
	})
	require.NoError(t, err)

	params, ok := cfg.GetPositionalParams()
	assert.True(t, ok)
	assert.Equal(t, []any{1, 2, 3}, params)

	// Test when params is not positional
	cfg2, err := sqlexec.ParseConfig(ctx, map[string]any{
		"dsn":    ":memory:",
		"params": map[string]any{"id": 1},
	})
	require.NoError(t, err)

	_, ok = cfg2.GetPositionalParams()
	assert.False(t, ok)
}

// Helper functions to create sql.Null* types
func sqlNullString(s string) any {
	return sql.NullString{String: s, Valid: true}
}

func sqlNullStringInvalid() any {
	return sql.NullString{Valid: false}
}

func sqlNullInt64(i int64) any {
	return sql.NullInt64{Int64: i, Valid: true}
}

func sqlNullInt64Invalid() any {
	return sql.NullInt64{Valid: false}
}

func sqlNullFloat64(f float64) any {
	return sql.NullFloat64{Float64: f, Valid: true}
}

func sqlNullFloat64Invalid() any {
	return sql.NullFloat64{Valid: false}
}

func sqlNullBool(b bool) any {
	return sql.NullBool{Bool: b, Valid: true}
}

func sqlNullBoolInvalid() any {
	return sql.NullBool{Valid: false}
}

func sqlNullTime(t time.Time) any {
	return sql.NullTime{Time: t, Valid: true}
}

func sqlNullTimeInvalid() any {
	return sql.NullTime{Valid: false}
}

// TestSQLiteExecutor_StreamingOutput tests streaming query results to a file.
func TestSQLiteExecutor_StreamingOutput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "output.jsonl")

	step := core.Step{
		Name: "test-streaming",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":           ":memory:",
				"streaming":     true,
				"output_file":   outputFile,
				"output_format": "jsonl",
			},
		},
		Script: `
			CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
			INSERT INTO users (name) VALUES ('Alice'), ('Bob'), ('Charlie');
			SELECT * FROM users ORDER BY id;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// Verify output file was created and contains data
	content, err := os.ReadFile(outputFile)
	require.NoError(t, err)

	output := string(content)
	assert.Contains(t, output, `"name":"Alice"`)
	assert.Contains(t, output, `"name":"Bob"`)
	assert.Contains(t, output, `"name":"Charlie"`)
}

// TestSQLiteExecutor_StreamingOutputCSV tests streaming CSV output to a file.
func TestSQLiteExecutor_StreamingOutputCSV(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	outputFile := filepath.Join(tmpDir, "output.csv")

	step := core.Step{
		Name: "test-streaming-csv",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":           ":memory:",
				"streaming":     true,
				"output_file":   outputFile,
				"output_format": "csv",
				"headers":       true,
			},
		},
		Script: `
			CREATE TABLE data (id INTEGER, value TEXT);
			INSERT INTO data VALUES (1, 'one'), (2, 'two');
			SELECT * FROM data ORDER BY id;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	err = exec.Run(ctx)
	require.NoError(t, err)

	content, err := os.ReadFile(outputFile)
	require.NoError(t, err)

	output := string(content)
	assert.Contains(t, output, "id,value")
	assert.Contains(t, output, "1,one")
	assert.Contains(t, output, "2,two")
}

// TestSQLiteExecutor_StreamingOutputSubdir tests streaming to a file in a subdirectory.
func TestSQLiteExecutor_StreamingOutputSubdir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	// Create path in non-existent subdirectory
	outputFile := filepath.Join(tmpDir, "subdir", "nested", "output.jsonl")

	step := core.Step{
		Name: "test-streaming-subdir",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":           ":memory:",
				"streaming":     true,
				"output_file":   outputFile,
				"output_format": "jsonl",
			},
		},
		Script: `SELECT 1 as value;`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// Verify subdirectories were created and file exists
	content, err := os.ReadFile(outputFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), `"value":1`)
}

// TestSQLiteExecutor_FileLock tests file locking for exclusive access.
func TestSQLiteExecutor_FileLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "locked.db")

	step := core.Step{
		Name: "test-filelock",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":       dbPath,
				"file_lock": true,
			},
		},
		Script: `
			CREATE TABLE test (id INTEGER PRIMARY KEY);
			INSERT INTO test VALUES (1);
			SELECT * FROM test;
		`,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// Verify data was written (lock was acquired and released successfully)
	assert.Contains(t, stdout.String(), `"id":1`)

	// The lock file may or may not exist after release (flock doesn't delete the file,
	// it just releases the lock). What matters is that the execution succeeded.
}

// TestSQLiteExecutor_SharedMemory tests shared memory mode for in-memory databases.
func TestSQLiteExecutor_SharedMemory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tableName := fmt.Sprintf("shared_test_%d", time.Now().UnixNano())

	// First step creates the table with shared memory
	step1 := core.Step{
		Name: "test-shared-memory-create",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn":           ":memory:",
				"shared_memory": true,
			},
		},
		Script: fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, value TEXT);
			DELETE FROM %s;
			INSERT INTO %s VALUES (1, 'shared');
		`, tableName, tableName, tableName),
	}

	exec1, err := newSQLiteExecutor(t, ctx, step1)
	require.NoError(t, err)

	err = exec1.Run(ctx)
	require.NoError(t, err)

	// Note: In a real DAG scenario, multiple steps would share the connection.
	// This test verifies the shared_memory config is properly processed.
}

// TestSQLiteExecutor_ScriptFile tests reading SQL from a file using file:// prefix.
func TestSQLiteExecutor_ScriptFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create a SQL script file
	scriptFile := filepath.Join(tmpDir, "script.sql")
	scriptContent := `
		CREATE TABLE from_file (id INTEGER, name TEXT);
		INSERT INTO from_file VALUES (1, 'from file');
		SELECT * FROM from_file;
	`
	err := os.WriteFile(scriptFile, []byte(scriptContent), 0644)
	require.NoError(t, err)

	step := core.Step{
		Name: "test-script-file",
		ExecutorConfig: core.ExecutorConfig{
			Type: "sqlite",
			Config: map[string]any{
				"dsn": ":memory:",
			},
		},
		// Use file:// prefix to load SQL from external file
		Script: "file://" + scriptFile,
	}

	exec, err := newSQLiteExecutor(t, ctx, step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), `"name":"from file"`)
}

// TestSplitStatements tests the SQL statement splitting function.
func TestSplitStatements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single statement",
			input:    "SELECT 1",
			expected: []string{"SELECT 1"},
		},
		{
			name:     "multiple statements",
			input:    "SELECT 1; SELECT 2; SELECT 3",
			expected: []string{"SELECT 1", "SELECT 2", "SELECT 3"},
		},
		{
			name:     "statement with string containing semicolon",
			input:    "INSERT INTO t VALUES ('a;b'); SELECT 1",
			expected: []string{"INSERT INTO t VALUES ('a;b')", "SELECT 1"},
		},
		{
			name:     "statement with double-quoted string",
			input:    `INSERT INTO t VALUES ("test;value"); SELECT 2`,
			expected: []string{`INSERT INTO t VALUES ("test;value")`, "SELECT 2"},
		},
		{
			name:     "empty statements filtered",
			input:    "SELECT 1;; ; SELECT 2",
			expected: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:     "multiline script",
			input:    "CREATE TABLE t (id INT);\nINSERT INTO t VALUES (1);\nSELECT * FROM t;",
			expected: []string{"CREATE TABLE t (id INT)", "INSERT INTO t VALUES (1)", "SELECT * FROM t"},
		},
		{
			name:     "postgres dollar-quoted string",
			input:    "SELECT $tag$hello;world$tag$; SELECT 1",
			expected: []string{"SELECT $tag$hello;world$tag$", "SELECT 1"},
		},
		{
			name:     "escaped single quote",
			input:    "INSERT INTO t VALUES ('it''s a test'); SELECT 1",
			expected: []string{"INSERT INTO t VALUES ('it''s a test')", "SELECT 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sqlexec.SplitStatements(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsSelectQuery tests the SELECT query detection function.
func TestIsSelectQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query    string
		expected bool
	}{
		{"SELECT * FROM users", true},
		{"  SELECT id FROM users", true},
		{"\n\tSELECT 1", true},
		{"select lower", true},
		{"INSERT INTO users VALUES (1)", false},
		{"UPDATE users SET name = 'test'", false},
		{"DELETE FROM users", false},
		{"CREATE TABLE users (id INT)", false},
		{"INSERT INTO users VALUES (1) RETURNING id", true},
		{"INSERT INTO users VALUES (1) RETURNING", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"  WITH RECURSIVE cte AS (SELECT 1) SELECT * FROM cte", true},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()
			result := sqlexec.IsSelectQuery(tt.query)
			assert.Equal(t, tt.expected, result, "query: %s", tt.query)
		})
	}
}
