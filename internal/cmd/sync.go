// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/dagucloud/dagu/internal/gitsync"
	"github.com/spf13/cobra"
)

// Sync returns the sync command with subcommands for Git sync operations.
func Sync() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Git sync operations",
		Long: `Manage Git synchronization for DAG definitions.

Git sync allows you to synchronize your DAG definitions with a remote Git repository.
This enables version control, collaboration, and backup of your workflow definitions.

Available Commands:
  status    Show the current sync status
  pull      Pull changes from remote repository
  publish   Publish local changes to remote repository
  discard   Discard local changes for a DAG`,
	}

	cmd.AddCommand(syncStatus())
	cmd.AddCommand(syncPull())
	cmd.AddCommand(syncPublish())
	cmd.AddCommand(syncDiscard())
	cmd.AddCommand(syncForget())
	cmd.AddCommand(syncCleanup())
	cmd.AddCommand(syncDelete())
	cmd.AddCommand(syncMove())

	return cmd
}

// syncStatus shows the current Git sync status.
func syncStatus() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show Git sync status",
			Long: `Display the current status of Git synchronization.

Shows overall sync status including:
- Whether sync is enabled
- Repository and branch information
- Last sync time and status
- Per-DAG status (synced, modified, untracked, conflict)

Example:
  dagu sync status`,
			Args: cobra.NoArgs,
		},
		nil,
		runSyncStatus,
	)
}

func runSyncStatus(ctx *Context, _ []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	status, err := syncSvc.GetStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get sync status: %w", err)
	}

	if !status.Enabled {
		fmt.Println("Git sync is not enabled")
		fmt.Println("\nTo enable Git sync, configure the gitSync section in your config file")
		return nil
	}

	// Print overall status
	fmt.Printf("Repository:  %s\n", status.Repository)
	fmt.Printf("Branch:      %s\n", status.Branch)
	fmt.Printf("Status:      %s\n", status.Summary)

	if status.LastSyncAt != nil {
		fmt.Printf("Last Sync:   %s\n", status.LastSyncAt.Format("2006-01-02 15:04:05"))
	}
	if status.LastSyncCommit != "" {
		fmt.Printf("Last Commit: %s\n", status.LastSyncCommit[:min(8, len(status.LastSyncCommit))])
	}
	if status.LastError != nil {
		fmt.Printf("Last Error:  %s\n", *status.LastError)
	}

	// Print counts
	fmt.Printf("\nDAG Status Counts:\n")
	fmt.Printf("  Synced:    %d\n", status.Counts.Synced)
	fmt.Printf("  Modified:  %d\n", status.Counts.Modified)
	fmt.Printf("  Untracked: %d\n", status.Counts.Untracked)
	fmt.Printf("  Conflict:  %d\n", status.Counts.Conflict)
	fmt.Printf("  Missing:   %d\n", status.Counts.Missing)

	// Print per-DAG status if there are any non-synced DAGs
	if status.Counts.Modified > 0 || status.Counts.Untracked > 0 || status.Counts.Conflict > 0 || status.Counts.Missing > 0 {
		fmt.Printf("\nDAGs with pending changes:\n")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "  NAME\tSTATUS\tMODIFIED AT")

		// Collect and sort DAG names
		var names []string
		for name, dagState := range status.DAGs {
			if dagState.Status != gitsync.StatusSynced {
				names = append(names, name)
			}
		}
		slices.Sort(names)

		for _, name := range names {
			dagState := status.DAGs[name]
			modifiedAt := "-"
			if dagState.ModifiedAt != nil {
				modifiedAt = dagState.ModifiedAt.Format("2006-01-02 15:04:05")
			}
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", name, dagState.Status, modifiedAt)
		}
		_ = w.Flush()
	}

	return nil
}

// syncPull pulls changes from the remote repository.
func syncPull() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "pull",
			Short: "Pull changes from remote",
			Long: `Pull changes from the remote Git repository.

This command fetches and applies changes from the remote repository
to your local DAG definitions.

Example:
  dagu sync pull`,
			Args: cobra.NoArgs,
		},
		nil,
		runSyncPull,
	)
}

func runSyncPull(ctx *Context, _ []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	fmt.Println("Pulling changes from remote...")

	result, err := syncSvc.Pull(ctx)
	if err != nil {
		return fmt.Errorf("failed to pull: %w", err)
	}

	if result.Success {
		fmt.Println("Pull completed successfully")
		if len(result.Synced) > 0 {
			fmt.Printf("  Synced: %d DAGs\n", len(result.Synced))
		}
		if len(result.Modified) > 0 {
			fmt.Printf("  Modified: %d DAGs (local changes preserved)\n", len(result.Modified))
		}
		if len(result.Conflicts) > 0 {
			fmt.Printf("  Conflicts: %d DAGs (require resolution)\n", len(result.Conflicts))
		}
	} else {
		fmt.Printf("Pull completed with issues: %s\n", result.Message)
	}

	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.Errors {
			fmt.Printf("  - %s: %s\n", e.DAGID, e.Message)
		}
	}

	return nil
}

