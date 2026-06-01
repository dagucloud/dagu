// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/spf13/cobra"
)

const (
	flagEnv          = "env"
	flagDotenv       = "dotenv"
	flagWorkerLabel  = "worker-label"
	flagWorkdir      = "workdir"
	flagShell        = "shell"
	flagBase         = "base"
	defaultStepName  = "main"
	execCommandUsage = "exec [flags] -- <command> [args...]"
)

var (
	execFlags = []commandLineFlag{
		dagRunIDFlag,
		nameFlag,
		workdirFlag,
		shellFlag,
		baseFlag,
	}
	workdirFlag = commandLineFlag{
		name:  flagWorkdir,
		usage: "Working directory for executing the command (default: current directory)",
	}
	shellFlag = commandLineFlag{
		name:  flagShell,
		usage: "Override shell binary for the command (default: use DAGU default shell)",
	}
	baseFlag = commandLineFlag{
		name:  flagBase,
		usage: "Path to a base DAG YAML whose defaults are applied before inline overrides",
	}
)

// Exec returns the cobra command for executing inline commands without a DAG spec.
func Exec() *cobra.Command {
	cmd := &cobra.Command{
		Use:   execCommandUsage,
		Short: "Execute a one-off command as a DAG run",
		Long: `Execute a one-off command as a DAG run without creating a DAG YAML file.

Examples:
  dagu exec -- echo "hello world"
  dagu exec --env FOO=bar -- sh -c 'echo $FOO'
  dagu exec --worker-label role=batch -- python remote_script.py`,
		Args: cobra.ArbitraryArgs,
	}

	command := NewCommand(cmd, execFlags, runExec)

	command.Flags().StringArrayP(flagEnv, "E", nil, "Environment variable (KEY=VALUE) to include in the run; repeatable")
	command.Flags().StringArray(flagDotenv, nil, "Path to a dotenv file to load before execution; repeatable")
	command.Flags().StringArray(flagWorkerLabel, nil, "Worker label selector (key=value) for distributed execution; repeatable")

	return command
}

// runExec parses flags and arguments and executes the provided command as an inline DAG run.
func runExec(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required (try: dagu exec -- <command>)")
	}

	runID, err := resolveRunID(ctx)
	if err != nil {
		return err
	}

	nameOverride, err := ctx.StringParam("name")
	if err != nil {
		return fmt.Errorf("failed to read name flag: %w", err)
	}

	workingDir, err := resolveWorkingDir(ctx)
	if err != nil {
		return err
	}

	shellOverride, err := ctx.Command.Flags().GetString(flagShell)
	if err != nil {
		return fmt.Errorf("failed to read shell flag: %w", err)
	}

	baseConfig, err := resolveBaseConfig(ctx)
	if err != nil {
		return err
	}

	envVars, err := parseEnvVars(ctx)
	if err != nil {
		return err
	}

	dotenvPaths, err := resolveDotenvPaths(ctx)
	if err != nil {
		return err
	}

	workerLabels, err := parseWorkerLabels(ctx)
	if err != nil {
		return err
	}

	opts := ExecOptions{
		Name:          nameOverride,
		CommandArgs:   args,
		ShellOverride: shellOverride,
		WorkingDir:    workingDir,
		Env:           envVars,
		DotenvFiles:   dotenvPaths,
		BaseConfig:    baseConfig,
		WorkerLabels:  workerLabels,
	}

	dag, _, err := buildExecDAG(ctx, opts)
	if err != nil {
		return err
	}

	dagRunRef := exec.NewDAGRunRef(dag.Name, runID)

	attempt, err := ctx.DAGRunStore.FindAttempt(ctx, dagRunRef)
	if err != nil && !errors.Is(err, exec.ErrDAGRunIDNotFound) {
		return fmt.Errorf("failed to check for existing dag-run: %w", err)
	}
	if attempt != nil {
		return fmt.Errorf("dag-run ID %s already exists for DAG %s", runID, dag.Name)
	}

	logger.Info(ctx, "Executing inline dag-run",
		tag.DAG(dag.Name),
		tag.RunID(runID),
	)
	logger.Debug(ctx, "Command details", tag.Command(strings.Join(args, " ")))

	return tryExecuteDAG(ctx, dag, runID, dagRunRef, "local", "", core.TriggerTypeManual, "", "")
}

