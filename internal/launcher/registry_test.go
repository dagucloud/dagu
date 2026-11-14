// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package launcher_test

import (
	"context"
	"os"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/launcher"
)

func longRunningSpec() launcher.CmdSpec {
	if goruntime.GOOS == "windows" {
		return launcher.CmdSpec{
			Executable: "powershell",
			Args:       []string{"-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 30"},
		}
	}
	return launcher.CmdSpec{Executable: "sleep", Args: []string{"30"}}
}

func quickSpec() launcher.CmdSpec {
	if goruntime.GOOS == "windows" {
		return launcher.CmdSpec{Executable: "cmd", Args: []string{"/C", "exit", "0"}}
	}
	return launcher.CmdSpec{Executable: "sh", Args: []string{"-c", "exit 0"}}
}

func TestProcessRegistryFromContext(t *testing.T) {
	t.Parallel()

	assert.Nil(t, launcher.ProcessRegistryFrom(context.Background()))

	reg := launcher.NewProcessRegistry()
	ctx := launcher.ContextWithProcessRegistry(context.Background(), reg)
	assert.Same(t, reg, launcher.ProcessRegistryFrom(ctx))
}

func TestPropagateSignalWithoutRegistryIsNoop(t *testing.T) {
	t.Parallel()

	select {
	case <-launcher.PropagateSignal(context.Background(), os.Interrupt):
	default:
		t.Fatal("propagation without a registry must already be complete")
	}
}

func TestProcessRegistryPropagatesSignal(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx := launcher.ContextWithProcessRegistry(context.Background(), reg)

	res, err := launcher.StartProcess(ctx, longRunningSpec())
	require.NoError(t, err)

	done := reg.Propagate(ctx, os.Interrupt)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("propagation did not complete after process exit")
	}

	select {
	case err := <-res.Done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("propagated signal did not terminate the tracked process")
	}

	// Propagation is one-shot; a second call must be a no-op.
	require.Equal(t, done, reg.Propagate(ctx, os.Interrupt))
}

func TestProcessRegistrySkipsExitedProcesses(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx := launcher.ContextWithProcessRegistry(context.Background(), reg)

	res, err := launcher.StartProcess(ctx, quickSpec())
	require.NoError(t, err)
	require.NoError(t, <-res.Done)

	// The exited command is untracked, so propagation is a no-op.
	select {
	case <-reg.Propagate(ctx, os.Interrupt):
	case <-time.After(5 * time.Second):
		t.Fatal("propagation waited for an exited process")
	}
}

func TestProcessesWithoutRegistryAreNotPropagated(t *testing.T) {
	reg := launcher.NewProcessRegistry()

	// The process is started without a registry on its context, so the
	// registry's propagation must not reach it.
	res, err := launcher.StartProcess(context.Background(), longRunningSpec())
	require.NoError(t, err)

	reg.Propagate(context.Background(), os.Interrupt)

	select {
	case <-res.Done:
		t.Fatal("untracked process received the propagated signal")
	case <-time.After(500 * time.Millisecond):
	}

	if proc, err := os.FindProcess(res.PID); err == nil {
		_ = proc.Kill()
	}
}

func TestRunWithCanceledRegistryContext(t *testing.T) {
	ctx, cancel := context.WithCancel(launcher.ContextWithProcessRegistry(t.Context(), launcher.NewProcessRegistry()))
	cancel()
	require.ErrorIs(t, launcher.Run(ctx, quickSpec()), context.Canceled)
}

func TestProcessRegistryLateStart(t *testing.T) {
	reg := launcher.NewProcessRegistry()
	ctx := launcher.ContextWithProcessRegistry(t.Context(), reg)
	done := reg.Propagate(ctx, os.Interrupt)
	result, err := launcher.StartProcess(ctx, longRunningSpec())
	require.NoError(t, err)
	require.Equal(t, done, reg.Propagate(ctx, os.Interrupt))
	select {
	case <-result.Done:
		t.Fatal("a run started after shutdown received the propagated signal")
	case <-time.After(100 * time.Millisecond):
	}
	proc, err := os.FindProcess(result.PID)
	require.NoError(t, err)
	require.NoError(t, proc.Kill())
	select {
	case <-result.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("late run did not exit after explicit termination")
	}
}
