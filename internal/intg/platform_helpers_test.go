// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/test/intgharness"
	"github.com/moby/moby/client"
)

func canonicalTestPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == '/' {
		drive := path[1]
		if ('a' <= drive && drive <= 'z') || ('A' <= drive && drive <= 'Z') {
			path = strings.ToUpper(string(drive)) + ":" + filepath.FromSlash(path[2:])
		}
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func intgTestTimeout(timeout time.Duration) time.Duration {
	return intgharness.ScaleTimeout(timeout)
}

func indentTestScript(script string, spaces int) string {
	script = strings.TrimPrefix(script, "\n")
	script = strings.TrimRight(script, "\n")
	if script == "" {
		return ""
	}

	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func requireLinuxContainerRuntime(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "windows" {
		return
	}

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("Docker integration tests require a Linux container runtime: %v", err)
	}
	defer func() { _ = dockerClient.Close() }()

	info, err := dockerClient.Info(context.Background(), client.InfoOptions{})
	if err != nil {
		t.Skipf("Docker integration tests require a Linux container runtime: %v", err)
	}

	if info.Info.OSType != "linux" {
		t.Skipf("Docker integration tests require a Linux container runtime, got %q", info.Info.OSType)
	}
}
