// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/spf13/cobra"
)

var dryFlags = []commandLineFlag{paramsFlag, nameFlag}

// Dry returns the cobra command for dry-run simulation.
func Dry() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "dry [flags] <DAG definition> [-- param1 param2 ...]",
			Short: "Simulate a DAG-run without executing actual commands",
			Long: `Perform a dry-run simulation of a DAG-run without executing any real actions.

This command simulates the DAG-run based on the provided DAG definition,
without producing any side effects or running actual commands.

Parameters after the "--" separator are passed as execution parameters (either positional or key=value pairs),
allowing you to test different parameter configurations.

Example:
  dagu dry my_dag.yaml -- P1=foo P2=bar
`,
			Args: cobra.MinimumNArgs(1),
		}, dryFlags,
		runDry,
	)
}

// runDry executes a dry-run simulation of the specified DAG.
func runDry(ctx *Context, args []string) error {
	dag, err := loadDAGForDryRun(ctx, args)
	if err != nil {
		return err
	}

	if err := dag.Validate(); err != nil {
		return fmt.Errorf("validation failed for %s: %w", args[0], err)
	}

	dagRunID, err := genRunID()
	if err != nil {
		return fmt.Errorf("failed to generate dag-run ID: %w", err)
	}

	logFile, err := ctx.OpenLogFile(dag, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize log file for dag-run %s: %w", dag.Name, err)
	}
	defer func() { _ = logFile.Close() }()

	ctx.LogToFile(logFile)

	dagStore, err := ctx.dagStore(dagStoreConfig{
		SearchPaths: []string{filepath.Dir(dag.Location)},
	})
	if err != nil {
		return err
	}

	as := ctx.agentStores()

	ag := agent.New(
		dagRunID,
		dag,
		filepath.Dir(logFile.Name()),
		logFile.Name(),
		ctx.DAGRunMgr,
		dagStore,
		agent.Options{
			Dry:                        true,
			DAGRunStore:                ctx.DAGRunStore,
			ServiceRegistry:            ctx.ServiceRegistry,
			RootDAGRun:                 exec.NewDAGRunRef(dag.Name, dagRunID),
			PeerConfig:                 ctx.Config.Core.Peer,
			DefaultExecMode:            ctx.Config.DefaultExecMode,
			AgentConfigStore:           as.ConfigStore,
			AgentModelStore:            as.ModelStore,
			AgentMemoryStore:           as.MemoryStore,
			AgentSoulStore:             as.SoulStore,
			AgentOAuthManager:          as.OAuthManager,
			AgentRemoteContextResolver: as.ContextResolver,
		},
	)

	listenSignals(ctx, ag)

	if err := ag.Run(ctx); err != nil {
		return fmt.Errorf("failed to execute dag-run %s (dag-run ID: %s): %w", dag.Name, dagRunID, err)
	}

	ag.PrintSummary(ctx)

	return nil
}

// loadDAGForDryRun loads the DAG with parameters from flags or command-line arguments.
func loadDAGForDryRun(ctx *Context, args []string) (*core.DAG, error) {
	loadOpts := []spec.LoadOption{
		spec.WithBaseConfig(ctx.Config.Paths.BaseConfig),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
		spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
	}

	nameOverride, err := ctx.StringParam("name")
	if err != nil {
		return nil, fmt.Errorf("failed to get name override: %w", err)
	}
	if nameOverride != "" {
		loadOpts = append(loadOpts, spec.WithName(nameOverride))
	}

	if argsLenAtDash := ctx.Command.ArgsLenAtDash(); argsLenAtDash != -1 {
		loadOpts = append(loadOpts, spec.WithParams(args[argsLenAtDash:]))
	} else {
		params, err := ctx.StringParam("params")
		if err != nil {
			return nil, fmt.Errorf("failed to get parameters: %w", err)
		}
		loadOpts = append(loadOpts, spec.WithParams(stringutil.RemoveQuotes(params)))
	}

	dag, err := spec.Load(ctx, args[0], loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load DAG from %s: %w", args[0], err)
	}

	return dag, nil
}
