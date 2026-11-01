// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"strings"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// remoteWorkerSelector renders the --worker-label flags for a remote start or
// enqueue request, or nil when none were given.
func remoteWorkerSelector(ctx *Context) (*api.WorkerSelectorOverride, error) {
	workerLabels, err := workerLabelsFromFlags(ctx)
	if err != nil {
		return nil, err
	}
	if len(workerLabels) == 0 {
		return nil, nil
	}
	selector := api.WorkerSelectorOverride(workerLabels)
	return &selector, nil
}

// flagWorkerLabel selects the workers a run may execute on, as repeatable
// key=value pairs. On start and enqueue it replaces the DAG's worker_selector
// for that run; exec uses it to build the one-off DAG.
const flagWorkerLabel = "worker-label"

var workerLabelFlag = commandLineFlag{
	name:          flagWorkerLabel,
	usage:         "Worker label (key=value) the run requires, replacing the DAG's worker_selector for this run; repeatable",
	isStringArray: true,
}

// workerLabelsFromFlags parses and validates the --worker-label flags. It
// returns an empty map when none were given.
func workerLabelsFromFlags(ctx *Context) (map[string]string, error) {
	workerLabelPairs, err := ctx.Command.Flags().GetStringArray(flagWorkerLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to read worker-label flags: %w", err)
	}
	// pflag drops an empty value before it reaches the array; the flag still
	// counts as changed, which is how `--worker-label ""` is told from no flag.
	if len(workerLabelPairs) == 0 && ctx.Command.Flags().Changed(flagWorkerLabel) {
		return nil, fmt.Errorf("invalid worker label %q: expected key=value", "")
	}

	workerLabels := make(map[string]string, len(workerLabelPairs))
	for _, pair := range workerLabelPairs {
		// An empty entry is a routing request that says nothing; refusing it
		// keeps a mistyped flag from silently leaving the DAG's selector in place.
		key, value, found := strings.Cut(strings.TrimSpace(pair), "=")
		if !found || key == "" || value == "" {
			return nil, fmt.Errorf("invalid worker label %q: expected key=value", pair)
		}
		workerLabels[key] = value
	}
	if err := ir.ValidateWorkerSelector(workerLabels); err != nil {
		return nil, err
	}
	return workerLabels, nil
}

// applyWorkerLabelsOverride replaces the loaded DAG's worker selector with the
// --worker-label flags when any were given.
func applyWorkerLabelsOverride(ctx *Context, dag *ir.DAG) error {
	workerLabels, err := workerLabelsFromFlags(ctx)
	if err != nil {
		return err
	}
	if len(workerLabels) == 0 {
		return nil
	}
	if err := dag.ApplyWorkerSelector(workerLabels); err != nil {
		return fmt.Errorf("invalid --worker-label: %w", err)
	}
	return nil
}
