// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
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
	// artifacts is the step's directory under the run artifacts directory
	// when artifact: true; nil otherwise.
	artifacts *agentstep.ArtifactStore

	mu       sync.Mutex
	cancel   context.CancelFunc
	outputs  map[string]any
	exitCode int
}

func newWriteExecutor(env runtime.Env, op, path string, cfg config) (*writeExecutor, error) {
	e := &writeExecutor{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		op:      op,
		path:    path,
		workDir: env.WorkingDir,
		cfg:     cfg,
	}
	if cfg.Artifact {
		dir := ""
		if env.Scope != nil {
			dir, _ = env.Scope.Get(runenv.EnvKeyDAGRunArtifactsDir)
		}
		if dir == "" {
			return nil, errors.New("artifact requires artifact storage")
		}
		e.artifacts = agentstep.NewArtifactStore(dir, "xlsx", env.Step.Name)
	}
	return e, nil
}

// keepArtifact copies the saved workbook into the run's artifacts and
// returns its path relative to the artifacts directory.
func (e *writeExecutor) keepArtifact() (string, error) {
	if e.artifacts == nil {
		return "", nil
	}
	dir := e.artifacts.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create artifact directory: %w", err)
	}
	name := filepath.Base(e.path)
	data, err := os.ReadFile(e.path)
	if err != nil {
		return "", fmt.Errorf("copy workbook to artifacts: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil { //nolint:gosec // artifacts are readable like other run files
		return "", fmt.Errorf("copy workbook to artifacts: %w", err)
	}
	return e.artifacts.RelPath(name), nil
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
	if err == nil && !result.DryRun {
		// The workbook is already saved at this point. A failed copy must
		// not fail the step, or a retry would append the same rows again;
		// it is reported as a warning instead.
		switch artifact, copyErr := e.keepArtifact(); {
		case copyErr != nil:
			result.Warnings = append(result.Warnings, "workbook saved but not kept as an artifact: "+copyErr.Error())
		case artifact != "":
			result.Artifact = artifact
		}
	}
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
	if result.Artifact != "" {
		e.outputs["artifact"] = result.Artifact
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
