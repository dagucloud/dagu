// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/spf13/cobra"
)

// PruneArtifacts creates and returns a cobra command for reclaiming orphaned
// artifact directories.
func PruneArtifacts() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "prune-artifacts [flags]",
			Short: "Remove artifact directories no surviving DAG run points to",
			Long: `Remove artifact directories and index records that no surviving DAG run
points to.

Run history is normally what removes artifacts, so directories are orphaned
when a run record is deleted by another route or the artifact root moved.
Only the artifact layout is examined: <root>/YYYY/MM/DD/<run> directories
with their index records, and pre-date <root>/<dag>/dag-run_<ts>_<id>
directories. Liveness is decided by name: an entry is removed only when no
run in the current history tree could still claim it, and only when it is
older than --older-than. A minimum age of 1h is always enforced because a
run's artifact directory exists before its record does. A root that holds
the run history or the log directory is refused.

Flags:
  -t, --older-than   Only remove entries older than a duration (e.g. 10d,
                     24h, 1w; default: 24h, minimum enforced: 1h)
      --root         Artifact root to prune (default: configured
                     paths.artifact_dir). Pass a previous artifacts
                     directory (e.g. <old data_dir>/artifacts) or a DAG's
                     artifacts.dir
      --dry-run      Preview what would be removed without removing
  -y, --yes          Skip confirmation prompt

Examples:
  dagu prune-artifacts --dry-run                  # Preview orphans under the configured root
  dagu prune-artifacts -t 7d -y                   # Remove orphans older than 7 days
  dagu prune-artifacts --root /old/data/artifacts # Reclaim artifacts left at an old data_dir
`,
			Args: cobra.NoArgs,
		},
		pruneArtifactsFlags,
		runPruneArtifacts,
	)
}

var pruneArtifactsFlags = []commandLineFlag{
	pruneArtifactsOlderThanFlag,
	pruneArtifactsRootFlag,
	dryRunFlag,
	yesFlag,
}

func runPruneArtifacts(ctx *Context, _ []string) error {
	dryRun, _ := ctx.Command.Flags().GetBool("dry-run")
	skipConfirm, _ := ctx.Command.Flags().GetBool("yes")

	olderThan, err := ctx.StringParam("older-than")
	if err != nil {
		return fmt.Errorf("failed to get older-than: %w", err)
	}
	dur, err := parseRelativeDuration(olderThan)
	if err != nil {
		return fmt.Errorf("invalid --older-than value %q: %w. Valid formats: 7d, 24h, 1w", olderThan, err)
	}
	if dur <= 0 {
		return fmt.Errorf("invalid --older-than value %q: must be greater than zero", olderThan)
	}

	root, err := ctx.StringParam("root")
	if err != nil {
		return fmt.Errorf("failed to get root: %w", err)
	}
	root = fileutil.ResolvePathOrBlank(root)

	displayRoot := root
	if displayRoot == "" {
		displayRoot = ctx.Config.Paths.ArtifactDir
	}

	req := persis.ArtifactPruneRequest{
		Root:          root,
		ProtectedDirs: []string{ctx.Config.Paths.LogDir},
		OlderThan:     persis.NewUTC(time.Now().UTC().Add(-dur)),
		DryRun:        dryRun,
	}
	repo := ctx.Persistence.DAGRunRepository

	if !dryRun && !skipConfirm {
		// The preview only informs the prompt; the sweep below re-evaluates
		// every entry and reports what it actually removed.
		preview := req
		preview.DryRun = true
		found, err := repo.PruneArtifacts(ctx, preview)
		if err != nil {
			return fmt.Errorf("failed to prune artifacts: %w", err)
		}
		if len(found.Dirs)+len(found.Records) == 0 {
			fmt.Printf("No orphaned artifacts older than %s under %s\n", olderThan, displayRoot)
			return nil
		}
		fmt.Printf("Found %d orphaned artifact director(ies) and %d index record(s) older than %s under %s.\n",
			len(found.Dirs), len(found.Records), olderThan, displayRoot)
		fmt.Println("Run with --dry-run to list them.")
		if !confirmAction("Delete them?") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	result, err := repo.PruneArtifacts(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to prune artifacts: %w", err)
	}

	total := len(result.Dirs) + len(result.Records)
	if dryRun {
		if total == 0 {
			fmt.Printf("Dry run: no orphaned artifacts to remove under %s\n", displayRoot)
			return nil
		}
		fmt.Printf("Dry run: would remove %d artifact director(ies) and %d index record(s) under %s:\n",
			len(result.Dirs), len(result.Records), displayRoot)
		for _, dir := range result.Dirs {
			fmt.Printf("  - %s\n", dir)
		}
		for _, record := range result.Records {
			fmt.Printf("  - %s\n", record)
		}
		return nil
	}

	if !ctx.Quiet {
		fmt.Printf("Removed %d artifact director(ies) and %d index record(s) under %s\n",
			len(result.Dirs), len(result.Records), displayRoot)
	}
	return nil
}
