// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func createCommandShim(binDir, command, targetPath, platform string) (string, error) {
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create tools bin dir: %w", err)
	}

	shimPath := filepath.Join(binDir, commandShimName(command, targetPath, platform))
	if err := os.Remove(shimPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("replace command shim %q: %w", command, err)
	}

	if err := os.Link(targetPath, shimPath); err == nil {
		return shimPath, nil
	} else if !isWindowsPlatform(platform) {
		if linkErr := os.Symlink(targetPath, shimPath); linkErr == nil {
			return shimPath, nil
		}
	}

	if err := copyExecutable(targetPath, shimPath); err != nil {
		return "", fmt.Errorf("create command shim %q: %w", command, err)
	}
	return shimPath, nil
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
