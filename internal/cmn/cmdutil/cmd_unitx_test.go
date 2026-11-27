// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmdutil

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestKillProcessGroup(t *testing.T) {
	tests := []struct {
		name        string
		setupCmd    func() *exec.Cmd
		signal      os.Signal
		shouldError bool
	}{
		{
			name: "KillRunningProcess",
			setupCmd: func() *exec.Cmd {
				cmd := exec.Command("sleep", "10")
				cmd.SysProcAttr = &syscall.SysProcAttr{
					Setpgid: true,
					Pgid:    0,
				}
				err := cmd.Start()
				require.NoError(t, err)
				return cmd
			},
			signal:      syscall.SIGTERM,
			shouldError: false,
		},
		{
			name: "KillNilProcess",
			setupCmd: func() *exec.Cmd {
				return nil
			},
			signal:      syscall.SIGTERM,
			shouldError: false, // Should handle nil gracefully
		},
		{
			name: "KillProcessWithoutProcessField",
			setupCmd: func() *exec.Cmd {
				return &exec.Cmd{}
			},
			signal:      syscall.SIGTERM,
			shouldError: false, // Should handle nil Process gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupCmd()

			err := KillProcessGroup(cmd, tt.signal)

			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Clean up if process was started
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Wait()
			}
		})
	}
}

func TestKillMultipleProcessGroups(t *testing.T) {
	// Start multiple processes
	cmds := make(map[string]*exec.Cmd)

	// Start a few sleep processes
	for i := range 3 {
		cmd := exec.Command("sleep", "10")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
			Pgid:    0,
		}
		err := cmd.Start()
		require.NoError(t, err)
		cmds[string(rune('a'+i))] = cmd
	}

	// Add a nil command
	cmds["nil"] = nil

	// Kill all processes
	err := KillMultipleProcessGroups(cmds, syscall.SIGTERM)
	// Error might occur if process already exited, which is OK
	_ = err

	// Verify processes are terminated
	for name, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			// Give process time to die
			done := make(chan error, 1)
			go func(c *exec.Cmd) {
				done <- c.Wait()
			}(cmd)

			select {
			case <-done:
				// Process terminated
			case <-time.After(1 * time.Second):
				t.Errorf("Process %s did not terminate", name)
			}
		}
	}
}

func TestSetupCommand_Unix(t *testing.T) {
	cmd := exec.Command("echo", "test")

	// Verify SysProcAttr is nil before setup
	assert.Nil(t, cmd.SysProcAttr)

	// Setup command
	setupCommand(cmd)

	// Verify SysProcAttr is set correctly
	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setpgid)
	assert.Equal(t, 0, cmd.SysProcAttr.Pgid)
}

// A command without Setsid starts in a process group of its own, in the
// session of the caller. A command with Setsid starts as the leader of a new
// session, which is also its process group. A stop of the group ends either.
func TestSetupCommandSession(t *testing.T) {
	callerSession, err := unix.Getsid(0)
	require.NoError(t, err)
	for _, setsid := range []bool{false, true} {
		cmd := exec.Command("sleep", "10")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: setsid}
		process, err := StartManagedProcess(cmd)
		require.NoError(t, err, "setsid=%v", setsid)
		pid := process.PID()
		pgid, err := unix.Getpgid(pid)
		require.NoError(t, err)
		assert.Equal(t, pid, pgid, "setsid=%v", setsid)
		session, err := unix.Getsid(pid)
		require.NoError(t, err)
		if setsid {
			assert.Equal(t, pid, session)
		} else {
			assert.Equal(t, callerSession, session)
		}

		_, err = process.Stop(StopRequest{Intent: ForceTermination()})
		require.NoError(t, err)
		assert.ErrorContains(t, process.Wait(), "killed", "setsid=%v", setsid)
		require.NoError(t, process.Release())
	}
}
