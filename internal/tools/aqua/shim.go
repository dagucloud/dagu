// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func createCommandShim(binDir, command, targetPath, platform string) (string, error) {
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create tools bin dir: %w", err)
	}

	shimPath := filepath.Join(binDir, commandShimName(command, targetPath, platform))
	matches, err := shimMatchesTarget(shimPath, targetPath)
	if err != nil {
		return "", fmt.Errorf("inspect command shim %q: %w", command, err)
	}
	if matches {
		return shimPath, nil
	}

	tmpPath := filepath.Join(binDir, fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(shimPath), os.Getpid(), time.Now().UnixNano()))
	if err := createShimFile(tmpPath, targetPath, platform); err != nil {
		return "", fmt.Errorf("create command shim %q: %w", command, err)
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if err := replaceShimFile(tmpPath, shimPath, platform); err != nil {
		return "", fmt.Errorf("replace command shim %q: %w", command, err)
	}
	return shimPath, nil
}

func shimMatchesTarget(shimPath, targetPath string) (bool, error) {
	info, err := os.Lstat(shimPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, err := os.Readlink(shimPath)
		if err != nil {
			return false, err
		}
		if !filepath.IsAbs(linkTarget) {
			linkTarget = filepath.Join(filepath.Dir(shimPath), linkTarget)
		}
		return filepath.Clean(linkTarget) == filepath.Clean(targetPath), nil
	}

	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return false, err
	}
	shimInfo, err := os.Stat(shimPath)
	if err != nil {
		return false, err
	}
	return os.SameFile(shimInfo, targetInfo), nil
}

func createShimFile(shimPath, targetPath, platform string) error {
	if err := os.Link(targetPath, shimPath); err == nil {
		return nil
	}
	if !isWindowsPlatform(platform) {
		if err := os.Symlink(targetPath, shimPath); err == nil {
			return nil
		}
	}
	return copyExecutable(targetPath, shimPath)
}

func replaceShimFile(src, dst, platform string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isWindowsPlatform(platform) {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(src, dst)
}

func commandShimName(command, targetPath, platform string) string {
	if !isWindowsPlatform(platform) || filepath.Ext(command) != "" {
		return command
	}
	if ext := filepath.Ext(targetPath); ext != "" {
		return command + ext
	}
	return command
}

func isWindowsPlatform(platform string) bool {
	return strings.HasPrefix(strings.ToLower(platform), "windows/")
}

func copyExecutable(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source executable: %w", err)
	}
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open source executable: %w", err)
	}
	defer func() {
		_ = in.Close()
	}()

	mode := info.Mode().Perm()
	if mode&0o111 == 0 {
		mode |= 0o500
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create shim executable: %w", err)
	}
	defer func() {
		_ = out.Close()
	}()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy shim executable: %w", err)
	}
	if err := out.Chmod(mode); err != nil {
		return fmt.Errorf("chmod shim executable: %w", err)
	}
	return nil
}
