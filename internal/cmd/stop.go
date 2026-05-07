// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/spf13/cobra"
)

func Stop() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "stop [flags] <DAG name>",
			Short: "Stop active DAG-runs or cancel pending auto-retry by run ID",
			Long: `Gracefully terminate an active DAG-run instance.

This command sends termination signals to all running tasks of the specified DAG-run,
ensuring resources are properly released and cleanup handlers are executed. It waits
for tasks to complete their shutdown procedures before exiting.

When --run-id is provided, the command can also cancel a failed root DAG-run that is
still pending DAG-level auto-retry. In that case the latest failed attempt is marked
as aborted so the scheduler will not enqueue another retry.

Flags:
  --run-id string   (optional) Unique identifier of the DAG-run to stop.
                                   If not provided, it will stop the currently running
                                   DAG-run(s) for the given DAG definition name.

Example:
  dagu stop --run-id=abc123 my_dag
`,
			Args: cobra.ExactArgs(1),
		}, stopFlags, runStop,
	)
}

var stopFlags = []commandLineFlag{
	dagRunIDFlagStop,
}

func runStop(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunStop(ctx, args)
	}
	dagRunID, err := ctx.StringParam("run-id")
	if err != nil {
		return fmt.Errorf("failed to get dag-run ID: %w", err)
	}

	name, err := extractDAGName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}

	var dag *core.DAG
	if dagRunID != "" {
		// Retrieve the previous run's history record for the specified dag-run ID.
		ref := exec.NewDAGRunRef(name, dagRunID)
		rec, err := ctx.DAGRunStore.FindAttempt(ctx, ref)
		if err != nil {
			return fmt.Errorf("failed to find the record for dag-run ID %s: %w", dagRunID, err)
		}

		d, err := rec.ReadDAG(ctx)
		if err != nil {
			return fmt.Errorf("failed to read DAG from history record: %w", err)
		}
		dag = d
	} else {
		d, err := spec.Load(ctx, args[0],
			spec.WithBaseConfig(ctx.Config.Paths.BaseConfig),
			spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
			spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
		)
		if err != nil {
			return fmt.Errorf("failed to load DAG from %s: %w", args[0], err)
		}
		dag = d
	}

	logger.Info(ctx, "Dag-run stop/cancel requested", tag.DAG(dag.Name))

	if err := ctx.DAGRunMgr.Stop(ctx, dag, dagRunID); err != nil {
		return fmt.Errorf("failed to stop or cancel DAG: %w", err)
	}

	logger.Info(ctx, "Dag-run stop/cancel completed", tag.DAG(dag.Name))
	return nil
}
