// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
)

// parseTriggerTypeParam parses and validates the trigger-type flag from the command context.
// Returns TriggerTypeUnknown (zero value) if the flag is empty, otherwise validates
// that the provided value is a known trigger type.
func parseTriggerTypeParam(ctx *Context) (core.TriggerType, error) {
	triggerTypeStr, err := ctx.StringParam("trigger-type")
	if err != nil {
		logger.Debug(ctx, "Failed to read trigger-type flag", tag.Error(err))
	}
	if triggerTypeStr == "" {
		return core.TriggerTypeUnknown, nil
	}

	triggerType := core.ParseTriggerType(triggerTypeStr)
	if triggerType == core.TriggerTypeUnknown {
		return core.TriggerTypeUnknown, fmt.Errorf(
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
func restoreDAGFromStatus(ctx context.Context, dag *core.DAG, status *exec.DAGRunStatus) (*core.DAG, error) {
	dag.Params = spec.QuoteRuntimeParams(status.ParamsList, dag.ParamDefs)
	dag.LoadDotEnv(ctx)
	restored, err := rebuildDAGFromYAML(ctx, dag)
	if err != nil {
		return nil, err
	}
	applyPersistedRunWorkingDir(restored, status)
	return restored, nil
}

func applyPersistedRunWorkingDir(dag *core.DAG, status *exec.DAGRunStatus) {
	if dag == nil || status == nil || status.WorkingDir == "" {
		return
	}
	dag.WorkingDir = status.WorkingDir
	dag.WorkingDirExplicit = true
}

// rebuildDAGFromYAML rebuilds a DAG from its YamlData using the spec loader.
// This populates fields excluded from JSON serialization (json:"-") and must be
// called after LoadDotEnv() so dotenv values are available during rebuild.
//
// The function preserves all JSON-serialized fields from the original DAG and
// only copies JSON-excluded fields (Env, Params, ParamsJSON, SMTP, SSH, S3,
// Redis, Harness, Harnesses, Kubernetes, RegistryAuths, WorkingDirExplicit)
// from the rebuilt DAG.
func rebuildDAGFromYAML(ctx context.Context, dag *core.DAG) (*core.DAG, error) {
	if len(dag.YamlData) == 0 {
		return dag, nil
	}

	buildEnvMap := buildenv.ToMap(dag.Env)
	for key, value := range dag.PresolvedBuildEnv {
		if buildEnvMap == nil {
			buildEnvMap = make(map[string]string)
		}
		buildEnvMap[key] = value
	}
	presolvedBuildEnv, err := buildenv.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load presolved build env: %w", err)
	}
	for key, value := range presolvedBuildEnv {
		if buildEnvMap == nil {
			buildEnvMap = make(map[string]string)
		}
		buildEnvMap[key] = value
	}

	loadOpts := []spec.LoadOption{
		spec.WithParams(dag.Params),
		spec.SkipSchemaValidation(),
	}
	if len(buildEnvMap) > 0 {
		loadOpts = append(loadOpts, spec.WithBuildEnv(buildEnvMap))
	}
	if len(dag.BaseConfigData) > 0 {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent(dag.BaseConfigData))
	}

	if dag.Name != "" {
		loadOpts = append(loadOpts, spec.WithName(dag.Name))
	}

	fresh, err := spec.LoadYAML(ctx, dag.YamlData, loadOpts...)
	if err != nil {
		return nil, err
	}

	// Copy only fields excluded from JSON serialization (json:"-").
	// All other fields (Queue, WorkerSelector, HandlerOn, Steps, Labels, etc.)
	// are already correctly stored in dag.json and must be preserved.
	dag.Env = fresh.Env
	dag.Params = fresh.Params
	dag.ParamsJSON = fresh.ParamsJSON
	dag.SMTP = fresh.SMTP
	dag.SSH = fresh.SSH
	dag.S3 = fresh.S3
	dag.Redis = fresh.Redis
	dag.Harness = fresh.Harness
	dag.Harnesses = fresh.Harnesses
	dag.Kubernetes = fresh.Kubernetes
	dag.RegistryAuths = fresh.RegistryAuths
	dag.WorkingDirExplicit = fresh.WorkingDirExplicit

	core.InitializeDefaults(dag)

	return dag, nil
}

// extractDAGName extracts the DAG name from a file path or name.
// If the input is a file path (.yaml or .yml), it loads the DAG metadata
// to extract the name. Otherwise, it returns the input as-is.
func extractDAGName(ctx *Context, name string) (string, error) {
	if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		return name, nil
	}

	dagStore, err := ctx.dagStore(dagStoreConfig{})
	if err != nil {
		return "", fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	dag, err := dagStore.GetMetadata(ctx, name)
	if err != nil {
		return "", fmt.Errorf("failed to read DAG metadata from file %s: %w", name, err)
	}

	return dag.Name, nil
}
