// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var (
	_ executor.Executor                = (*writeExecutor)(nil)
	_ executor.DeclaredOutputsProvider = (*writeExecutor)(nil)
	_ executor.ExitCoder               = (*writeExecutor)(nil)
)

// writeExecutor runs xlsx.write, xlsx.append, and xlsx.update_rows.
type writeExecutor struct {
	stdout  io.Writer
	stderr  io.Writer
	op      string
	path    string
	workDir string
	cfg     config

	mu       sync.Mutex
	cancel   context.CancelFunc
	outputs  map[string]any
	exitCode int
}

func newWriteExecutor(env runtime.Env, op, path string, cfg config) *writeExecutor {
	return &writeExecutor{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		op:      op,
		path:    path,
		workDir: env.WorkingDir,
		cfg:     cfg,
	}
}

func (e *writeExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *writeExecutor) SetStderr(out io.Writer) { e.stderr = out }

func (e *writeExecutor) Kill(os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *writeExecutor) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *writeExecutor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	result, line, err := e.run(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.exitCode = 1
		return err
	}
	e.outputs = map[string]any{
		"path":     result.Path,
		"sheet":    result.Sheet,
		"changes":  result.Changes,
		"dry_run":  result.DryRun,
		"warnings": result.Warnings,
	}
	for _, w := range result.Warnings {
		_, _ = fmt.Fprintln(e.stderr, "warning: "+w)
	}
	_, _ = fmt.Fprintln(e.stdout, line)
	return nil
}

// lockLog reports lock retries to the step log and the run log.
func (e *writeExecutor) lockLog(ctx context.Context) func(string) {
	return func(msg string) {
		_, _ = fmt.Fprintln(e.stderr, msg)
		logger.Info(ctx, msg)
	}
}

func (e *writeExecutor) loadTable() (workbook.Table, error) {
	if e.cfg.present["rows"] {
		table, err := workbook.DecodeRows(e.cfg.Rows, e.cfg.Columns)
		if err != nil {
			return workbook.Table{}, fmt.Errorf("%w: %v", errConfig, err)
		}
		return table, nil
	}
	input, err := resolvePath(e.workDir, e.cfg.Input)
	if err != nil {
		return workbook.Table{}, err
	}
	table, err := workbook.LoadTable(input, e.cfg.Format, e.cfg.Columns)
	if err != nil {
		return workbook.Table{}, fmt.Errorf("%w: %v", errConfig, err)
	}
	return table, nil
}

func (e *writeExecutor) run(ctx context.Context) (*workbook.WriteResult, string, error) {
	switch e.op {
	case opWrite, opAppend:
		table, err := e.loadTable()
		if err != nil {
			return nil, "", err
		}
		opts := e.cfg.writeOptions(e.lockLog(ctx))
		var result *workbook.WriteResult
		if e.op == opAppend {
			result, err = workbook.Append(ctx, e.path, table, opts)
		} else {
			result, err = workbook.Write(ctx, e.path, table, opts)
		}
		if err != nil {
			return nil, "", err
		}
		verb := "Wrote"
		if e.op == opAppend || opts.Mode == workbook.WriteAppend {
			verb = "Appended"
		}
		return result, summaryLine(verb, result), nil
	case opUpdateRows:
		rows, err := workbook.DecodeUpdateRows(e.cfg.Rows)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", errConfig, err)
		}
		result, err := workbook.UpdateRows(ctx, e.path, e.cfg.updateOptions(rows, e.lockLog(ctx)))
		if err != nil {
			return nil, "", err
		}
		line := summaryLine("Updated", result)
		if result.Changes.RowsAppended > 0 {
			line += fmt.Sprintf(" (%d rows appended)", result.Changes.RowsAppended)
		}
		return result, line, nil
	default:
		return nil, "", fmt.Errorf("%w: unsupported operation %q", errConfig, e.op)
	}
}

func summaryLine(verb string, result *workbook.WriteResult) string {
	count := result.Changes.RowsAppended
	if verb == "Updated" {
		count = result.Changes.RowsUpdated
	}
	line := fmt.Sprintf("%s %d rows %s %s %s", verb, count, preposition(verb), workbook.Base(result.Path), result.Sheet)
	if result.Changes.ColumnsAdded > 0 {
		line += fmt.Sprintf(" (%d columns added)", result.Changes.ColumnsAdded)
	}
	if result.DryRun {
		line += " (dry run)"
	}
	return line
}

func preposition(verb string) string {
	if verb == "Updated" {
		return "in"
	}
	return "to"
}

// GetOutputs returns path, sheet, changes, dry_run, and warnings after a
// successful run.
func (e *writeExecutor) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

// PublishesDeclaredOutputs exposes the fixed outputs to step references.
func (e *writeExecutor) PublishesDeclaredOutputs() bool { return true }
