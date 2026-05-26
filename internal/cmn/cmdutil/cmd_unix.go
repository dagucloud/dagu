// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmdutil

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// SetupCommand configures Unix-specific command attributes
func SetupCommand(cmd *exec.Cmd) {
	setupCommand(cmd)
}

// setupCommand configures Unix-specific command attributes
func setupCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
}

// TerminateProcessGroup stops the process group on Unix systems according to
// the requested lifecycle intent.
func TerminateProcessGroup(cmd *exec.Cmd, intent TerminationIntent) error {
	if cmd != nil && cmd.Process != nil {
		if intent.IsForce() {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		sig, ok := intent.Signal.(syscall.Signal)
		if !ok {
			return fmt.Errorf("unsupported process signal %T", intent.Signal)
		}
		return syscall.Kill(-cmd.Process.Pid, sig)
	}
	return nil
}

// KillProcessGroup kills the process group on Unix systems.
//
// Deprecated: use TerminateProcessGroup with a TerminationIntent.
func KillProcessGroup(cmd *exec.Cmd, sig os.Signal) error {
	return TerminateProcessGroup(cmd, TerminationFromSignal(sig))
}

// TerminateMultipleProcessGroups stops multiple process groups on Unix systems.
func TerminateMultipleProcessGroups(cmds map[string]*exec.Cmd, intent TerminationIntent) error {
	var lastErr error
	for _, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			if err := TerminateProcessGroup(cmd, intent); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// KillMultipleProcessGroups kills multiple process groups on Unix systems.
//
// Deprecated: use TerminateMultipleProcessGroups with a TerminationIntent.
func KillMultipleProcessGroups(cmds map[string]*exec.Cmd, sig os.Signal) error {
	return TerminateMultipleProcessGroups(cmds, TerminationFromSignal(sig))
}
