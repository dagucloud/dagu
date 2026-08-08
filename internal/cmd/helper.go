// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/dagwarning"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// parseTriggerTypeParam parses and validates the trigger-type flag from the command context.
// Returns TriggerTypeUnknown (zero value) if the flag is empty, otherwise validates
// that the provided value is a known trigger type.
func parseTriggerTypeParam(ctx *Context) (ir.TriggerType, error) {
	triggerTypeStr, err := ctx.StringParam("trigger-type")
	if err != nil {
		logger.Debug(ctx, "Failed to read trigger-type flag", tag.Error(err))
	}
	if triggerTypeStr == "" {
		return ir.TriggerTypeUnknown, nil
	}

	triggerType := ir.ParseTriggerType(triggerTypeStr)
	if triggerType == ir.TriggerTypeUnknown {
		return ir.TriggerTypeUnknown, fmt.Errorf(
			"invalid trigger-type %q: must be one of scheduler, manual, webhook, subdag, retry, catchup",
			triggerTypeStr,
		)
	}

	return triggerType, nil
}

func labelsParam(ctx *Context) (string, error) {
	labels, err := ctx.StringParam("labels")
	if err != nil {
		return "", fmt.Errorf("failed to get labels: %w", err)
	}
	tags, err := ctx.StringParam("tags")
	if err != nil {
		return "", fmt.Errorf("failed to get deprecated tags: %w", err)
	}

	labelsChanged := ctx.Command.Flags().Changed("labels")
	tagsChanged := ctx.Command.Flags().Changed("tags")
	if labelsChanged && tagsChanged {
		return "", fmt.Errorf("labels and deprecated tags cannot both be set")
	}
	if labelsChanged {
		return labels, nil
	}
	return tags, nil
}

// parseScheduleTimeParam reads and validates the --schedule-time flag.
// Returns the validated RFC 3339 string or empty if not set.
func parseScheduleTimeParam(ctx *Context) (string, error) {
	scheduleTime, err := ctx.StringParam("schedule-time")
	if err != nil {
		return "", fmt.Errorf("failed to get schedule-time: %w", err)
	}
	if scheduleTime != "" {
		if _, parseErr := time.Parse(time.RFC3339, scheduleTime); parseErr != nil {
			return "", fmt.Errorf("invalid --schedule-time value %q: must be RFC 3339 format: %w", scheduleTime, parseErr)
		}
	}
	return scheduleTime, nil
}

// restoreDAGFromStatus restores a DAG from a previous run's status and YAML.
// It restores params from the status, loads dotenv, and rebuilds fields excluded
// from JSON serialization (env, params JSON, registryAuths, etc.).
func restoreDAGFromStatus(ctx context.Context, dag *ir.DAG, status *exec.DAGRunStatus) (*ir.DAG, error) {
	runtimeParams := append([]string(nil), status.ParamsList...)
	dag.Params = runtimeParams
	if err := dagwarning.LoadDotEnv(ctx, dag); err != nil {
		return nil, err
	}
	restored, err := spec.RebuildFromYAML(ctx, dag, spec.QuoteRuntimeParams(runtimeParams, dag.ParamDefs))
	if err != nil {
		return nil, err
	}
	applyPersistedRunWorkingDir(restored, status)
	return restored, nil
}

func applyPersistedRunWorkingDir(dag *ir.DAG, status *exec.DAGRunStatus) {
	if dag == nil || status == nil || status.WorkingDir == "" {
		return
	}
	dag.WorkingDir = status.WorkingDir
	dag.WorkingDirExplicit = true
}

// extractDAGName extracts the DAG name from a file path or name.
// If the input is a file path (.yaml or .yml), it loads the DAG metadata
// to extract the name. Otherwise, it returns the input as-is.
func extractDAGName(ctx *Context, name string) (string, error) {
	if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		return name, nil
	}

	absolutePath, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("failed to resolve DAG file path %s: %w", name, err)
	}

	dagStore, err := ctx.dagStore(dagStoreConfig{
		SearchPaths: []string{filepath.Dir(absolutePath)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	dag, err := dagStore.GetMetadata(ctx, absolutePath)
	if err != nil {
		return "", fmt.Errorf("failed to read DAG metadata from file %s: %w", name, err)
	}

	return dag.Name, nil
}
