// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/dagucloud/dagu/internal/cmn/config"
)

// Options configures the upgrade operation.
type Options struct {
	TargetVersion     string // Empty = latest
	CheckOnly         bool
	DryRun            bool
	CreateBackup      bool
	Force             bool
	IncludePreRelease bool
	OnProgress        func(downloaded, total int64)
}

// ReleaseInfo contains pre-fetched release data to avoid multiple API calls.
type ReleaseInfo struct {
	Release       *Release
	Checksums     map[string]string
	Asset         *Asset
	TargetVersion *semver.Version
}

// Result contains the result of an upgrade operation.
type Result struct {
	CurrentVersion         string
	TargetVersion          string
	UpgradeNeeded          bool
	WasUpgraded            bool
	BackupPath             string
	DryRun                 bool
	AssetName              string
	AssetSize              int64
	DownloadURL            string
	ExecutablePath         string
	SpecificVersionRequest bool
}

// InstallMethod represents how dagu was installed.
type InstallMethod int

const (
	InstallMethodUnknown InstallMethod = iota
	InstallMethodBinary
	InstallMethodHomebrew
	InstallMethodSnap
	InstallMethodDocker
	InstallMethodGoInstall
)

var installMethodNames = map[InstallMethod]string{
	InstallMethodUnknown:   "unknown",
	InstallMethodBinary:    "binary",
	InstallMethodHomebrew:  "homebrew",
	InstallMethodSnap:      "snap",
	InstallMethodDocker:    "docker",
	InstallMethodGoInstall: "go install",
}

// String returns a human-readable name for the install method.
func (m InstallMethod) String() string {
	if name, ok := installMethodNames[m]; ok {
		return name
	}
	return "unknown"
}

// DetectInstallMethod checks how dagu was installed.
func DetectInstallMethod() InstallMethod {
	execPath, err := GetExecutablePath()
	if err != nil {
		return InstallMethodUnknown
	}

	// Homebrew: path contains "/Cellar/" or "/homebrew/"
	if strings.Contains(execPath, "/Cellar/") || strings.Contains(execPath, "/homebrew/") {
		return InstallMethodHomebrew
	}

	// Snap: path starts with "/snap/" or SNAP env var exists
	if strings.HasPrefix(execPath, "/snap/") || os.Getenv("SNAP") != "" {
		return InstallMethodSnap
	}

	// Docker: /.dockerenv exists
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return InstallMethodDocker
	}

	// Go install: path in GOPATH/bin or GOBIN
	gopath := os.Getenv("GOPATH")
	gobin := os.Getenv("GOBIN")
	if gobin != "" && strings.HasPrefix(execPath, gobin) {
		return InstallMethodGoInstall
	}
	if gopath != "" && strings.HasPrefix(execPath, filepath.Join(gopath, "bin")) {
		return InstallMethodGoInstall
	}

	return InstallMethodBinary
}

// CanSelfUpgrade returns true if dagu can perform a self-upgrade.
func CanSelfUpgrade() (bool, string) {
	method := DetectInstallMethod()
	switch method {
	case InstallMethodHomebrew:
		return false, "Installed via Homebrew. Use 'brew upgrade dagu' instead."
	case InstallMethodSnap:
		return false, "Installed via Snap. Use 'snap refresh dagu' instead."
	case InstallMethodDocker:
		return false, "Running in Docker. Pull the latest image instead."
	case InstallMethodGoInstall:
		return false, "Installed via go install. Use 'go install github.com/dagucloud/dagu@latest' instead."
	case InstallMethodUnknown, InstallMethodBinary:
		return true, ""
	}
	return true, ""
}

// formatVersion ensures the version has a "v" prefix for consistent display.
func formatVersion(v string) string {
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// FormatResult formats the upgrade result for display.
func FormatResult(r *Result) string {
	var sb strings.Builder

	if r.DryRun {
		sb.WriteString("Dry run - no changes will be made\n\n")
	}

	fmt.Fprintf(&sb, "Current version: %s\n", formatVersion(r.CurrentVersion))
	fmt.Fprintf(&sb, "Target version: %s\n", r.TargetVersion)

	if !r.UpgradeNeeded && !r.WasUpgraded {
		sb.WriteString("\nAlready running the latest version.\n")
		return sb.String()
	}

	if r.DryRun {
		sb.WriteString("\nThe following changes will be made:\n")
		fmt.Fprintf(&sb, "  - Download: %s (%s)\n", r.AssetName, FormatBytes(r.AssetSize))
		sb.WriteString("  - Verify:   SHA256 checksum\n")
		fmt.Fprintf(&sb, "  - Replace:  %s\n", r.ExecutablePath)
		return sb.String()
	}

	if r.WasUpgraded {
		sb.WriteString("\nUpgrade successful!\n")
		if r.BackupPath != "" {
			fmt.Fprintf(&sb, "Backup created: %s\n", r.BackupPath)
		}
	}

	return sb.String()
}

// FormatCheckResult formats the check result for display.
func FormatCheckResult(r *Result) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Current version: %s\n", formatVersion(r.CurrentVersion))
	label := "Latest version"
	if r.SpecificVersionRequest {
		label = "Target version"
	}
	fmt.Fprintf(&sb, "%s: %s\n", label, r.TargetVersion)

	if r.UpgradeNeeded {
		sb.WriteString("\nAn update is available. Run 'dagu upgrade' to update.\n")
	} else {
		sb.WriteString("\nYou are running the latest version.\n")
	}

	return sb.String()
}

