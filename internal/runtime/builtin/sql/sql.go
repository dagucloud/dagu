// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sql provides SQL executor capabilities for PostgreSQL and SQLite databases.
package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime/executor"
)

// File system constants
const defaultDirPermissions = 0o755

var (
	_ executor.Executor = (*sqlExecutor)(nil)
	_ io.Closer         = (*sqlExecutor)(nil)
)

// sqlExecutor implements the Executor interface for SQL operations.
type sqlExecutor struct {
	mu              sync.Mutex
	step            core.Step
	cfg             *Config
	driver          Driver
	connMgr         *ConnectionManager
	poolManager     *GlobalPoolManager // Reference for cleanup in worker mode
	stdout          io.Writer
	stderr          io.Writer
	cancelFunc      context.CancelFunc
	advisoryRelease func() error
	useGlobalPool   bool // true if using global pool manager (shared-nothing mode)
	closed          bool // guard against double-close
}

// ExecutionMetrics holds metrics from SQL execution.
type ExecutionMetrics struct {
	QueryHash    string    `json:"query_hash"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	DurationMs   int64     `json:"duration_ms"`
	RowsAffected int64     `json:"rows_affected,omitempty"`
	RowsReturned int64     `json:"rows_returned,omitempty"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
}

// newSQLExecutor creates a new SQL executor for the given driver type.
func newSQLExecutor(ctx context.Context, step core.Step, driverName string) (executor.Executor, error) {
	driver, ok := GetDriver(driverName)
	if !ok {
		return nil, fmt.Errorf("sql driver %q not found", driverName)
	}

	cfg, err := ParseConfig(ctx, step.ExecutorConfig.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sql config: %w", err)
	}

	// Check for global pool manager (shared-nothing mode)
	var connMgr *ConnectionManager
	var poolManager *GlobalPoolManager
	var useGlobalPool bool

	if pm := GetPoolManager(ctx); pm != nil && driverName == "postgres" {
		// Use global pool for PostgreSQL in shared-nothing mode
		db, err := pm.GetOrCreatePool(ctx, driver, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to get pool from global manager: %w", err)
		}
		// Create a connection manager that wraps the global pool's db
		// We don't set cleanup since the global pool manages the lifecycle
		connMgr = &ConnectionManager{
			db:       db,
			driver:   driver,
			cfg:      cfg,
			refCount: 1,
		}
		poolManager = pm
		useGlobalPool = true
	} else {
		// Use per-step connection manager (non-distributed mode or SQLite)
		connMgr, err = NewConnectionManager(ctx, driver, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create connection manager: %w", err)
		}
	}

	return &sqlExecutor{
		step:          step,
		cfg:           cfg,
		driver:        driver,
		connMgr:       connMgr,
		poolManager:   poolManager,
		useGlobalPool: useGlobalPool,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
	}, nil
}

// newPostgresExecutor creates a new PostgreSQL executor.
func newPostgresExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	return newSQLExecutor(ctx, step, "postgres")
}

// newSQLiteExecutor creates a new SQLite executor.
func newSQLiteExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	return newSQLExecutor(ctx, step, "sqlite")
}

// newDuckDBExecutor creates a new DuckDB executor.
func newDuckDBExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	return newSQLExecutor(ctx, step, "duckdb")
}

// SetStdout sets the stdout writer.
func (e *sqlExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

// SetStderr sets the stderr writer.
func (e *sqlExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

// Kill cancels the execution.
func (e *sqlExecutor) Kill(_ os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cancelFunc != nil {
		e.cancelFunc()
	}
	return nil
}

// Close releases resources held by the SQL executor.
// In worker mode (global pool), releases the connection back to the pool.
// In non-worker mode, closes the connection.
// Implements io.Closer.
func (e *sqlExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Guard against double-close
	if e.closed {
		return nil
	}
	e.closed = true

	if e.connMgr == nil {
		return nil
	}

	var err error

	if e.useGlobalPool && e.poolManager != nil {
		// Worker mode: release connection back to pool
		// The pool manages the actual connection lifecycle
		e.poolManager.ReleasePool(e.cfg.DSN)
		// Don't close the connection - pool owns it
	} else {
		// Non-worker mode: close the connection
		err = e.connMgr.Close()
	}

	// Clear references to prevent misuse
	e.connMgr = nil
	e.poolManager = nil
	return err
}

// Run executes the SQL query or script.
func (e *sqlExecutor) Run(ctx context.Context) error {
	// Create cancellable context
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancelFunc = cancel
	e.mu.Unlock()
	defer cancel()

	// Apply timeout if configured
	if e.cfg.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, time.Duration(e.cfg.Timeout)*time.Second)
		defer timeoutCancel()
	}

	// Acquire advisory lock if configured
	if e.cfg.AdvisoryLock != "" && e.driver.SupportsAdvisoryLock() {
		release, err := e.driver.AcquireAdvisoryLock(ctx, e.connMgr.DB(), e.cfg.AdvisoryLock)
		if err != nil {
			return err
		}
		e.advisoryRelease = release
		defer func() {
			if e.advisoryRelease != nil {
				if err := e.advisoryRelease(); err != nil {
					logger.Warn(ctx, "failed to release advisory lock", tag.Error(err))
				}
			}
		}()
	}

	// Check if this is an import operation
	if e.cfg.Import != nil {
		return e.executeImport(ctx)
	}

	// Get the query to execute
	query, err := e.getQuery()
	if err != nil {
		return err
	}

	// Execute based on whether it's a script or single query
	if e.step.Script != "" {
		return e.executeScript(ctx, query)
	}
	return e.executeQuery(ctx, query)
}

