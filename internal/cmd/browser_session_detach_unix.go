// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmd

import (
	"os/exec"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
)

// startDetached starts the command in its own session, so the caller's
// terminal or process group ending does not end it. It runs in the
// command's own directory rather than the caller's, which it would
// otherwise keep in use.
func startDetached(command string, args []string) error {
	cmd := exec.Command(command, args...) //nolint:gosec // starting this executable's own watchdog is the purpose
	cmd.Dir = filepath.Dir(command)
	cmd.SysProcAttr = cmdutil.DetachAttr(true)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
