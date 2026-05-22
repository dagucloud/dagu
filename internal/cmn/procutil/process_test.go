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
