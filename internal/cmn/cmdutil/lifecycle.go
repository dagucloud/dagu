// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmdutil

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

// ManagedProcess owns the lifecycle for one local OS process.
type ManagedProcess struct {
	mu sync.Mutex

	cmd       *exec.Cmd
	platform  managedPlatformProcess
	stopWatch func()

	releaseOnce sync.Once
	releaseErr  error
}

type managedPlatformProcess interface {
	prepare(*exec.Cmd) error
	afterStart(*exec.Cmd) error
	stop(*exec.Cmd, StopRequest) (StopOutcome, error)
	release() error
}

// NewManagedProcess wraps an already-created command for lifecycle operations.
func NewManagedProcess(cmd *exec.Cmd) *ManagedProcess {
	return &ManagedProcess{
		cmd:      cmd,
		platform: newManagedPlatformProcess(),
	}
}

// StartManagedProcess configures, starts, and contains cmd for lifecycle management.
func StartManagedProcess(cmd *exec.Cmd) (*ManagedProcess, error) {
	return startManagedProcess(cmd, newManagedPlatformProcess())
}

func startManagedProcess(cmd *exec.Cmd, platform managedPlatformProcess) (*ManagedProcess, error) {
	proc := &ManagedProcess{
		cmd:      cmd,
		platform: platform,
	}
	if cmd == nil {
		return proc, nil
	}

	SetupCommand(cmd)
	if err := proc.platform.prepare(cmd); err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = proc.platform.release()
		return nil, err
	}
	if err := proc.platform.afterStart(cmd); err != nil {
		waitErr := stopAndWaitStartedCommand(cmd)
		releaseErr := proc.platform.release()
		return nil, fmt.Errorf("failed to contain process: %w", errors.Join(err, waitErr, releaseErr))
	}

	stopWatch, err := StartParentExitWatcher(cmd)
	if err != nil {
		_, _ = proc.Stop(StopRequest{Intent: ForceTermination(), Reason: StopReasonParentExit})
		_ = cmd.Wait()
		_ = proc.platform.release()
		return nil, fmt.Errorf("failed to start parent exit watcher: %w", err)
	}
	proc.stopWatch = stopWatch
	return proc, nil
}

// PID returns the root process ID, or zero when no process is attached.
func (p *ManagedProcess) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Command returns the wrapped command.
func (p *ManagedProcess) Command() *exec.Cmd {
	if p == nil {
		return nil
	}
	return p.cmd
}

// Wait waits for the root process to exit.
func (p *ManagedProcess) Wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}

// Stop requests that the platform adapter stop the process.
func (p *ManagedProcess) Stop(req StopRequest) (StopOutcome, error) {
	req = req.normalize()
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return noopStopOutcome(req), nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.platform.stop(p.cmd, req)
}

// Release releases lifecycle resources. It is safe to call multiple times.
func (p *ManagedProcess) Release() error {
	if p == nil {
		return nil
	}
	p.releaseOnce.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		if p.stopWatch != nil {
			p.stopWatch()
		}
		p.releaseErr = p.platform.release()
	})
	return p.releaseErr
}

func stopAndWaitStartedCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	var errs []error
	if err := cmd.Process.Kill(); err != nil {
		errs = append(errs, fmt.Errorf("kill started process: %w", err))
	}
	if err := cmd.Wait(); err != nil {
		errs = append(errs, fmt.Errorf("wait for started process: %w", err))
	}
	return errors.Join(errs...)
}
