// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/spf13/cobra"
)

// Cleanup creates and returns a cobra command for removing old DAG run history.
func Cleanup() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "cleanup [flags] <DAG name>",
			Short: "Remove old DAG run history",
			Long: `Remove old DAG run history for a specified DAG.

By default, removes all history except for currently active runs.
Use --retention-days to keep recent history.

Active runs are never deleted for safety.

Examples:
  dagu cleanup my-workflow                      # Delete all history (with confirmation)
  dagu cleanup --retention-days 30 my-workflow  # Keep last 30 days
  dagu cleanup --dry-run my-workflow            # Preview what would be deleted
  dagu cleanup -y my-workflow                   # Skip confirmation
`,
			Args: cobra.ExactArgs(1),
		},
		cleanupFlags,
		runCleanup,
	)
}

var cleanupFlags = []commandLineFlag{
	retentionDaysFlag,
	dryRunFlag,
	yesFlag,
}

func runCleanup(ctx *Context, args []string) error {
	dagName := args[0]

	// Parse retention days (flags are string-based in this codebase)
	retentionStr, err := ctx.StringParam("retention-days")
	if err != nil {
		return fmt.Errorf("failed to get retention-days: %w", err)
	}
	retentionDays, err := strconv.Atoi(retentionStr)
	if err != nil {
		return fmt.Errorf("invalid retention-days value %q: must be a non-negative integer", retentionStr)
	}

	// Reject negative retention (clearer error than silent no-op)
	if retentionDays < 0 {
		return fmt.Errorf("retention-days cannot be negative (got %d)", retentionDays)
	}

	// Get boolean flags
	dryRun, _ := ctx.Command.Flags().GetBool("dry-run")
	skipConfirm, _ := ctx.Command.Flags().GetBool("yes")

	// Build description message
	var actionDesc string
	if retentionDays == 0 {
		actionDesc = fmt.Sprintf("all history for DAG %q", dagName)
	} else {
		actionDesc = fmt.Sprintf("history older than %d days for DAG %q", retentionDays, dagName)
	}

	// Build options for RemoveOldDAGRuns
	var opts []exec.RemoveOldDAGRunsOption
	if dryRun {
		opts = append(opts, exec.WithDryRun())
	}

	// Dry run mode - show what would be deleted
	if dryRun {
		runIDs, err := ctx.DAGRunStore.RemoveOldDAGRuns(ctx, dagName, retentionDays, opts...)
		if err != nil {
			return fmt.Errorf("failed to check history for %q: %w", dagName, err)
		}

		if len(runIDs) == 0 {
			fmt.Printf("Dry run: No runs to delete for DAG %q\n", dagName)
		} else {
			fmt.Printf("Dry run: Would delete %d run(s) for DAG %q:\n", len(runIDs), dagName)
			for _, runID := range runIDs {
				fmt.Printf("  - %s\n", runID)
			}
		}
		return nil
	}

	// Confirmation prompt (unless --yes or --quiet)
	if !skipConfirm && !ctx.Quiet {
		fmt.Printf("This will delete %s.\n", actionDesc)
		fmt.Println("Active runs will be preserved.")
		fmt.Print("Continue? [y/N]: ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read user input: %w", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))

		if response != "y" && response != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	// Execute cleanup using the existing DAGRunStore method
	runIDs, err := ctx.DAGRunStore.RemoveOldDAGRuns(ctx, dagName, retentionDays, opts...)
	if err != nil {
		return fmt.Errorf("failed to cleanup history for %q: %w", dagName, err)
	}

	if !ctx.Quiet {
		if len(runIDs) == 0 {
			fmt.Printf("No runs to delete for DAG %q\n", dagName)
		} else {
			fmt.Printf("Successfully removed %d run(s) for DAG %q\n", len(runIDs), dagName)
		}
	}

	return nil
}
