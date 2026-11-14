// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package desktop

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts the command outside the caller's job object when the
// job allows it, so closing the job does not end the application.
func startDetached(dir, command string, args []string) (*exec.Cmd, error) {
	cmd := newCommand(dir, command, args, windows.CREATE_NEW_PROCESS_GROUP|windows.CREATE_BREAKAWAY_FROM_JOB)
	err := cmd.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The job forbids breaking away; start the application inside it.
		cmd = newCommand(dir, command, args, windows.CREATE_NEW_PROCESS_GROUP)
		err = cmd.Start()
	}
	return cmd, err
}

func newCommand(dir, command string, args []string, flags uint32) *exec.Cmd {
	cmd := exec.Command(command, args...) //nolint:gosec // starting the configured application is the purpose
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	return cmd
}