// syncPublish publishes local changes to the remote repository.
func syncPublish() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish [dag-name]",
		Short: "Publish changes to remote",
		Long: `Publish local changes to the remote Git repository.

When a DAG name is provided, only that DAG's changes are published.
When no DAG name is provided with --all flag, all modified DAGs are published.

Examples:
  dagu sync publish my_dag -m "Updated schedule"
  dagu sync publish --all -m "Batch update"`,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().StringP("message", "m", "", "Commit message")
	cmd.Flags().Bool("all", false, "Publish all modified DAGs")
	cmd.Flags().BoolP("force", "f", false, "Force publish even with conflicts")

	return NewCommand(cmd, nil, runSyncPublish)
}

func runSyncPublish(ctx *Context, args []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	message, _ := ctx.Command.Flags().GetString("message")
	publishAll, _ := ctx.Command.Flags().GetBool("all")
	force, _ := ctx.Command.Flags().GetBool("force")

	if len(args) == 0 && !publishAll {
		return fmt.Errorf("either provide a DAG name or use --all flag")
	}

	if len(args) > 0 && publishAll {
		return fmt.Errorf("cannot specify both a DAG name and --all flag")
	}

	var result *gitsync.SyncResult

	if publishAll {
		fmt.Println("Publishing all modified DAGs...")
		status, statusErr := syncSvc.GetStatus(ctx)
		if statusErr != nil {
			return fmt.Errorf("failed to get sync status: %w", statusErr)
		}
		var dagIDs []string
		for id, dagState := range status.DAGs {
			if dagState.Status == gitsync.StatusModified || dagState.Status == gitsync.StatusUntracked {
				dagIDs = append(dagIDs, id)
			}
		}
		sort.Strings(dagIDs)
		if len(dagIDs) == 0 {
			fmt.Println("No modified or untracked DAGs to publish")
			return nil
		}
		result, err = syncSvc.PublishAll(ctx, message, dagIDs)
	} else {
		fmt.Printf("Publishing DAG: %s...\n", args[0])
		result, err = syncSvc.Publish(ctx, args[0], message, force)
	}

	if err != nil {
		if gitsync.IsConflict(err) {
			fmt.Println("Conflict detected!")
			fmt.Println("The DAG has been modified on the remote since your last sync.")
			fmt.Println("Use --force to overwrite remote changes, or pull first to resolve.")
			return err
		}
		return fmt.Errorf("failed to publish: %w", err)
	}

	if result.Success {
		fmt.Println("Publish completed successfully")
		if len(result.Synced) > 0 {
			fmt.Printf("  Published: %d DAGs\n", len(result.Synced))
		}
	} else {
		fmt.Printf("Publish completed with issues: %s\n", result.Message)
	}

	if len(result.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range result.Errors {
			fmt.Printf("  - %s: %s\n", e.DAGID, e.Message)
		}
	}

	return nil
}

// syncDiscard discards local changes for a DAG.
func syncDiscard() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discard <dag-name>",
		Short: "Discard local changes",
		Long: `Discard local changes for a DAG and restore the remote version.

This command reverts local modifications to a DAG, restoring it to
the version from the remote repository.

WARNING: This will permanently discard your local changes!

Example:
  dagu sync discard my_dag
  dagu sync discard my_dag --yes`,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return NewCommand(cmd, nil, runSyncDiscard)
}

func runSyncDiscard(ctx *Context, args []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	dagName := args[0]
	skipConfirm, _ := ctx.Command.Flags().GetBool("yes")

	fmt.Printf("Discarding local changes for DAG: %s\n", dagName)
	fmt.Println("WARNING: This will permanently discard your local changes!")

	if !skipConfirm && !confirmAction("Are you sure?") {
		fmt.Println("Aborted")
		return nil
	}

	if err := syncSvc.Discard(ctx, dagName); err != nil {
		if gitsync.IsDAGNotFound(err) {
			return fmt.Errorf("DAG not found: %s", dagName)
		}
		return fmt.Errorf("failed to discard changes: %w", err)
	}

	fmt.Println("Changes discarded successfully")
	return nil
}

// syncForget removes state entries for missing/untracked/conflict items.
func syncForget() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forget <item-id> [item-id...]",
		Short: "Forget sync items",
		Long: `Remove state entries for missing, untracked, or conflict sync items.

This command removes items from the sync state without deleting them from the
remote repository. Only missing, untracked, and conflict items can be forgotten.

Example:
  dagu sync forget my_dag
  dagu sync forget my_dag other_dag --yes`,
		Args: cobra.MinimumNArgs(1),
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return NewCommand(cmd, nil, runSyncForget)
}

