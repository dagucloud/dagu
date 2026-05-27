// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmdutil

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedProcessForceStopTerminatesCommand(t *testing.T) {
	cmd := longRunningCommand()
	proc, err := StartManagedProcess(cmd)
	require.NoError(t, err)
	defer func() { _ = proc.Release() }()

	outcome, err := proc.Stop(StopRequest{
		Intent: ForceTermination(),
		Reason: StopReasonTimeout,
	})
	require.NoError(t, err)
	require.Equal(t, TerminationModeForce, outcome.RequestedMode)
	require.Equal(t, TerminationModeForce, outcome.AppliedMode)
	require.NotEmpty(t, outcome.Mechanism)

	done := make(chan error, 1)
	go func() {
		done <- proc.Wait()
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("managed process did not exit after force stop")
	}
}

func TestManagedProcessStopNilCommandIsNoop(t *testing.T) {
	proc := NewManagedProcess(nil)

	outcome, err := proc.Stop(StopRequest{
		Intent: GracefulTermination(nil),
		Reason: StopReasonCancel,
	})

	require.NoError(t, err)
	require.Equal(t, TerminationModeGraceful, outcome.RequestedMode)
	require.Equal(t, TerminationModeGraceful, outcome.AppliedMode)
	require.Equal(t, StopMechanismNone, outcome.Mechanism)
}

func TestManagedProcessReleaseIsIdempotent(t *testing.T) {
	cmd := quickCommand()
	proc, err := StartManagedProcess(cmd)
	require.NoError(t, err)
	require.NoError(t, proc.Wait())

	require.NoError(t, proc.Release())
	require.NoError(t, proc.Release())
}

func TestStartManagedProcessCleansUpAfterContainmentFailure(t *testing.T) {
	cmd := longRunningCommand()

	_, err := startManagedProcess(cmd, failingAfterStartPlatform{
		err: errors.New("containment failed"),
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "failed to contain process")
	require.ErrorContains(t, err, "containment failed")
	require.NotNil(t, cmd.ProcessState)
}

type failingAfterStartPlatform struct {
	err error
}

func (p failingAfterStartPlatform) prepare(*exec.Cmd) error {
	return nil
}

func (p failingAfterStartPlatform) afterStart(*exec.Cmd) error {
	return p.err
}

func (p failingAfterStartPlatform) stop(*exec.Cmd, StopRequest) (StopOutcome, error) {
	return StopOutcome{}, nil
}

func (p failingAfterStartPlatform) release() error {
	return nil
}

func longRunningCommand() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 30")
	}
	return exec.Command("sleep", "30")
}

func quickCommand() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", "exit", "0")
	}
	return exec.CommandContext(context.Background(), "sh", "-c", "exit 0")
}
