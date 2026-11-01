// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmd

import (
	"errors"
	"os/exec"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"golang.org/x/sys/windows"
)

// startDetached starts the command without a console, outside the caller's
// job object when the job allows it, so closing the job does not end it. It
// runs in the command's own directory rather than the caller's, which
// Windows would keep from being removed while it runs.
func startDetached(command string, args []string) error {
	err := startWithBreakaway(command, args, true)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The job forbids breaking away; start the command inside it.
		err = startWithBreakaway(command, args, false)
	}
	return err
}

func startWithBreakaway(command string, args []string, breakaway bool) error {
	cmd := exec.Command(command, args...) //nolint:gosec // starting this executable's own watchdog is the purpose
	cmd.Dir = filepath.Dir(command)
	cmd.SysProcAttr = cmdutil.DetachAttr(breakaway)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
