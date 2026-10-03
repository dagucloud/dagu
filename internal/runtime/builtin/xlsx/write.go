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
	"strings"
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

// keepArtifact copies the file a writer saved into the run's artifacts and
// returns its path relative to the artifacts directory.
func (e *writeExecutor) keepArtifact(saved string) (string, error) {
	if e.artifacts == nil {
		return "", nil
	}
	dir := e.artifacts.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create artifact directory: %w", err)
	}
	name := filepath.Base(saved)
	data, err := os.ReadFile(saved) //nolint:gosec // the file the step just wrote
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

	out, err := e.run(ctx)
	if err == nil && out.saved != "" {
		// The file is already saved at this point. A failed copy must not
		// fail the step, or a retry would append the same rows again; it is
		// reported as a warning instead.
		switch artifact, copyErr := e.keepArtifact(out.saved); {
		case copyErr != nil:
			out.warnings = append(out.warnings, "file saved but not kept as an artifact: "+copyErr.Error())
		case artifact != "":
			out.outputs["artifact"] = artifact
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.exitCode = 1
		return err
	}
	out.outputs["warnings"] = out.warnings
	e.outputs = out.outputs
	for _, w := range out.warnings {
		_, _ = fmt.Fprintln(e.stderr, "warning: "+w)
	}
	_, _ = fmt.Fprintln(e.stdout, out.line)
	return nil
}

// outcome is what one writer operation produced.
type outcome struct {
	outputs  map[string]any
	line     string
	warnings []string
	// saved is the file to keep as an artifact; empty when nothing was
	// written, as in a dry run or a skipped sheet operation.
	saved string
}

// writerOutcome is the outcome of an operation that returns a WriteResult.
func writerOutcome(result *workbook.WriteResult, line string, saved bool) outcome {
	out := outcome{
		outputs: map[string]any{
			"path":    result.Path,
			"sheet":   result.Sheet,
			"changes": result.Changes,
			"dry_run": result.DryRun,
		},
		line:     line,
		warnings: result.Warnings,
	}
	if saved && !result.DryRun {
		out.saved = result.Path
	}
	return out
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
	table, err := workbook.LoadTable(input, workbook.LoadOptions{Format: e.cfg.Format, Columns: e.cfg.Columns, Encoding: e.cfg.encoding, Delimiter: e.cfg.delimiter})
	if err != nil {
		return workbook.Table{}, fmt.Errorf("%w: %v", errConfig, err)
	}
	return table, nil
}

func (e *writeExecutor) run(ctx context.Context) (outcome, error) {
	switch e.op {
	case opWrite, opAppend:
		table, err := e.loadTable()
		if err != nil {
			return outcome{}, err
		}
		opts := e.cfg.writeOptions(e.lockLog(ctx))
		var result *workbook.WriteResult
		if e.op == opAppend {
			result, err = workbook.Append(ctx, e.path, table, opts)
		} else {
			result, err = workbook.Write(ctx, e.path, table, opts)
		}
		if err != nil {
			return outcome{}, err
		}
		verb := "Wrote"
		if e.op == opAppend || opts.Mode == workbook.WriteAppend {
			verb = "Appended"
		}
		return writerOutcome(result, summaryLine(verb, result), true), nil
	case opUpdateRows:
		rows, err := workbook.DecodeUpdateRows(e.cfg.Rows)
		if err != nil {
			return outcome{}, fmt.Errorf("%w: %v", errConfig, err)
		}
		result, err := workbook.UpdateRows(ctx, e.path, e.cfg.updateOptions(rows, e.lockLog(ctx)))
		if err != nil {
			return outcome{}, err
		}
		line := summaryLine("Updated", result)
		if result.Changes.RowsAppended > 0 {
			line += fmt.Sprintf(" (%d rows appended)", result.Changes.RowsAppended)
		}
		return writerOutcome(result, line, true), nil
	case opWriteCells:
		output, err := e.outputPath()
		if err != nil {
			return outcome{}, err
		}
		result, err := workbook.WriteCells(ctx, e.path, e.cfg.writeCellsOptions(output, e.lockLog(ctx)))
		if err != nil {
			return outcome{}, err
		}
		n := result.Changes.CellsChanged
		line := fmt.Sprintf("Wrote %d %s to %s %s", n, plural(n, "cell"), workbook.Base(result.Path), result.Sheet)
		if m := result.Changes.Merged; m > 0 {
			line += fmt.Sprintf(" (%d %s merged)", m, plural(m, "range"))
		}
		if result.DryRun {
			line += " (dry run)"
		}
		return writerOutcome(result, line, true), nil
	case opSheet:
		result, err := workbook.Sheet(ctx, e.path, e.cfg.sheetOptions(e.lockLog(ctx)))
		if err != nil {
			return outcome{}, err
		}
		out := writerOutcome(&result.WriteResult, sheetLine(e.cfg, result), !result.Skipped)
		out.outputs["sheets"] = result.Sheets
		return out, nil
	case opConvert:
		output, err := e.outputPath()
		if err != nil {
			return outcome{}, err
		}
		result, err := workbook.Convert(ctx, e.path, e.cfg.convertOptions(output))
		if err != nil {
			return outcome{}, err
		}
		return outcome{
			outputs: map[string]any{
				"path":   result.Path,
				"format": result.Format,
				"count":  result.Count,
				"sheet":  result.Sheet,
				"range":  result.Range,
			},
			line:     fmt.Sprintf("Converted %d %s from %s %s to %s", result.Count, plural(result.Count, "row"), workbook.Base(e.path), result.Range, workbook.Base(result.Path)),
			warnings: result.Warnings,
			saved:    result.Path,
		}, nil
	default:
		return outcome{}, fmt.Errorf("%w: unsupported operation %q", errConfig, e.op)
	}
}

// outputPath resolves the output option against the working directory, or
// returns empty when there is none.
func (e *writeExecutor) outputPath() (string, error) {
	if strings.TrimSpace(e.cfg.Output) == "" {
		return "", nil
	}
	return resolvePath(e.workDir, e.cfg.Output)
}

// sheetLine describes a sheet operation in one line.
func sheetLine(cfg config, result *workbook.SheetResult) string {
	book := workbook.Base(result.Path)
	operation := workbook.SheetOperation(strings.ToLower(strings.TrimSpace(cfg.Operation)))
	var line string
	switch {
	case result.Skipped:
		// A source that was not found has no result name; the one asked
		// for is what the reader recognizes.
		name := result.Sheet
		if name == "" {
			name = cfg.Sheet
		}
		line = fmt.Sprintf("Sheet %q left as it is in %s; skipped", name, book)
	case operation == workbook.SheetAdd:
		line = fmt.Sprintf("Added sheet %q to %s", result.Sheet, book)
	case operation == workbook.SheetCopy:
		line = fmt.Sprintf("Copied sheet %q to %q in %s", cfg.Sheet, result.Sheet, book)
	case operation == workbook.SheetRename:
		line = fmt.Sprintf("Renamed sheet %q to %q in %s", cfg.Sheet, result.Sheet, book)
	default:
		line = fmt.Sprintf("Deleted sheet %q from %s", result.Sheet, book)
	}
	if result.DryRun && !result.Skipped {
		line += " (dry run)"
	}
	return line
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
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
