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

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var (
	_ executor.Executor                = (*readExecutor)(nil)
	_ executor.DeclaredOutputsProvider = (*readExecutor)(nil)
	_ executor.ExitCoder               = (*readExecutor)(nil)
)

// readExecutor runs xlsx.read, xlsx.info, and xlsx.list_sheets.
type readExecutor struct {
	stdout io.Writer
	stderr io.Writer
	op     string
	path   string
	cfg    config
	budget int

	mu       sync.Mutex
	cancel   context.CancelFunc
	outputs  map[string]any
	exitCode int
}

func newReadExecutor(env runtime.Env, op, path string, cfg config) *readExecutor {
	return &readExecutor{
		stdout: os.Stdout,
		stderr: os.Stderr,
		op:     op,
		path:   path,
		cfg:    cfg,
		budget: budgetFor(env),
	}
}

func (e *readExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *readExecutor) SetStderr(out io.Writer) { e.stderr = out }

func (e *readExecutor) Kill(os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *readExecutor) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *readExecutor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	outputs, line, warnings, err := e.run(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.exitCode = 1
		return err
	}
	e.outputs = outputs
	for _, w := range warnings {
		_, _ = fmt.Fprintln(e.stderr, "warning: "+w)
	}
	_, _ = fmt.Fprintln(e.stdout, line)
	return nil
}

func (e *readExecutor) run(ctx context.Context) (map[string]any, string, []string, error) {
	switch e.op {
	case opRead:
		result, err := workbook.Read(ctx, e.path, e.cfg.readOptions())
		if err != nil {
			return nil, "", nil, err
		}
		rows, truncated := workbook.FitRows(result.Rows, e.budget)
		if truncated {
			result.Warnings = append(result.Warnings, fmt.Sprintf("output truncated to %d of %d rows; narrow the range or columns, or filter with where", len(rows), result.Count))
			result.Truncated = true
		}
		outputs := map[string]any{
			"rows":      rows,
			"count":     len(rows),
			"headers":   result.Headers,
			"sheet":     result.Sheet,
			"range":     result.Range,
			"warnings":  result.Warnings,
			"truncated": result.Truncated,
		}
		line := fmt.Sprintf("Read %d rows from %s %s", len(rows), workbook.Base(e.path), result.Range)
		return outputs, line, result.Warnings, nil
	case opInfo:
		info, err := workbook.Inspect(ctx, e.path, workbook.InspectOptions{Password: e.cfg.Password})
		if err != nil {
			return nil, "", nil, err
		}
		outputs := map[string]any{
			"path":         e.path,
			"date_system":  info.DateSystem,
			"sheets":       info.Sheets,
			"named_ranges": info.NamedRanges,
			"warnings":     info.Warnings,
		}
		line := fmt.Sprintf("%s: %d sheets", workbook.Base(e.path), len(info.Sheets))
		return outputs, line, info.Warnings, nil
	case opListSheets:
		sheets, err := workbook.ListSheets(e.path, e.cfg.Password)
		if err != nil {
			return nil, "", nil, err
		}
		outputs := map[string]any{"sheets": sheets, "count": len(sheets)}
		line := fmt.Sprintf("%s: %d sheets", workbook.Base(e.path), len(sheets))
		return outputs, line, nil, nil
	default:
		return nil, "", nil, fmt.Errorf("%w: unsupported operation %q", errConfig, e.op)
	}
}

// GetOutputs returns the fixed outputs of the operation after a successful run.
func (e *readExecutor) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

// PublishesDeclaredOutputs exposes the fixed outputs to step references.
func (e *readExecutor) PublishesDeclaredOutputs() bool { return true }
