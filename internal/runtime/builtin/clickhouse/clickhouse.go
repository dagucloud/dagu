// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

const executorType = "clickhouse"

var (
	_ executor.Executor = (*clickhouseExecutor)(nil)
	_ io.Closer         = (*clickhouseExecutor)(nil)
)

// clickhouseExecutor runs the statements of a step against ClickHouse.
type clickhouseExecutor struct {
	mu         sync.Mutex
	step       ir.Step
	cfg        *Config
	client     *client
	workDir    string
	stdout     io.Writer
	stderr     io.Writer
	cancelFunc context.CancelFunc
}

// ExecutionMetrics holds metrics from the execution of one statement.
type ExecutionMetrics struct {
	Statement     int       `json:"statement"`
	QueryID       string    `json:"query_id"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMs    int64     `json:"duration_ms"`
	BytesReturned int64     `json:"bytes_returned"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
}

func newClickHouseExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	cfg, err := ParseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse clickhouse config: %w", err)
	}
	if err := checkQuerySource(cfg, step); err != nil {
		return nil, err
	}
	workDir := runtime.GetEnv(ctx).WorkingDir
	c, err := newClient(cfg, workDir)
	if err != nil {
		return nil, err
	}
	return &clickhouseExecutor{
		step:    step,
		cfg:     cfg,
		client:  c,
		workDir: workDir,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
	}, nil
}

// checkQuerySource requires exactly one source of SQL: the step command or
// script, or the file option.
func checkQuerySource(cfg *Config, step ir.Step) error {
	sources := 0
	for _, present := range []bool{inlineQuery(step) != "", cfg.File != ""} {
		if present {
			sources++
		}
	}
	switch {
	case sources > 1:
		return errors.New("query and file are mutually exclusive")
	case sources == 0:
		return errors.New("query or file is required")
	}
	return nil
}

// validateStep checks the configuration of a declared step. Values that still
// hold a variable reference are resolved only when the step runs, so they are
// left out of the check.
func validateStep(step ir.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	raw := make(map[string]any, len(step.ExecutorConfig.Config))
	for k, v := range step.ExecutorConfig.Config {
		if s, ok := v.(string); ok && strings.Contains(s, "$") {
			continue
		}
		raw[k] = v
	}
	// A reference that was left out still declares the option: url must be
	// present and file counts as a query source.
	for key, placeholder := range map[string]string{"url": "http://reference.invalid", "file": "reference.sql"} {
		if _, ok := raw[key]; !ok {
			if v, declared := step.ExecutorConfig.Config[key]; declared && v != "" {
				raw[key] = placeholder
			}
		}
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		return err
	}
	return checkQuerySource(cfg, step)
}

// inlineQuery returns the query given as the step command or script.
func inlineQuery(step ir.Step) string {
	if step.Script != "" {
		return step.Script
	}
	if len(step.Commands) > 0 {
		cmd := step.Commands[0]
		// CmdWithArgs is the full text; Command may have been split.
		if cmd.CmdWithArgs != "" {
			return cmd.CmdWithArgs
		}
		return cmd.Command
	}
	return ""
}

// SetStdout sets the stdout writer.
func (e *clickhouseExecutor) SetStdout(out io.Writer) { e.stdout = out }

// SetStderr sets the stderr writer.
func (e *clickhouseExecutor) SetStderr(out io.Writer) { e.stderr = out }

// Kill cancels the execution. The statement in flight is stopped on the
// server before Run returns.
func (e *clickhouseExecutor) Kill(_ os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancelFunc != nil {
		e.cancelFunc()
	}
	return nil
}

// Close releases the connections of the executor.
func (e *clickhouseExecutor) Close() error {
	e.client.close()
	return nil
}

