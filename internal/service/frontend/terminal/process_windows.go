// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package terminal

import (
	"os/exec"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
)

// requestHangup is intentionally a no-op on Windows because there is no
// portable SIGHUP equivalent. terminateProcess therefore falls back to
// forceKillProcess once its grace period expires.
func requestHangup(_ *exec.Cmd) error {
	return nil
}

func forceKillProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmdutil.TerminateProcessGroup(cmd, cmdutil.ForceTermination())
}
