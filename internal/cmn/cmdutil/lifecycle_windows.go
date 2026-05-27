// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

import (
	"errors"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsManagedProcess struct {
	job        windows.Handle
	assigned   bool
	degraded   bool
	released   bool
	releaseErr error
}

func newManagedPlatformProcess() managedPlatformProcess {
	return &windowsManagedProcess{}
}

func (p *windowsManagedProcess) prepare(*exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		p.degraded = true
		return nil
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		p.degraded = true
		return nil
	}

	p.job = job
	return nil
}

func (p *windowsManagedProcess) afterStart(cmd *exec.Cmd) error {
	if p.job == 0 || cmd == nil || cmd.Process == nil {
		p.degraded = true
		return nil
	}

	var assignErr error
	if err := cmd.Process.WithHandle(func(handle uintptr) {
		assignErr = windows.AssignProcessToJobObject(p.job, windows.Handle(handle))
	}); err != nil {
		assignErr = err
	}
	if assignErr != nil {
		p.degraded = true
		return nil
	}
	p.assigned = true
	return nil
}

func (p *windowsManagedProcess) stop(cmd *exec.Cmd, req StopRequest) (StopOutcome, error) {
	outcome := StopOutcome{
		RequestedMode: req.Intent.Mode,
		AppliedMode:   TerminationModeForce,
		Mechanism:     StopMechanismProcessTree,
		Contained:     false,
		Partial:       p.degraded,
		Reason:        req.Reason,
	}
	if cmd == nil || cmd.Process == nil {
		outcome.AppliedMode = req.Intent.Mode
		outcome.Mechanism = StopMechanismNone
		outcome.Contained = true
		return outcome, nil
	}
	if p.job != 0 && p.assigned {
		outcome.Mechanism = StopMechanismJobObject
		outcome.Contained = true
		if err := windows.TerminateJobObject(p.job, 1); err == nil {
			return outcome, nil
		} else if fallbackErr := killProcessTree(uint32(cmd.Process.Pid)); fallbackErr != nil {
			outcome.Mechanism = StopMechanismProcessTree
			outcome.Contained = false
			outcome.Partial = true
			return outcome, errors.Join(err, fallbackErr)
		}
		outcome.Mechanism = StopMechanismProcessTree
		outcome.Contained = false
		outcome.Partial = true
		return outcome, nil
	}
	if err := killProcessTree(uint32(cmd.Process.Pid)); err != nil {
		return outcome, err
	}
	return outcome, nil
}

func (p *windowsManagedProcess) release() error {
	if p == nil || p.released {
		if p == nil {
			return nil
		}
		return p.releaseErr
	}
	p.released = true
	if p.job != 0 {
		p.releaseErr = windows.CloseHandle(p.job)
		p.job = 0
	}
	return p.releaseErr
}

// TerminateProcessGroup stops the process tree on Windows systems according to
// the requested lifecycle intent.
func TerminateProcessGroup(cmd *exec.Cmd, intent TerminationIntent) error {
	_, err := NewManagedProcess(cmd).Stop(StopRequest{Intent: intent})
	return err
}

// KillProcessGroup kills the process and its subprocess tree on Windows systems.
//
// Deprecated: use TerminateProcessGroup with a TerminationIntent.
func KillProcessGroup(cmd *exec.Cmd, sig os.Signal) error {
	return TerminateProcessGroup(cmd, TerminationFromSignal(sig))
}

// TerminateMultipleProcessGroups stops multiple process trees on Windows systems.
func TerminateMultipleProcessGroups(cmds map[string]*exec.Cmd, intent TerminationIntent) error {
	var lastErr error
	for _, cmd := range cmds {
		if err := TerminateProcessGroup(cmd, intent); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// KillMultipleProcessGroups kills multiple processes on Windows systems.
//
// Deprecated: use TerminateMultipleProcessGroups with a TerminationIntent.
func KillMultipleProcessGroups(cmds map[string]*exec.Cmd, sig os.Signal) error {
	return TerminateMultipleProcessGroups(cmds, TerminationFromSignal(sig))
}
