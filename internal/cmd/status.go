// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/output"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func Status() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "status [flags] <DAG name>",
			Short: "Display the current status of a DAG-run",
			Long: `Show real-time status information for a specified DAG-run instance.

This command retrieves and displays the current execution status of a DAG-run,
including its state (running, completed, failed), process ID, and other relevant details.

Flags:
  --run-id string (optional)     Unique identifier of the DAG-run to check.
                                 If not provided, it will show the status of the
                                 most recent DAG-run for the given name.
  --sub-run-id string (optional) Unique identifier of a sub DAG-run.
                                 Requires --run-id to be provided.
                                 Use this to check the status of nested DAG executions.

Example:
  dagu status --run-id=abc123 my_dag
  dagu status my_dag  # Shows status of the most recent DAG-run
  dagu status --run-id=abc123 --sub-run-id=def456 my_dag  # Shows status of a sub DAG-run
`,
			Args: cobra.ExactArgs(1),
		}, statusFlags, runStatus,
	)
}

var statusFlags = []commandLineFlag{
	dagRunIDFlagStatus,
	subDAGRunIDFlagStatus,
}

func runStatus(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunStatus(ctx, args)
	}
	dagRunID, err := ctx.StringParam("run-id")
	if err != nil {
		return fmt.Errorf("failed to get dag-run ID: %w", err)
	}

	subDAGRunID, err := ctx.StringParam("sub-run-id")
	if err != nil {
		return fmt.Errorf("failed to get sub-dag-run ID: %w", err)
	}

	if subDAGRunID != "" && dagRunID == "" {
		return fmt.Errorf("--sub-run-id requires --run-id to be provided (root DAG run context is needed)")
	}

	name, err := extractDAGName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}

	attempt, err := extractAttemptForStatus(ctx, name, dagRunID, subDAGRunID)
	if err != nil {
		return fmt.Errorf("failed to extract attempt: %w", err)
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("failed to read DAG from run data: %w", err)
	}

	dagStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to read status from attempt: %w", err)
	}

	// For running root DAGs, fetch real-time status via socket connection.
	// Sub DAGs use stored status since they may run on different workers.
	if dagStatus.Status == ir.Running && subDAGRunID == "" {
		// Use dagStatus.DAGRunID as fallback when --run-id flag is omitted
		runIDForLookup := dagRunID
		if runIDForLookup == "" {
			runIDForLookup = dagStatus.DAGRunID
		}
		realtimeStatus, err := ctx.DAGRunMgr.GetCurrentStatus(ctx, dag, runIDForLookup)
		if err != nil {
			return fmt.Errorf("failed to retrieve current status: %w", err)
		}
		if realtimeStatus.DAGRunID == dagStatus.DAGRunID {
			dagStatus = realtimeStatus
		}
	}

	displayTreeStatus(dag, dagStatus)

	return nil
}

func displayTreeStatus(dag *ir.DAG, dagStatus *ir.DAGRunStatus) {
	config := output.DefaultConfig()
	config.ColorEnabled = term.IsTerminal(int(os.Stdout.Fd()))

	renderer := output.NewRenderer(config)
	fmt.Print(renderer.RenderDAGStatus(dag, dagStatus))
}

// extractAttemptForStatus returns the appropriate Attempt based on the provided IDs.
// For sub DAG runs, it finds the nested attempt under the root run.
// For root runs, it finds either the specified run or the latest run.
func extractAttemptForStatus(ctx *Context, name, dagRunID, subDAGRunID string) (dagrun.Attempt, error) {
	if subDAGRunID != "" {
		dagRunRef := ir.NewDAGRunRef(name, dagRunID)
		attempt, err := ctx.DAGRunRepository.FindSubAttempt(ctx, dagRunRef, subDAGRunID)
		if err != nil {
			return nil, fmt.Errorf("failed to find sub dag-run with ID %s under root %s: %w",
				subDAGRunID, dagRunID, err)
		}
		return attempt, nil
	}

	if dagRunID != "" {
		dagRunRef := ir.NewDAGRunRef(name, dagRunID)
		attempt, err := ctx.DAGRunRepository.FindAttempt(ctx, dagRunRef)
		if err != nil {
			return nil, fmt.Errorf("failed to find run data for dag-run ID %s: %w", dagRunID, err)
		}
		return attempt, nil
	}

	attempt, err := ctx.DAGRunRepository.LatestAttempt(ctx, name, persis.DAGRunLatestAttemptOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to find the latest run data for DAG %s: %w", name, err)
	}
	return attempt, nil
}
