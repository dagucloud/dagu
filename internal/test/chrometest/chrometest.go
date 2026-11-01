// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package chrometest finds the Chrome that browser tests drive.
package chrometest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Path returns the Chrome executable CHROME_PATH names, or an installed
// Chrome or Chromium, or "" without one.
func Path() string {
	if path := os.Getenv("CHROME_PATH"); path != "" {
		return path
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium"}
	case "windows":
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	default:
		for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium-browser", "chromium"} {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil { //nolint:gosec // standard install locations
			return candidate
		}
	}
	return ""
}

// Require returns Chrome's path. It skips the test when no Chrome is
// installed, except in CI, where the runners provide one and a missing
// browser is a setup failure.
func Require(t testing.TB) string {
	t.Helper()
	if path := Path(); path != "" {
		return path
	}
	if os.Getenv("CI") != "" {
		t.Fatal("Chrome is required in CI; set CHROME_PATH")
	}
	t.Skip("Chrome is not installed; set CHROME_PATH to run browser tests")
	return ""
}
