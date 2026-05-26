// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScaleTimeoutAppliesPlatformAndRaceMultiplier(t *testing.T) {
	base := 2 * time.Second

	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		require.Equal(t, 8*time.Second, scaleTimeout(base))
	case runtime.GOOS == "windows" || raceEnabled():
		require.Equal(t, 4*time.Second, scaleTimeout(base))
	default:
		require.Equal(t, base, scaleTimeout(base))
	}
}
