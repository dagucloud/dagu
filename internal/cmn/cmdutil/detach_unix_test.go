// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmdutil

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDetachAttrStartsOwnSession(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "5")
	cmd.SysProcAttr = DetachAttr(false)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	sid, err := unix.Getsid(cmd.Process.Pid)
	require.NoError(t, err)
	assert.Equal(t, cmd.Process.Pid, sid, "the command leads a session of its own")
}