func runSyncForget(ctx *Context, args []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	skipConfirm, _ := ctx.Command.Flags().GetBool("yes")

	fmt.Printf("Forgetting %d item(s): %s\n", len(args), strings.Join(args, ", "))

	if !skipConfirm && !confirmAction("Are you sure?") {
		fmt.Println("Aborted")
		return nil
	}

	forgotten, err := syncSvc.Forget(ctx, args)
	if err != nil {
		return fmt.Errorf("failed to forget: %w", err)
	}

	fmt.Printf("Forgotten %d item(s)\n", len(forgotten))
	return nil
}

// syncCleanup removes all missing entries from state.
func syncCleanup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Cleanup missing sync items",
		Long: `Remove all missing entries from the sync state.

This command removes all items with "missing" status from the sync state.
These are items that were previously tracked but whose files have been deleted.

Example:
  dagu sync cleanup
  dagu sync cleanup --yes
  dagu sync cleanup --dry-run`,
		Args: cobra.NoArgs,
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("dry-run", false, "Show what would be cleaned up without making changes")

	return NewCommand(cmd, nil, runSyncCleanup)
}

func runSyncCleanup(ctx *Context, _ []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	dryRun, _ := ctx.Command.Flags().GetBool("dry-run")
	skipConfirm, _ := ctx.Command.Flags().GetBool("yes")

	if dryRun {
		// Get status to show what would be cleaned up
		status, err := syncSvc.GetStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to get sync status: %w", err)
		}

		var missing []string
		for id, dagState := range status.DAGs {
			if dagState.Status == gitsync.StatusMissing {
				missing = append(missing, id)
			}
		}
		slices.Sort(missing)

		if len(missing) == 0 {
			fmt.Println("No missing items to clean up")
			return nil
		}

		fmt.Printf("Would clean up %d missing item(s):\n", len(missing))
		for _, id := range missing {
			fmt.Printf("  - %s\n", id)
		}
		return nil
	}

	fmt.Println("This will remove all missing items from sync state.")
	if !skipConfirm && !confirmAction("Are you sure?") {
		fmt.Println("Aborted")
		return nil
	}

	forgotten, err := syncSvc.Cleanup(ctx)
	if err != nil {
		return fmt.Errorf("failed to cleanup: %w", err)
	}

	if len(forgotten) == 0 {
		fmt.Println("No missing items to clean up")
	} else {
		fmt.Printf("Cleaned up %d missing item(s)\n", len(forgotten))
		for _, id := range forgotten {
			fmt.Printf("  - %s\n", id)
		}
	}
	return nil
}

// syncDelete deletes items from remote, local disk, and state.
func syncDelete() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <item-id>",
		Short: "Delete a sync item from remote",
		Long: `Delete a sync item from the remote repository, local disk, and sync state.

This command performs a git rm + commit + push to remove the item from remote.
Untracked items cannot be deleted (use 'forget' instead).
Modified items require --force.

Use --all-missing to delete all missing items at once.

Examples:
  dagu sync delete my_dag -m "Remove old workflow"
  dagu sync delete my_dag --force -y
  dagu sync delete --all-missing -m "Clean up deleted items"
  dagu sync delete --all-missing --dry-run`,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().StringP("message", "m", "", "Commit message")
	cmd.Flags().Bool("force", false, "Force delete even with local modifications")
	cmd.Flags().Bool("all-missing", false, "Delete all missing items")
	cmd.Flags().Bool("dry-run", false, "Show what would be deleted without making changes")

	return NewCommand(cmd, nil, runSyncDelete)
}