// executeImport handles data import from CSV/TSV/JSONL files.
func (e *sqlExecutor) executeImport(ctx context.Context) error {
	db := e.connMgr.DB()
	var tx *Transaction
	var err error

	// Begin transaction if configured
	if e.cfg.Transaction {
		tx, err = BeginTransaction(ctx, db, e.cfg.IsolationLevel)
		if err != nil {
			return err
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback()
			}
		}()
	}

	// Create importer with optional transaction
	var sqlTx *sql.Tx
	if tx != nil {
		sqlTx = tx.Tx()
	}
	importer := NewImporter(db, sqlTx, e.driver, e.cfg.Import)

	// Execute import
	metrics, err := importer.Import(ctx)

	// Write metrics to stderr
	e.writeImportMetrics(metrics)

	if err != nil {
		return err
	}

	// Commit transaction if we started one
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
		tx = nil
	}

	return nil
}

// writeImportMetrics writes import metrics to stderr.
func (e *sqlExecutor) writeImportMetrics(metrics *ImportMetrics) {
	if metrics == nil {
		return
	}
	data, err := json.Marshal(metrics)
	if err != nil {
		return
	}
	_, _ = e.stderr.Write(append(data, '\n'))
}

// getQuery extracts the SQL query from the step configuration.
func (e *sqlExecutor) getQuery() (string, error) {
	// Check for script content
	if e.step.Script != "" {
		// Handle file:// prefix for external SQL files
		if after, ok := strings.CutPrefix(e.step.Script, "file://"); ok {
			filePath := after
			content, err := os.ReadFile(filePath)
			if err != nil {
				return "", fmt.Errorf("failed to read sql file %q: %w", filePath, err)
			}
			return string(content), nil
		}
		return e.step.Script, nil
	}

	// Check for command - prefer CmdWithArgs (full command string) over Command
	// (which may be split by cmdutil.SplitCommand for shell commands)
	if len(e.step.Commands) > 0 {
		cmd := e.step.Commands[0]
		if cmd.CmdWithArgs != "" {
			return cmd.CmdWithArgs, nil
		}
		if cmd.Command != "" {
			return cmd.Command, nil
		}
	}

	return "", fmt.Errorf("no sql query provided")
}

// executeQuery executes a single SQL query.
func (e *sqlExecutor) executeQuery(ctx context.Context, query string) error {
	metrics := &ExecutionMetrics{
		QueryHash: hashQuery(query),
		StartedAt: time.Now(),
	}

	defer func() {
		metrics.FinishedAt = time.Now()
		metrics.DurationMs = metrics.FinishedAt.Sub(metrics.StartedAt).Milliseconds()
		e.writeMetrics(metrics)
	}()

	// Prepare parameters
	convertedQuery, params, err := PrepareParams(query, e.cfg, e.driver)
	if err != nil {
		metrics.Status = "error"
		metrics.Error = err.Error()
		return err
	}

	db := e.connMgr.DB()
	var tx *Transaction

	// Begin transaction if configured
	if e.cfg.Transaction {
		tx, err = BeginTransaction(ctx, db, e.cfg.IsolationLevel)
		if err != nil {
			metrics.Status = "error"
			metrics.Error = err.Error()
			return err
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback()
			}
		}()
	}

	qe := GetQueryExecutor(db, tx)

	// Determine if query returns rows
	if isSelectQuery(convertedQuery) {
		rowCount, err := e.executeSelectQuery(ctx, qe, convertedQuery, params)
		if err != nil {
			metrics.Status = "error"
			metrics.Error = err.Error()
			return err
		}
		metrics.RowsReturned = rowCount
	} else {
		affected, err := e.executeExecQuery(ctx, qe, convertedQuery, params)
		if err != nil {
			metrics.Status = "error"
			metrics.Error = err.Error()
			return err
		}
		metrics.RowsAffected = affected
	}

	// Commit transaction if we started one
	if tx != nil {
		if err := tx.Commit(); err != nil {
			metrics.Status = "error"
			metrics.Error = err.Error()
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
		tx = nil
	}

	metrics.Status = "success"
	return nil
}

