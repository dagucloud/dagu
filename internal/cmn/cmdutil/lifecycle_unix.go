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

type unixManagedProcess struct{}

func newManagedPlatformProcess() managedPlatformProcess {
	return unixManagedProcess{}
}

func (unixManagedProcess) prepare(*exec.Cmd) error {
	return nil
}

func (unixManagedProcess) afterStart(*exec.Cmd) error {
	return nil
}

func (unixManagedProcess) release() error {
	return nil
}

func (unixManagedProcess) stop(cmd *exec.Cmd, req StopRequest) (StopOutcome, error) {
	outcome := StopOutcome{
		RequestedMode: req.Intent.Mode,
		AppliedMode:   req.Intent.Mode,
		Mechanism:     StopMechanismProcessGroup,
		Contained:     true,
		Reason:        req.Reason,
	}
	if cmd == nil || cmd.Process == nil {
		outcome.Mechanism = StopMechanismNone
		return outcome, nil
	}
	if req.Intent.IsForce() {
		return outcome, syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	sig := req.Intent.Signal
	if sig == nil {
		sig = syscall.SIGTERM
	}
	sysSig, ok := sig.(syscall.Signal)
	if !ok {
		return outcome, fmt.Errorf("unsupported process signal %T", sig)
	}
	return outcome, syscall.Kill(-cmd.Process.Pid, sysSig)
}

// TerminateProcessGroup stops the process group on Unix systems according to
// the requested lifecycle intent.
func TerminateProcessGroup(cmd *exec.Cmd, intent TerminationIntent) error {
	_, err := NewManagedProcess(cmd).Stop(StopRequest{Intent: intent})
	return err
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
		if err := TerminateProcessGroup(cmd, intent); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// KillMultipleProcessGroups kills multiple processes on Unix systems.
//
// Deprecated: use TerminateMultipleProcessGroups with a TerminationIntent.
func KillMultipleProcessGroups(cmds map[string]*exec.Cmd, sig os.Signal) error {
	return TerminateMultipleProcessGroups(cmds, TerminationFromSignal(sig))
}