// resolveRunID returns a validated run ID from the flag or generates a new one.
func resolveRunID(ctx *Context) (string, error) {
	runID, err := ctx.StringParam("run-id")
	if err != nil {
		return "", fmt.Errorf("failed to read run-id flag: %w", err)
	}

	if runID == "" {
		generatedID, genErr := genRunID()
		if genErr != nil {
			return "", fmt.Errorf("failed to generate dag-run ID: %w", genErr)
		}
		return generatedID, nil
	}

	if err := validateRunID(runID); err != nil {
		return "", err
	}
	return runID, nil
}

// resolveWorkingDir returns the validated working directory from the flag or current directory.
func resolveWorkingDir(ctx *Context) (string, error) {
	workdirValue, err := ctx.Command.Flags().GetString(flagWorkdir)
	if err != nil {
		return "", fmt.Errorf("failed to read workdir flag: %w", err)
	}

	workingDir := fileutil.ResolvePathOrBlank(workdirValue)
	if workingDir == "" {
		workingDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to determine current directory: %w", err)
		}
	}

	info, err := os.Stat(workingDir)
	if err != nil {
		return "", fmt.Errorf("working directory %q not found: %w", workingDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a directory", workingDir)
	}

	return workingDir, nil
}

// resolveBaseConfig returns the validated base config path from the flag.
func resolveBaseConfig(ctx *Context) (string, error) {
	baseConfigValue, err := ctx.Command.Flags().GetString(flagBase)
	if err != nil {
		return "", fmt.Errorf("failed to read base flag: %w", err)
	}

	if baseConfigValue == "" {
		return "", nil
	}

	baseConfig := fileutil.ResolvePathOrBlank(baseConfigValue)
	if baseConfig == "" || !fileutil.FileExists(baseConfig) {
		return "", fmt.Errorf("base DAG file %q not found", baseConfigValue)
	}
	return baseConfig, nil
}

// parseEnvVars validates and returns environment variables from the flag.
func parseEnvVars(ctx *Context) ([]string, error) {
	envVars, err := ctx.Command.Flags().GetStringArray(flagEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to read env flags: %w", err)
	}

	for _, env := range envVars {
		if !strings.Contains(env, "=") || strings.HasPrefix(env, "=") {
			return nil, fmt.Errorf("invalid --env value %q: expected KEY=VALUE", env)
		}
	}
	return envVars, nil
}

// resolveDotenvPaths validates and returns resolved dotenv file paths.
func resolveDotenvPaths(ctx *Context) ([]string, error) {
	dotenvPathsRaw, err := ctx.Command.Flags().GetStringArray(flagDotenv)
	if err != nil {
		return nil, fmt.Errorf("failed to read dotenv flags: %w", err)
	}

	var dotenvPaths []string
	for _, path := range dotenvPathsRaw {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		resolved := fileutil.ResolvePathOrBlank(trimmed)
		if resolved == "" || !fileutil.FileExists(resolved) {
			return nil, fmt.Errorf("dotenv file %q not found", path)
		}
		dotenvPaths = append(dotenvPaths, resolved)
	}
	return dotenvPaths, nil
}

// parseWorkerLabels validates and returns worker labels from the flag.
func parseWorkerLabels(ctx *Context) (map[string]string, error) {
	workerLabelPairs, err := ctx.Command.Flags().GetStringArray(flagWorkerLabel)
	if err != nil {
		return nil, fmt.Errorf("failed to read worker-label flags: %w", err)
	}

	workerLabels := make(map[string]string, len(workerLabelPairs))
	for _, pair := range workerLabelPairs {
		trimmed := strings.TrimSpace(pair)
		if trimmed == "" {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found || key == "" || value == "" {
			return nil, fmt.Errorf("invalid worker label %q: expected key=value", pair)
		}
		workerLabels[key] = value
	}

	if len(workerLabels) > 0 && !ctx.Config.Queues.Enabled {
		return nil, fmt.Errorf("worker selector requires queues; enable queues or remove --worker-label")
	}

	return workerLabels, nil
}