// executeScript executes a SQL script with multiple statements.
func (e *sqlExecutor) executeScript(ctx context.Context, script string) error {
	statements := splitStatements(script)
	if len(statements) == 0 {
		return nil
	}

	db := e.connMgr.DB()
	var tx *Transaction
	var err error

	// Begin transaction if configured
	if e.cfg.Transaction {
		tx, err = BeginTransaction(ctx, db, e.cfg.IsolationLevel)
		if err != nil {
			return err
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback()
			}
		}()
	}

	qe := GetQueryExecutor(db, tx)

	for i, stmt := range statements {
		if err := e.executeStatement(ctx, qe, stmt, i+1); err != nil {
			return err
		}
	}

	// Commit transaction if we started one
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
		tx = nil
	}

	return nil
}

// executeStatement executes a single statement with metrics tracking.
func (e *sqlExecutor) executeStatement(ctx context.Context, qe QueryExecutor, stmt string, stmtNum int) error {
	metrics := &ExecutionMetrics{
		QueryHash: hashQuery(stmt),
		StartedAt: time.Now(),
	}

	defer func() {
		metrics.FinishedAt = time.Now()
		metrics.DurationMs = metrics.FinishedAt.Sub(metrics.StartedAt).Milliseconds()
		e.writeMetrics(metrics)
	}()

	convertedStmt, params, err := PrepareParams(stmt, e.cfg, e.driver)
	if err != nil {
		metrics.Status = "error"
		metrics.Error = err.Error()
		return err
	}

	if isSelectQuery(convertedStmt) {
		rowCount, err := e.executeSelectQuery(ctx, qe, convertedStmt, params)
		if err != nil {
			metrics.Status = "error"
			metrics.Error = err.Error()
			return fmt.Errorf("statement %d failed: %w", stmtNum, err)
		}
		metrics.RowsReturned = rowCount
	} else {
		affected, err := e.executeExecQuery(ctx, qe, convertedStmt, params)
		if err != nil {
			metrics.Status = "error"
			metrics.Error = err.Error()
			return fmt.Errorf("statement %d failed: %w", stmtNum, err)
		}
		metrics.RowsAffected = affected
	}

	metrics.Status = "success"
	return nil
}

// executeSelectQuery executes a SELECT query and writes results.
func (e *sqlExecutor) executeSelectQuery(ctx context.Context, qe QueryExecutor, query string, params []any) (rowCount int64, err error) {
	rows, err := qe.QueryContext(ctx, query, params...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("failed to get columns: %w", err)
	}

	// Determine output destination
	var output io.Writer = e.stdout
	var outputFile *os.File

	if e.cfg.Streaming && e.cfg.OutputFile != "" {
		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(e.cfg.OutputFile), defaultDirPermissions); err != nil {
			return 0, fmt.Errorf("failed to create output directory: %w", err)
		}

		// Write to temp file first for atomic operation
		tmpFile := e.cfg.OutputFile + ".tmp"
		outputFile, err = os.Create(tmpFile)
		if err != nil {
			return 0, fmt.Errorf("failed to create output file: %w", err)
		}
		defer func() {
			// Close the file first
			closeErr := outputFile.Close()
			if closeErr != nil {
				// Only set error if no previous error
				if err == nil {
					err = fmt.Errorf("failed to close output file: %w", closeErr)
				}
				// Try to remove temp file on close failure
				_ = os.Remove(tmpFile)
				return
			}
			// Atomic rename only if close succeeded
			if renameErr := os.Rename(tmpFile, e.cfg.OutputFile); renameErr != nil {
				if err == nil {
					err = fmt.Errorf("failed to rename output file: %w", renameErr)
				}
				// Try to remove temp file on rename failure
				_ = os.Remove(tmpFile)
			}
		}()
		output = outputFile
	}

	writer := NewResultWriter(output, e.cfg.OutputFormat, e.cfg.NullString, e.cfg.Headers)
	if err := writer.WriteHeader(columns); err != nil {
		return 0, fmt.Errorf("failed to write header: %w", err)
	}

	for rows.Next() {
		if e.cfg.MaxRows > 0 && rowCount >= int64(e.cfg.MaxRows) {
			break
		}

		values, err := ScanRow(rows, columns)
		if err != nil {
			return rowCount, fmt.Errorf("failed to scan row: %w", err)
		}

		if err := writer.WriteRow(values); err != nil {
			return rowCount, fmt.Errorf("failed to write row: %w", err)
		}

		rowCount++
	}

	if err := rows.Err(); err != nil {
		return rowCount, fmt.Errorf("row iteration error: %w", err)
	}

	if err := writer.Flush(); err != nil {
		return rowCount, fmt.Errorf("failed to flush writer: %w", err)
	}

	if err := writer.Close(); err != nil {
		return rowCount, fmt.Errorf("failed to close writer: %w", err)
	}

	return rowCount, nil
}

