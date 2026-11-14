// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package desktop

import (
	"os/exec"
	"syscall"
)

// startDetached starts the command in its own session so signals sent to
// the caller's process group do not reach it.
func startDetached(dir, command string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(command, args...) //nolint:gosec // starting the configured application is the purpose
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, cmd.Start()
}
