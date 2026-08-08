// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
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
