// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package xlsx implements the xlsx.* built-in actions over
// internal/cmn/workbook: reading, inspecting, and writing .xlsx workbooks
// without a spreadsheet application.
package xlsx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

const (
	executorType = ir.ExecutorTypeXlsx

	opRead       = "read"
	opInfo       = "info"
	opListSheets = "list_sheets"
	opWrite      = "write"
	opAppend     = "append"
	opUpdateRows = "update_rows"
	opValidate   = "validate"
	opWriteCells = "write_cells"
	opSheet      = "sheet"
	opConvert    = "convert"
	opExtract    = "extract"
)

const (
	// Output budget: 900 KiB, or the DAG's max_output_size less this margin,
	// matching mail.search.
	outputBudget = 900 << 10
	outputMargin = 64 << 10
)

var errConfig = errors.New("xlsx: configuration error")

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{Command: true, LLM: true, DryRunCheck: dryRunCheck})
}

func newExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	// At run time every reference has been resolved, so nothing is deferred.
	cfg, op, err := loadConfig(step, false)
	if err != nil {
		return nil, err
	}
	env := runtime.GetEnv(ctx)
	path, err := resolvePath(env.WorkingDir, cfg.Path)
	if err != nil {
		return nil, err
	}
	switch op {
	case opRead, opInfo, opListSheets, opValidate:
		return newReadExecutor(env, op, path, cfg), nil
	case opWrite, opAppend, opUpdateRows, opWriteCells, opSheet, opConvert:
		return newWriteExecutor(env, op, path, cfg)
	case opExtract:
		return newExtractExecutor(ctx, env, step, path, cfg)
	default:
		return nil, fmt.Errorf("%w: unsupported operation %q", errConfig, op)
	}
}

func validateStep(step ir.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	// At build time a value such as ${params.DRY_RUN} is still a reference;
	// its type is checked when the step runs.
	_, op, err := loadConfig(step, true)
	if err != nil {
		return err
	}
	if op == opExtract && step.LLM == nil {
		return errors.New(errMissingModel)
	}
	return nil
}

func loadConfig(step ir.Step, deferReferences bool) (config, string, error) {
	cfg := defaultConfig()
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg, deferReferences); err != nil {
		return cfg, "", err
	}
	op := stepOperation(step)
	if err := validateConfig(op, &cfg); err != nil {
		return cfg, op, err
	}
	return cfg, op, nil
}

func stepOperation(step ir.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
}

// budgetFor is the byte budget for rows published as outputs. It stays
// positive for a small max_output_size, because a budget of zero would
// mean no limit to FitRows and the step would then exceed the DAG's size.
func budgetFor(env runtime.Env) int {
	limit := ir.DefaultMaxOutputSize
	if env.DAG != nil && env.DAG.MaxOutputSize > 0 {
		limit = env.DAG.MaxOutputSize
	}
	budget := limit - outputMargin
	if budget <= 0 {
		budget = limit / 2
	}
	return max(min(outputBudget, budget), 1)
}
