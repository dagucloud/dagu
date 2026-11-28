// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package procutil

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// An exited child that is not reaped yet is a zombie, which still answers
// signal 0; IsAlive reports it as exited.
func TestIsAliveZombie(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("true")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Wait() })

	// WNOWAIT returns once the child has exited and leaves it unreaped.
	var info unix.Siginfo
	require.NoError(t, unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil))

	require.True(t, isZombie(pid))
	require.False(t, IsAlive(pid))
}