// Run executes the statements one after another and stops at the first error.
func (e *clickhouseExecutor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.cancelFunc = cancel
	e.mu.Unlock()
	defer cancel()

	if e.cfg.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, time.Duration(e.cfg.Timeout)*time.Second)
		defer timeoutCancel()
	}

	script, err := e.loadScript()
	if err != nil {
		return err
	}
	statements := splitStatements(script)
	if len(statements) == 0 {
		return errors.New("no clickhouse statement to execute")
	}

	// Statements of a script share one session so that SET, USE, and
	// temporary tables apply to the ones that follow.
	var sessionID string
	if len(statements) > 1 {
		sessionID = newID()
	}

	// The server does not keep the database parameter in the session, so a
	// script selects it once with USE and sends no parameter afterwards. That
	// leaves a USE statement of the script in effect for the ones after it.
	inSession := false
	if sessionID != "" && e.cfg.Database != "" {
		use := statement{sql: "USE " + quoteIdentifier(e.cfg.Database), queryID: newID(), sessionID: sessionID, inSession: true}
		if err := e.runStatement(ctx, 0, use); err != nil {
			return fmt.Errorf("failed to select database %q: %w", e.cfg.Database, err)
		}
		inSession = true
	}

	for i, sql := range statements {
		st := statement{sql: sql, queryID: newID(), sessionID: sessionID, inSession: inSession}
		if err := e.runStatement(ctx, i+1, st); err != nil {
			if len(statements) > 1 {
				return fmt.Errorf("statement %d failed: %w", i+1, err)
			}
			return err
		}
	}
	return nil
}

// quoteIdentifier quotes a database name for a USE statement.
func quoteIdentifier(name string) string {
	return "`" + strings.NewReplacer(`\`, `\\`, "`", "\\`").Replace(name) + "`"
}

// loadScript returns the SQL text of the step.
func (e *clickhouseExecutor) loadScript() (string, error) {
	if e.cfg.File == "" {
		return inlineQuery(e.step), nil
	}
	path := e.cfg.File
	if !filepath.IsAbs(path) && e.workDir != "" {
		path = filepath.Join(e.workDir, path)
	}
	content, err := fileutil.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read sql file %q: %w", e.cfg.File, err)
	}
	return strings.TrimPrefix(string(content), "\ufeff"), nil
}

// runStatement executes one statement, writes metrics, and stops the query on
// the server when the step is cancelled or times out.
func (e *clickhouseExecutor) runStatement(ctx context.Context, num int, st statement) (err error) {
	metrics := &ExecutionMetrics{Statement: num, QueryID: st.queryID, StartedAt: time.Now()}
	defer func() {
		metrics.FinishedAt = time.Now()
		metrics.DurationMs = metrics.FinishedAt.Sub(metrics.StartedAt).Milliseconds()
		metrics.Status = "success"
		if err != nil {
			metrics.Status = "error"
			metrics.Error = err.Error()
		}
		e.writeMetrics(metrics)
	}()

	n, err := e.client.execute(ctx, st, e.stdout)
	metrics.BytesReturned = n
	if err == nil {
		return nil
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		if killErr := e.client.kill(ctx, st.queryID); killErr != nil {
			logger.Warn(ctx, "failed to stop clickhouse query",
				tag.Error(killErr), tag.String("query_id", st.queryID))
		}
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %ds: %w", e.cfg.Timeout, ctxErr)
		}
		return ctxErr
	}
	return err
}

// writeMetrics writes execution metrics to stderr.
func (e *clickhouseExecutor) writeMetrics(metrics *ExecutionMetrics) {
	data, err := json.Marshal(metrics)
	if err != nil {
		return
	}
	_, _ = e.stderr.Write(append(data, '\n'))
}

// newID returns a random identifier for query and session ids.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "dagu-" + hex.EncodeToString(b[:])
}

func init() {
	executor.RegisterExecutor(
		executorType,
		newClickHouseExecutor,
		validateStep,
		registry.ExecutorCapabilities{
			Command:    true,
			Script:     true,
			PathInputs: []string{"file", "tls_ca"},
		},
	)
}
