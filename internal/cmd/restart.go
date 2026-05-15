// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/spf13/cobra"
)

func Restart() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "restart [flags] <DAG name>",
			Short: "Restart a running DAG-run with a new ID",
			Long: `Stop a currently running DAG-run and immediately restart it with the same configuration but with a new DAG-run ID.

It first gracefully stops the active DAG-run, ensuring all resources are properly released, then
initiates a new DAG-run with identical parameters.

Flags:
  --run-id string (optional) Unique identifier of the DAG-run to restart. If not provided,
                             the command will find the current running DAG-run by the given DAG name.

Example:
  dagu restart --run-id=abc123 my_dag
`,
			Args: cobra.ExactArgs(1),
		}, restartFlags, runRestart,
	)
}

var restartFlags = []commandLineFlag{dagRunIDFlagRestart, scheduleTimeFlag}

func runRestart(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunRestart(ctx, args)
	}
	dagRunID, err := ctx.StringParam("run-id")
	if err != nil {
		return fmt.Errorf("failed to get dag-run ID: %w", err)
	}
	scheduleTime, err := parseScheduleTimeParam(ctx)
	if err != nil {
		return err
	}

	name := args[0]

	var attempt exec.DAGRunAttempt
	if dagRunID != "" {
		// Retrieve the previous run for the specified dag-run ID.
		dagRunRef := exec.NewDAGRunRef(name, dagRunID)
		attempt, err = ctx.DAGRunStore.FindAttempt(ctx, dagRunRef)
		if err != nil {
			return fmt.Errorf("failed to find the run for dag-run ID %s: %w", dagRunID, err)
		}
	} else {
		attempt, err = ctx.DAGRunStore.LatestAttempt(ctx, name)
		if err != nil {
			return fmt.Errorf("failed to find the latest execution history for DAG %s: %w", name, err)
		}
	}

	dagStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to read status: %w", err)
	}
	if dagStatus.Status != core.Running {
		return fmt.Errorf("DAG %s is not running, current status: %s", name, dagStatus.Status)
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("failed to read DAG from execution history: %w", err)
	}

	dag, err = restoreDAGFromStatus(ctx.Context, dag, dagStatus)
	if err != nil {
		return fmt.Errorf("failed to restore DAG from status: %w", err)
	}

	if err := handleRestartProcess(ctx, dag, dagRunID, scheduleTime); err != nil {
		return fmt.Errorf("restart process failed for DAG %s: %w", dag.Name, err)
	}

	return nil
}

func handleRestartProcess(ctx *Context, d *core.DAG, oldDagRunID string, scheduleTime string) error {
	if err := stopDAGIfRunning(ctx, ctx.DAGRunMgr, d, oldDagRunID); err != nil {
		return err
	}

	if d.RestartWait > 0 {
		logger.Info(ctx, "Waiting for restart", tag.Duration(d.RestartWait))
		time.Sleep(d.RestartWait)
	}

	newDagRunID, err := genRunID()
	if err != nil {
		return fmt.Errorf("failed to generate dag-run ID: %w", err)
	}

	return withPreparedLocalExecution(
		ctx,
		d,
		newDagRunID,
		exec.NewDAGRunRef(d.Name, newDagRunID),
		exec.DAGRunRef{},
		core.TriggerTypeUnknown,
		scheduleTime,
		func(execCtx context.Context) (exec.DAGRunAttempt, error) {
			return ctx.DAGRunStore.CreateAttempt(execCtx, d, time.Now(), newDagRunID, exec.NewDAGRunAttemptOptions{})
		},
		func(preparedAttempt exec.DAGRunAttempt) error {
			return executeDAGWithRunID(ctx, ctx.DAGRunMgr, d, newDagRunID, scheduleTime, preparedAttempt)
		},
	)
}

// executeDAGWithRunID executes a DAG with a pre-generated run ID.
func executeDAGWithRunID(ctx *Context, cli runtime.Manager, dag *core.DAG, dagRunID string, scheduleTime string, preparedAttempt exec.DAGRunAttempt) error {
	logFile, err := ctx.OpenLogFile(dag, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize log file: %w", err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	ctx.LogToFile(logFile)

	ctx.Context = logger.WithValues(ctx.Context, tag.DAG(dag.Name), tag.RunID(dagRunID))

	logger.Info(ctx, "Dag-run restart initiated", tag.File(logFile.Name()))

	artifactDir, err := ctx.GenArtifactDir(dag, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize artifact directory: %w", err)
	}

	dr, err := ctx.dagStore(dagStoreConfig{
		SearchPaths: []string{filepath.Dir(dag.Location)},
	})
	if err != nil {
		return fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	as := ctx.agentStores()

	agentInstance := agent.New(
		dagRunID,
		dag,
		filepath.Dir(logFile.Name()),
		logFile.Name(),
		cli,
		dr,
		agent.Options{
			Dry:                        false,
			PreparedAttempt:            preparedAttempt,
			DAGRunStore:                ctx.DAGRunStore,
			SecretStore:                as.SecretStore,
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
			ScheduleTime:               scheduleTime,
			ArtifactDir:                artifactDir,
		})

	listenSignals(ctx, agentInstance)
	if err := agentInstance.Run(ctx); err != nil {
		if !ctx.Quiet {
			agentInstance.PrintSummary(ctx)
		}
		return fmt.Errorf("dag-run failed: %w", err)
	}

	return nil
}

func stopDAGIfRunning(ctx context.Context, cli runtime.Manager, dag *core.DAG, dagRunID string) error {
	dagStatus, err := cli.GetCurrentStatus(ctx, dag, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to get current status: %w", err)
	}

	if dagStatus.Status == core.Running {
		logger.Info(ctx, "Stopping DAG", tag.DAG(dag.Name))
		if err := stopRunningDAG(ctx, cli, dag, dagRunID); err != nil {
			return fmt.Errorf("failed to stop running DAG: %w", err)
		}
	}
	return nil
}

func stopRunningDAG(ctx context.Context, cli runtime.Manager, dag *core.DAG, dagRunID string) error {
	const stopPollInterval = 100 * time.Millisecond
	for {
		dagStatus, err := cli.GetCurrentStatus(ctx, dag, dagRunID)
		if err != nil {
			return fmt.Errorf("failed to get current status: %w", err)
		}

		if dagStatus.Status != core.Running {
			return nil
		}

		if err := cli.Stop(ctx, dag, dagRunID); err != nil {
			return err
		}

		time.Sleep(stopPollInterval)
	}
}
