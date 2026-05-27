// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/upgrade"
	"github.com/spf13/cobra"
)

// Upgrade command flags
var upgradeFlags = []commandLineFlag{
	{name: "check", usage: "Only check if an update is available", isBool: true},
	{name: "version", shorthand: "v", usage: "Upgrade to specific version (e.g., v1.30.0)"},
	{name: "dry-run", usage: "Show what would happen without making changes", isBool: true},
	{name: "backup", usage: "Create backup of current binary before upgrade", isBool: true},
	yesFlag,
	{name: "force", shorthand: "f", usage: "Allow downgrading to an older version", isBool: true},
	{name: "pre-release", usage: "Include pre-release versions", isBool: true},
}

// Upgrade returns the upgrade command.
func Upgrade() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "upgrade [flags]",
			Short: "Upgrade dagu to the latest version",
			Long: `Upgrade the dagu binary to the latest version or a specified version.

This command downloads the latest (or specified) release from GitHub, verifies
the checksum, and replaces the current binary.

Examples:
  dagu upgrade                    # Upgrade to latest version
  dagu upgrade --check            # Check if an update is available
  dagu upgrade --version v1.30.0  # Upgrade to specific version
  dagu upgrade --dry-run          # Show what would happen
  dagu upgrade --backup           # Create backup before upgrade
  dagu upgrade -y                 # Skip confirmation prompt
  dagu upgrade -f                 # Allow downgrade to older version
  dagu upgrade -y -f              # Skip prompt and allow downgrade

Note: This command cannot be used if dagu was installed via Homebrew, Snap,
go install, or is running in Docker. Use the appropriate package manager instead.`,
		},
		upgradeFlags,
		runUpgrade,
	)
}

func runUpgrade(ctx *Context, _ []string) error {
	checkOnly, err := ctx.Command.Flags().GetBool("check")
	if err != nil {
		return fmt.Errorf("failed to get check flag: %w", err)
	}

	targetVersion, err := ctx.StringParam("version")
	if err != nil {
		return fmt.Errorf("failed to get version flag: %w", err)
	}

	dryRun, err := ctx.Command.Flags().GetBool("dry-run")
	if err != nil {
		return fmt.Errorf("failed to get dry-run flag: %w", err)
	}

	createBackup, err := ctx.Command.Flags().GetBool("backup")
	if err != nil {
		return fmt.Errorf("failed to get backup flag: %w", err)
	}

	skipConfirm, err := ctx.Command.Flags().GetBool("yes")
	if err != nil {
		return fmt.Errorf("failed to get yes flag: %w", err)
	}

	forceDowngrade, err := ctx.Command.Flags().GetBool("force")
	if err != nil {
		return fmt.Errorf("failed to get force flag: %w", err)
	}

	includePreRelease, err := ctx.Command.Flags().GetBool("pre-release")
	if err != nil {
		return fmt.Errorf("failed to get pre-release flag: %w", err)
	}

	canUpgrade, reason := upgrade.CanSelfUpgrade()
	if !canUpgrade {
		return fmt.Errorf("%s", reason)
	}

	upgradeStore, err := file.NewUpgradeCheckStore(ctx.Config)
	if err != nil {
		return fmt.Errorf("failed to create upgrade check store: %w", err)
	}

	opts := upgrade.Options{
		TargetVersion:     targetVersion,
		CheckOnly:         checkOnly,
		DryRun:            dryRun,
		CreateBackup:      createBackup,
		Force:             forceDowngrade,
		IncludePreRelease: includePreRelease,
	}

	releaseInfo, err := upgrade.FetchReleaseInfo(ctx, opts)
	if err != nil {
		return err
	}

	checkOpts := opts
	checkOpts.DryRun = true
	result, err := upgrade.UpgradeWithReleaseInfo(ctx, checkOpts, releaseInfo, upgradeStore)
	if err != nil {
		return err
	}

	if checkOnly {
		fmt.Print(upgrade.FormatCheckResult(result))
		return nil
	}

	if !result.UpgradeNeeded && !forceDowngrade {
		fmt.Println("Already running the latest version.")
		return nil
	}

	if dryRun {
		fmt.Print(upgrade.FormatResult(result))
		return nil
	}

	if !skipConfirm {
		fmt.Printf("Current version: %s\n", result.CurrentVersion)
		fmt.Printf("Target version: %s\n\n", result.TargetVersion)
		fmt.Println("The following changes will be made:")
		fmt.Printf("  - Download: %s (%s)\n", result.AssetName, upgrade.FormatBytes(result.AssetSize))
		fmt.Println("  - Verify:   SHA256 checksum")
		fmt.Printf("  - Replace:  %s\n\n", result.ExecutablePath)

		if !confirmAction("Continue?") {
			fmt.Println("Upgrade cancelled.")
			return nil
		}
	}

	opts.OnProgress = createProgressCallback()
	result, err = upgrade.UpgradeWithReleaseInfo(ctx, opts, releaseInfo, upgradeStore)
	if err != nil {
		return err
	}

	if result.WasUpgraded {
		fmt.Printf("\nSuccessfully upgraded to %s\n", result.TargetVersion)
		if result.BackupPath != "" {
			fmt.Printf("Backup created: %s\n", result.BackupPath)
		}
	}

	return nil
}

// createProgressCallback returns a callback function for download progress display.
func createProgressCallback() func(downloaded, total int64) {
	var lastPercent int
	return func(downloaded, total int64) {
		if total <= 0 {
			return
		}
		percent := int(downloaded * 100 / total)
		// Reset on retry: percent only decreases when a fresh download starts
		if percent < lastPercent {
			lastPercent = 0
		}
		// Update display every 5%
		if percent/5 > lastPercent/5 {
			lastPercent = percent
			fmt.Printf("\rDownloading... %d%% (%s / %s)",
				percent, upgrade.FormatBytes(downloaded), upgrade.FormatBytes(total))
		}
		if downloaded >= total {
			fmt.Println()
		}
	}
}

// confirmAction prompts the user for confirmation.
func confirmAction(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s [y/N]: ", prompt)

	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}
