// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/spf13/cobra"
)

// Rm creates and returns a cobra command for removing DAG history and/or definitions.
func Rm() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "rm [flags] <DAG>",
			Short: "Remove DAG run history and/or DAG definition",
			Long: `Remove DAG run history and/or the DAG YAML definition.

At least one of --history (-H) or --definition (-d) is required.

Flags:
  -H, --history       Delete run history for the DAG
  -d, --definition    Delete the DAG YAML definition
  -t, --older-than    With --history: delete runs older than a duration
                      (e.g. 10d, 24h, 1w). Omitted = delete all history
  -f, --force         Skip confirmation prompt
      --dry-run       Preview what would be deleted without deleting

Active runs are never deleted from history. Definition deletion is refused
while the DAG has alive processes.

With --definition, identify the DAG by filename, stem, or configured path.

Examples:
  dagu rm -H my-workflow                 # Delete all history (with confirmation)
  dagu rm -H -t 10d my-workflow          # Delete history older than 10 days
  dagu rm -H -t 24h -f my-workflow       # Delete history older than 24h, no prompt
  dagu rm -H --dry-run my-workflow       # Preview history removals without deleting
  dagu rm -d my-workflow                 # Delete DAG YAML definition
  dagu rm -H -d my-workflow              # Delete history and definition
`,
			Args: cobra.ExactArgs(1),
		},
		rmFlags,
		runRm,
	)
}

var rmFlags = []commandLineFlag{
	rmHistoryFlag,
	rmDefinitionFlag,
	rmOlderThanFlag,
	rmForceFlag,
	dryRunFlag,
}

type rmOptions struct {
	dagName       string
	definitionID  string
	deleteHist    bool
	deleteDef     bool
	olderThan     string
	dryRun        bool
	skipConfirm   bool
	retentionDays *int // when set (cleanup alias), use day-based retention instead of olderThan
}

func runRm(ctx *Context, args []string) error {
	deleteHist, _ := ctx.Command.Flags().GetBool("history")
	deleteDef, _ := ctx.Command.Flags().GetBool("definition")
	force, _ := ctx.Command.Flags().GetBool("force")
	dryRun, _ := ctx.Command.Flags().GetBool("dry-run")
	olderThan, err := ctx.StringParam("older-than")
	if err != nil {
		return fmt.Errorf("failed to get older-than: %w", err)
	}

	if !deleteHist && !deleteDef {
		return fmt.Errorf("at least one of --history (-H) or --definition (-d) is required")
	}
	if olderThan != "" && !deleteHist {
		return fmt.Errorf("--older-than (-t) requires --history (-H)")
	}

	var dagName string
	definitionID := ""
	if deleteDef {
		dag, err := ctx.DAGRepository.GetMetadata(ctx, args[0])
		if err != nil {
			return fmt.Errorf("failed to resolve DAG definition %q: %w", args[0], err)
		}
		dagName = dag.Name
		definitionID = args[0]
	} else {
		dagName, err = extractDAGName(ctx, args[0])
		if err != nil {
			return fmt.Errorf("failed to extract DAG name: %w", err)
		}
	}

	return executeRm(ctx, rmOptions{
		dagName:      dagName,
		definitionID: definitionID,
		deleteHist:   deleteHist,
		deleteDef:    deleteDef,
		olderThan:    olderThan,
		dryRun:       dryRun,
		skipConfirm:  force,
	})
}

