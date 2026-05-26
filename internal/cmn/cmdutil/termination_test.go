// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmdutil

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTerminationIntentFromSignal(t *testing.T) {
	t.Run("Graceful", func(t *testing.T) {
		intent := TerminationFromSignal(syscall.SIGTERM)

		require.Equal(t, TerminationModeGraceful, intent.Mode)
		require.Equal(t, os.Signal(syscall.SIGTERM), intent.Signal)
		require.True(t, intent.IsTermination())
		require.False(t, intent.IsForce())
	})

	t.Run("Force", func(t *testing.T) {
		intent := TerminationFromSignal(os.Kill)

		require.Equal(t, TerminationModeForce, intent.Mode)
		require.Equal(t, os.Kill, intent.Signal)
		require.True(t, intent.IsTermination())
		require.True(t, intent.IsForce())
	})

	t.Run("NonTerminationSignal", func(t *testing.T) {
		intent := TerminationFromSignal(syscall.Signal(0))

		require.Equal(t, TerminationModeGraceful, intent.Mode)
		require.Equal(t, os.Signal(syscall.Signal(0)), intent.Signal)
		require.False(t, intent.IsTermination())
		require.False(t, intent.IsForce())
	})
}

func TestForceTerminationIgnoresSignalOverride(t *testing.T) {
	intent := ForceTermination().WithSignal(syscall.SIGTERM)

	require.Equal(t, TerminationModeForce, intent.Mode)
	require.Equal(t, os.Kill, intent.Signal)
	require.True(t, intent.IsForce())
}

func TestGracefulTerminationNormalizesForceSignal(t *testing.T) {
	intent := GracefulTermination(os.Kill)

	require.Equal(t, TerminationModeForce, intent.Mode)
	require.Equal(t, os.Kill, intent.Signal)
	require.True(t, intent.IsForce())
}
