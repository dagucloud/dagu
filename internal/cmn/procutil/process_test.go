// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package procutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAlive(t *testing.T) {
	t.Parallel()

	require.False(t, IsAlive(0))
	require.False(t, IsAlive(-1))
	require.True(t, IsAlive(os.Getpid()))
}

func TestStartTime(t *testing.T) {
	t.Parallel()

	startedAt, ok := StartTime(os.Getpid())
	require.True(t, ok)
	require.Positive(t, startedAt)

	matched, actualStartedAt, ok := MatchesStartTime(os.Getpid(), startedAt)
	require.True(t, ok)
	require.True(t, matched)
	require.Equal(t, startedAt, actualStartedAt)

	matched, _, ok = MatchesStartTime(os.Getpid(), startedAt+1)
	require.True(t, ok)
	require.False(t, matched)
}