func executeRm(ctx *Context, opts rmOptions) error {
	if opts.deleteDef {
		if err := ensureNoActiveRuns(ctx, opts.dagName); err != nil {
			return err
		}
	}

	if opts.dryRun {
		return previewRm(ctx, opts)
	}

	actionDesc := buildRmActionDesc(opts)

	if !opts.skipConfirm {
		fmt.Printf("This will delete %s.\n", actionDesc)
		if opts.deleteHist {
			fmt.Println("Active runs will be preserved.")
		}
		if !confirmAction("Continue?") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if opts.deleteHist {
		runIDs, err := removeHistory(ctx, opts)
		if err != nil {
			return err
		}
		if !ctx.Quiet {
			if len(runIDs) == 0 {
				fmt.Printf("No runs to delete for DAG %q\n", opts.dagName)
			} else {
				fmt.Printf("Successfully removed %d run(s) for DAG %q\n", len(runIDs), opts.dagName)
			}
		}
	}

	if opts.deleteDef {
		if err := ensureNoActiveRuns(ctx, opts.dagName); err != nil {
			return err
		}
		if err := ctx.DAGRepository.Delete(ctx, opts.definitionID); err != nil {
			return fmt.Errorf("failed to delete DAG definition %q: %w", opts.dagName, err)
		}
		if !ctx.Quiet {
			fmt.Printf("Successfully deleted DAG definition %q\n", opts.dagName)
		}
	}

	return nil
}

func buildRmActionDesc(opts rmOptions) string {
	var parts []string
	if opts.deleteHist {
		switch {
		case opts.retentionDays != nil && *opts.retentionDays > 0:
			parts = append(parts, fmt.Sprintf("history older than %d days for DAG %q", *opts.retentionDays, opts.dagName))
		case opts.olderThan != "":
			parts = append(parts, fmt.Sprintf("history older than %s for DAG %q", opts.olderThan, opts.dagName))
		default:
			parts = append(parts, fmt.Sprintf("all history for DAG %q", opts.dagName))
		}
	}
	if opts.deleteDef {
		parts = append(parts, fmt.Sprintf("DAG definition %q", opts.dagName))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + " and " + parts[1]
}

func previewRm(ctx *Context, opts rmOptions) error {
	if !opts.deleteHist {
		if !ctx.Quiet {
			fmt.Printf("Dry run: would delete DAG definition %q\n", opts.dagName)
		}
		return nil
	}

	runIDs, err := removeHistory(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to check history for %q: %w", opts.dagName, err)
	}

	if len(runIDs) == 0 {
		fmt.Printf("Dry run: No runs to delete for DAG %q\n", opts.dagName)
	} else {
		fmt.Printf("Dry run: Would delete %d run(s) for DAG %q:\n", len(runIDs), opts.dagName)
		for _, runID := range runIDs {
			fmt.Printf("  - %s\n", runID)
		}
	}

	if opts.deleteDef && !ctx.Quiet {
		fmt.Printf("Dry run: Would also delete DAG definition %q\n", opts.dagName)
	}
	return nil
}

func removeHistory(ctx *Context, opts rmOptions) ([]string, error) {
	retentionDays := 0
	var removeOpts []dagrun.RemoveOldDAGRunsOption
	if opts.dryRun {
		removeOpts = append(removeOpts, dagrun.WithDryRun())
	}

	switch {
	case opts.retentionDays != nil:
		retentionDays = *opts.retentionDays
	case opts.olderThan != "":
		dur, err := parseRelativeDuration(opts.olderThan)
		if err != nil {
			return nil, fmt.Errorf("invalid --older-than value %q: %w. Valid formats: 7d, 24h, 1w", opts.olderThan, err)
		}
		cutoff := time.Now().UTC().Add(-dur)
		removeOpts = append(removeOpts, dagrun.WithOlderThan(cutoff))
	}

	runIDs, err := ctx.DAGRunStore.RemoveOldDAGRuns(ctx, opts.dagName, retentionDays, removeOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to remove history for %q: %w", opts.dagName, err)
	}
	return runIDs, nil
}

func ensureNoActiveRuns(ctx *Context, dagName string) error {
	if ctx.ProcStore != nil {
		entries, err := ctx.ProcStore.ListAllEntries(ctx)
		if err != nil {
			return fmt.Errorf("failed to check alive processes for %q: %w", dagName, err)
		}

		for _, entry := range entries {
			if entry.Fresh && entry.Meta.Name == dagName {
				return fmt.Errorf("cannot delete definition for %q: DAG has alive process (run-id=%s)", dagName, entry.Meta.DAGRunID)
			}
		}
	}

	if ctx.ActiveDistributedRunStore == nil {
		return nil
	}

	runs, err := ctx.ActiveDistributedRunStore.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to check active distributed runs for %q: %w", dagName, err)
	}
	for _, run := range runs {
		if run.Status.IsActive() && (run.DAGRun.Name == dagName || run.Root.Name == dagName) {
			return fmt.Errorf("cannot delete definition for %q: DAG has active distributed run (run-id=%s)", dagName, run.DAGRun.ID)
		}
	}
	return nil
}
