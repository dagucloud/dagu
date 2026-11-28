// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package spec108_peer_transport_test

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// pauseProcess stops the process pid, as a suspended laptop or a stopped
// container does, until the test ends.
func pauseProcess(t *testing.T, pid int) {
	t.Helper()
	require.NoError(t, syscall.Kill(pid, syscall.SIGSTOP))
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
}