func runSyncDelete(ctx *Context, args []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	allMissing, _ := ctx.Command.Flags().GetBool("all-missing")
	dryRun, _ := ctx.Command.Flags().GetBool("dry-run")
	message, _ := ctx.Command.Flags().GetString("message")
	force, _ := ctx.Command.Flags().GetBool("force")
	skipConfirm, _ := ctx.Command.Flags().GetBool("yes")

	if allMissing {
		if len(args) > 0 {
			return fmt.Errorf("cannot specify both an item ID and --all-missing")
		}

		if dryRun {
			status, err := syncSvc.GetStatus(ctx)
			if err != nil {
				return fmt.Errorf("failed to get sync status: %w", err)
			}
			var missing []string
			for id, dagState := range status.DAGs {
				if dagState.Status == gitsync.StatusMissing {
					missing = append(missing, id)
				}
			}
			slices.Sort(missing)
			if len(missing) == 0 {
				fmt.Println("No missing items to delete")
				return nil
			}
			fmt.Printf("Would delete %d missing item(s) from remote:\n", len(missing))
			for _, id := range missing {
				fmt.Printf("  - %s\n", id)
			}
			return nil
		}

		fmt.Println("This will delete all missing items from the remote repository.")
		if !skipConfirm && !confirmAction("Are you sure?") {
			fmt.Println("Aborted")
			return nil
		}

		deleted, err := syncSvc.DeleteAllMissing(ctx, message)
		if err != nil {
			return fmt.Errorf("failed to delete missing items: %w", err)
		}
		if len(deleted) == 0 {
			fmt.Println("No missing items to delete")
		} else {
			fmt.Printf("Deleted %d missing item(s) from remote\n", len(deleted))
			for _, id := range deleted {
				fmt.Printf("  - %s\n", id)
			}
		}
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("provide an item ID or use --all-missing")
	}

	itemID := args[0]

	if dryRun {
		fmt.Printf("Would delete:\n")
		fmt.Printf("  - %s\n", itemID)
		return nil
	}

	fmt.Printf("Deleting item: %s\n", itemID)
	fmt.Println("WARNING: This will delete the item from the remote repository!")

	if !skipConfirm && !confirmAction("Are you sure?") {
		fmt.Println("Aborted")
		return nil
	}

	if err := syncSvc.Delete(ctx, itemID, message, force); err != nil {
		return fmt.Errorf("failed to delete: %w", err)
	}

	fmt.Println("Item deleted successfully")
	return nil
}

// syncMove atomically renames an item across local, remote, and state.
func syncMove() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mv <old-id> <new-id>",
		Short: "Move/rename a sync item",
		Long: `Atomically rename a sync item across local filesystem, remote repository, and sync state.

Both source and destination must be of the same kind (DAG, memory, skill, soul).

Supports two modes:
  - Preemptive: source file exists on disk → reads it, writes to new location,
    stages removal+addition in repo, commits and pushes.
  - Retroactive: source is missing but new file already exists at destination →
    reads new file, stages old removal + new addition, commits and pushes.

Examples:
  dagu sync mv old_dag new_dag -m "Rename workflow"
  dagu sync mv old_dag new_dag --force -y
  dagu sync mv memory/OLD memory/NEW`,
		Args: cobra.ExactArgs(2),
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().StringP("message", "m", "", "Commit message")
	cmd.Flags().Bool("force", false, "Force move even with conflicts")
	cmd.Flags().Bool("dry-run", false, "Show what would be moved without making changes")

	return NewCommand(cmd, nil, runSyncMove)
}

func runSyncMove(ctx *Context, args []string) error {
	syncSvc, err := newSyncService(ctx)
	if err != nil {
		return err
	}

	oldID := args[0]
	newID := args[1]
	message, _ := ctx.Command.Flags().GetString("message")
	force, _ := ctx.Command.Flags().GetBool("force")
	dryRun, _ := ctx.Command.Flags().GetBool("dry-run")
	skipConfirm, _ := ctx.Command.Flags().GetBool("yes")

	if dryRun {
		status, err := syncSvc.GetStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to get sync status: %w", err)
		}
		oldState, exists := status.DAGs[oldID]
		if !exists {
			return fmt.Errorf("item not found: %s", oldID)
		}
		mode := "preemptive"
		if oldState.Status == gitsync.StatusMissing {
			mode = "retroactive"
		}
		fmt.Printf("Would move:\n")
		fmt.Printf("  - %s → %s (%s, currently %s)\n", oldID, newID, mode, oldState.Status)
		if !force && oldState.Status == gitsync.StatusConflict {
			fmt.Println("\nNote: source has conflict status; use --force to allow move")
		}
		return nil
	}

	fmt.Printf("Moving %s → %s\n", oldID, newID)

	if !skipConfirm && !confirmAction("Are you sure?") {
		fmt.Println("Aborted")
		return nil
	}

	if err := syncSvc.Move(ctx, oldID, newID, message, force); err != nil {
		return fmt.Errorf("failed to move: %w", err)
	}

	fmt.Println("Item moved successfully")
	return nil
}

// newSyncService creates a new GitSync service from the context configuration.
func newSyncService(ctx *Context) (gitsync.Service, error) {
	syncCfg := gitsync.NewConfigFromGlobal(ctx.Config.GitSync)
	if !syncCfg.Enabled {
		return nil, fmt.Errorf("git sync is not enabled, set gitSync.enabled=true in your config")
	}
	return gitsync.NewService(syncCfg, ctx.Config.Paths.DAGsDir, ctx.Config.Paths.DataDir, ctx.Config.Paths.BaseConfig), nil
}
