// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCalculateBackoffInterval_NegativeAttemptCountClampsToZero(t *testing.T) {
	t.Parallel()

	interval := 5 * time.Second
	got := CalculateBackoffInterval(interval, 2.0, 0, -3)
	require.Equal(t, interval, got)
}

func TestCalculateBackoffIntervalOverflowRespectsMax(t *testing.T) {
	t.Parallel()

	got := CalculateBackoffInterval(time.Second, 2.0, 10*time.Second, 64)
	require.Equal(t, 10*time.Second, got)
}

func TestCalculateBackoffIntervalSaturatesAtInt64Boundary(t *testing.T) {
	t.Parallel()

	// float64(math.MaxInt64) rounds to 2^63, so >= must be used to saturate.
	got := CalculateBackoffInterval(time.Nanosecond, 2.0, 0, 63)
	require.Equal(t, time.Duration(math.MaxInt64), got)
}
