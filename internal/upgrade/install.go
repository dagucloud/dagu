// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mholt/archives"
)

// InstallOptions configures the installation operation.
type InstallOptions struct {
	ArchivePath     string // Downloaded .tar.gz
	TargetPath      string // Path to current dagu binary
	CreateBackup    bool
	ExpectedVersion string // for verification after install
}

// InstallResult contains information about the installation.
type InstallResult struct {
	BackupPath string
	Installed  bool
}

// Install extracts the binary from the archive and replaces the current binary.
func Install(ctx context.Context, opts InstallOptions) (*InstallResult, error) {
	result := &InstallResult{}

	tempDir, err := os.MkdirTemp("", "dagu-upgrade-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	if err := extractArchive(ctx, opts.ArchivePath, tempDir); err != nil {
		return nil, fmt.Errorf("failed to extract archive: %w", err)
	}

	binaryName := "dagu"
	if runtime.GOOS == "windows" {
		binaryName = "dagu.exe"
	}

	extractedBinary, err := findBinary(tempDir, binaryName)
	if err != nil {
		return nil, err
	}

	if opts.CreateBackup {
		backupPath := opts.TargetPath + ".bak"
		if _, err := os.Stat(backupPath); err == nil {
			// Backup already exists; use a timestamped name to avoid overwriting
			backupPath = fmt.Sprintf("%s.bak.%s", opts.TargetPath, time.Now().Format("20060102150405"))
		}
		if err := copyFile(opts.TargetPath, backupPath); err != nil {
			return nil, fmt.Errorf("failed to create backup: %w", err)
		}
		result.BackupPath = backupPath
	}

	if err := replaceBinary(extractedBinary, opts.TargetPath); err != nil {
		if result.BackupPath != "" {
			_ = copyFile(result.BackupPath, opts.TargetPath)
		}
		return nil, fmt.Errorf("failed to replace binary: %w", err)
	}

	result.Installed = true
	return result, nil
}

// extractArchive extracts a tar.gz archive to the destination directory.
func extractArchive(ctx context.Context, archivePath, destDir string) error {
	srcFile, err := os.Open(archivePath) //nolint:gosec // archivePath is from controlled internal source
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	format, _, err := archives.Identify(ctx, filepath.Base(archivePath), srcFile)
	if err != nil {
		return fmt.Errorf("failed to identify archive format: %w", err)
	}

	if _, err := srcFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to reset file position: %w", err)
	}

	extractor, ok := format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("archive format does not support extraction")
	}

	err = extractor.Extract(ctx, srcFile, func(_ context.Context, f archives.FileInfo) error {
		if f.IsDir() {
			return nil
		}

		name := filepath.Clean(f.NameInArchive)
		if strings.HasPrefix(name, "..") {
			return fmt.Errorf("invalid path in archive: %s", f.NameInArchive)
		}

		targetPath := filepath.Join(destDir, name)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
			return err
		}

		src, err := f.Open()
		if err != nil {
			return err
		}

		dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()) //nolint:gosec // targetPath is constructed from destDir which is a temp directory
		if err != nil {
			_ = src.Close()
			return err
		}

		_, copyErr := io.Copy(dst, src)
		_ = src.Close()
		_ = dst.Close()
		return copyErr
	})

	return err
}

// findBinary searches for the binary in the extracted directory.
func findBinary(dir, binaryName string) (string, error) {
	var found string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if d.Name() == binaryName {
			found = path
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("error searching for binary: %w", err)
	}

	if found == "" {
		return "", fmt.Errorf("binary %s not found in archive", binaryName)
	}

	return found, nil
}

// replaceBinary atomically replaces the target binary with the source.
func replaceBinary(src, target string) error {
	perm := os.FileMode(0755)
	if info, err := os.Stat(target); err == nil {
		perm = info.Mode().Perm()
	}

	if runtime.GOOS == "windows" {
		return replaceWindowsBinary(src, target, perm)
	}

	return replaceUnixBinary(src, target, perm)
}

// replaceUnixBinary replaces the binary on Unix systems.
func replaceUnixBinary(src, target string, perm os.FileMode) error {
	dir := filepath.Dir(target)
	tempFile, err := os.CreateTemp(dir, "dagu-new-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()

	if err := copyFile(src, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	// Set permissions
	if err := os.Chmod(tempPath, perm); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := os.Rename(tempPath, target); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	return nil
}

// replaceWindowsBinary replaces the binary on Windows systems.
// Mirrors the Unix approach: copy new binary to a temp file in the same
// directory, rename old binary to .old, then rename temp to target.
// This reduces the vulnerable window from a full copy to just two renames.
func replaceWindowsBinary(src, target string, perm os.FileMode) error {
	dir := filepath.Dir(target)
	tempFile, err := os.CreateTemp(dir, "dagu-new-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()

	if err := copyFile(src, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	if err := os.Chmod(tempPath, perm); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	oldPath := target + ".old"
	_ = os.Remove(oldPath)

	if err := os.Rename(target, oldPath); err != nil {
		if !os.IsNotExist(err) {
			_ = os.Remove(tempPath)
			return fmt.Errorf("failed to rename old binary: %w", err)
		}
	}

	if err := os.Rename(tempPath, target); err != nil {
		// Try to restore old binary
		_ = os.Rename(oldPath, target)
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	_ = os.Remove(oldPath)
	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src) //nolint:gosec // src is from controlled internal source
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source file: %w", err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode()) //nolint:gosec // dst is from controlled internal source
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() { _ = dstFile.Close() }()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

// GetExecutablePath returns the path to the currently running binary.
func GetExecutablePath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	return execPath, nil
}

// CheckWritePermission verifies we can write to the target directory.
func CheckWritePermission(targetPath string) error {
	dir := filepath.Dir(targetPath)
	tempFile, err := os.CreateTemp(dir, ".dagu-permission-check-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied: cannot write to %s (%s)", dir, permissionHint(runtime.GOOS))
		}
		return fmt.Errorf("cannot write to directory %s: %w", dir, err)
	}
	name := tempFile.Name()
	_ = tempFile.Close()
	_ = os.Remove(name)
	return nil
}

// permissionHint suggests how to rerun the upgrade with write access to the
// install directory.
func permissionHint(goos string) string {
	if goos == "windows" {
		return "try running as Administrator"
	}
	return "try running with sudo"
}

// VerifyBinary runs the installed binary with "version" argument to verify it works.
func VerifyBinary(binaryPath, expectedVersion string) error {
	cmd := exec.Command(binaryPath, "version") //nolint:gosec // binaryPath is the path we just installed to
	// Use CombinedOutput to capture both stdout and stderr (older versions write to stderr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("binary verification failed: %w", err)
	}
	if !strings.Contains(string(output), ExtractVersionFromTag(expectedVersion)) {
		return fmt.Errorf("version mismatch after install: expected %s in output, got: %s",
			ExtractVersionFromTag(expectedVersion), strings.TrimSpace(string(output)))
	}
	return nil
}