// executeExecQuery executes a non-SELECT query (INSERT, UPDATE, DELETE, etc.).
func (e *sqlExecutor) executeExecQuery(ctx context.Context, qe QueryExecutor, query string, params []any) (int64, error) {
	result, err := qe.ExecContext(ctx, query, params...)
	if err != nil {
		return 0, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		// Some drivers don't support RowsAffected, log and continue
		logger.Debug(ctx, "failed to get rows affected (driver may not support it)", tag.Error(err))
		return 0, nil
	}
	return affected, nil
}

// writeMetrics writes execution metrics to stderr.
func (e *sqlExecutor) writeMetrics(metrics *ExecutionMetrics) {
	data, err := json.Marshal(metrics)
	if err != nil {
		return
	}
	_, _ = e.stderr.Write(append(data, '\n'))
}

// isSelectQuery determines if a query is a SELECT statement.
func isSelectQuery(query string) bool {
	trimmed := strings.TrimSpace(strings.ToUpper(query))

	// Check for RETURNING clause (PostgreSQL/SQLite)
	// INSERT/UPDATE/DELETE with RETURNING returns rows like SELECT
	if strings.Contains(trimmed, " RETURNING ") || strings.HasSuffix(trimmed, " RETURNING") {
		return true
	}

	// Standard select-like statements
	return strings.HasPrefix(trimmed, "SELECT") ||
		strings.HasPrefix(trimmed, "WITH") ||
		strings.HasPrefix(trimmed, "TABLE") ||
		strings.HasPrefix(trimmed, "VALUES") ||
		strings.HasPrefix(trimmed, "PRAGMA")
}

// splitStatements splits a SQL script into individual statements.
func splitStatements(script string) []string {
	var statements []string
	var current strings.Builder
	inString := false
	stringChar := rune(0)
	inDollarQuote := false
	dollarTag := ""

	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// Handle dollar-quoted strings (PostgreSQL specific)
		if !inString && r == '$' {
			tagEnd := i + 1
			for tagEnd < len(runes) && (runes[tagEnd] == '_' || (runes[tagEnd] >= 'a' && runes[tagEnd] <= 'z') || (runes[tagEnd] >= 'A' && runes[tagEnd] <= 'Z') || (runes[tagEnd] >= '0' && runes[tagEnd] <= '9')) {
				tagEnd++
			}
			if tagEnd < len(runes) && runes[tagEnd] == '$' {
				tag := string(runes[i : tagEnd+1])
				if inDollarQuote && tag == dollarTag {
					inDollarQuote = false
					dollarTag = ""
				} else if !inDollarQuote {
					inDollarQuote = true
					dollarTag = tag
				}
				current.WriteString(tag)
				i = tagEnd
				continue
			}
		}

		if inDollarQuote {
			current.WriteRune(r)
			continue
		}

		if (r == '\'' || r == '"') && !inString {
			inString = true
			stringChar = r
			current.WriteRune(r)
			continue
		}

		if inString {
			current.WriteRune(r)
			if r == stringChar {
				if i+1 < len(runes) && runes[i+1] == stringChar {
					current.WriteRune(runes[i+1])
					i++
				} else {
					inString = false
				}
			}
			continue
		}

		if r == ';' {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}

		current.WriteRune(r)
	}

	stmt := strings.TrimSpace(current.String())
	if stmt != "" {
		statements = append(statements, stmt)
	}

	return statements
}

// hashQuery creates a hash of the query for metrics.
func hashQuery(query string) string {
	// Simple hash for identification, not security
	var h uint64
	for _, c := range query {
		h = h*31 + uint64(c)
	}
	return fmt.Sprintf("%016x", h)
}

func init() {
	// Register PostgreSQL executor
	executor.RegisterExecutor(
		"postgres",
		newPostgresExecutor,
		nil,
		core.ExecutorCapabilities{Command: true, Script: true},
	)

	// Register SQLite executor
	executor.RegisterExecutor(
		"sqlite",
		newSQLiteExecutor,
		nil,
		core.ExecutorCapabilities{Command: true, Script: true},
	)

	// Register DuckDB executor
	executor.RegisterExecutor(
		"duckdb",
		newDuckDBExecutor,
		nil,
		core.ExecutorCapabilities{Command: true, Script: true},
	)
}