// FetchReleaseInfo fetches all release information in a single set of API calls.
// This allows checking and upgrading without making duplicate requests.
// Note: Callers are expected to check CanSelfUpgrade() before calling this.
func FetchReleaseInfo(ctx context.Context, opts Options) (*ReleaseInfo, error) {
	// Check platform support
	platform := Detect()
	if !platform.IsSupported() {
		return nil, fmt.Errorf("platform %s is not supported\n%s", platform, SupportedPlatformsMessage())
	}

	// Create GitHub client
	client := NewGitHubClient()

	// Fetch target release
	var release *Release
	var err error
	if opts.TargetVersion != "" {
		release, err = client.GetRelease(ctx, opts.TargetVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch release %s: %w", opts.TargetVersion, err)
		}
	} else {
		release, err = client.GetLatestRelease(ctx, opts.IncludePreRelease)
		if err != nil {
			return nil, err
		}
	}

	// Parse target version
	targetV, err := ParseVersion(release.TagName)
	if err != nil {
		return nil, fmt.Errorf("failed to parse target version: %w", err)
	}

	// Find asset for current platform
	asset, err := FindAsset(release, platform, release.TagName)
	if err != nil {
		return nil, err
	}

	// Get checksums
	checksums, err := client.GetChecksums(ctx, release)
	if err != nil {
		return nil, fmt.Errorf("failed to get checksums: %w", err)
	}

	return &ReleaseInfo{
		Release:       release,
		Checksums:     checksums,
		Asset:         asset,
		TargetVersion: targetV,
	}, nil
}

// UpgradeWithReleaseInfo performs the upgrade using pre-fetched release information.
func UpgradeWithReleaseInfo(ctx context.Context, opts Options, info *ReleaseInfo, store CacheStore) (*Result, error) {
	result := &Result{
		CurrentVersion:         config.Version,
		DryRun:                 opts.DryRun,
		TargetVersion:          info.Release.TagName,
		AssetName:              info.Asset.Name,
		AssetSize:              info.Asset.Size,
		DownloadURL:            info.Asset.BrowserDownloadURL,
		SpecificVersionRequest: opts.TargetVersion != "",
	}

	// Get executable path
	execPath, err := GetExecutablePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}
	result.ExecutablePath = execPath

	// Parse current version
	currentV, err := ParseVersion(config.Version)
	if err != nil {
		return nil, fmt.Errorf("cannot determine current version: %w", err)
	}

	// Compare versions
	result.UpgradeNeeded = IsNewer(currentV, info.TargetVersion)

	// If check-only, return result now
	if opts.CheckOnly {
		return result, nil
	}

	// If not an upgrade (same or older), exit unless forced
	if !result.UpgradeNeeded && !opts.Force {
		return result, nil
	}

	// If dry-run, return plan without making changes
	if opts.DryRun {
		return result, nil
	}

	// Check write permission early (fail fast)
	if err := CheckWritePermission(execPath); err != nil {
		return nil, err
	}

	// Get expected checksum
	expectedHash, ok := info.Checksums[info.Asset.Name]
	if !ok {
		return nil, fmt.Errorf("checksum for %s not found", info.Asset.Name)
	}

	// Create temp directory for download
	tempDir, err := os.MkdirTemp("", "dagu-upgrade-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create internal backup of current binary (for restore on verify failure)
	internalBackupPath := filepath.Join(tempDir, "dagu.prev")
	if err := copyFile(execPath, internalBackupPath); err != nil {
		return nil, fmt.Errorf("failed to create internal backup: %w", err)
	}

	archivePath := filepath.Join(tempDir, info.Asset.Name)

	// Download archive
	if err := Download(ctx, DownloadOptions{
		URL:          info.Asset.BrowserDownloadURL,
		Destination:  archivePath,
		ExpectedHash: expectedHash,
		OnProgress:   opts.OnProgress,
	}); err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}

	// Install (extract + replace)
	installResult, err := Install(ctx, InstallOptions{
		ArchivePath:     archivePath,
		TargetPath:      execPath,
		CreateBackup:    opts.CreateBackup,
		ExpectedVersion: info.Release.TagName,
	})
	if err != nil {
		return nil, fmt.Errorf("installation failed: %w", err)
	}

	result.WasUpgraded = installResult.Installed
	result.BackupPath = installResult.BackupPath

	// Verify the installed binary
	if err := VerifyBinary(execPath, info.Release.TagName); err != nil {
		// Prefer user backup when available; fall back to internal backup
		restoreSrc := internalBackupPath
		if result.BackupPath != "" {
			restoreSrc = result.BackupPath
		}
		restoreErr := copyFile(restoreSrc, execPath)
		if restoreErr == nil {
			return nil, fmt.Errorf("upgrade verification failed (restored backup): %w", err)
		}
		return nil, fmt.Errorf("upgrade verification failed (restore also failed: %v): %w", restoreErr, err)
	}

	// Update cache with new version info
	_ = store.Save(&UpgradeCheckCache{
		LastCheck:       time.Now(),
		CurrentVersion:  info.Release.TagName,
		LatestVersion:   info.Release.TagName,
		UpdateAvailable: false,
	})

	return result, nil
}
