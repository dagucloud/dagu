// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stringutil_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/stretchr/testify/assert"
)

func TestFormatDuration(t *testing.T) {
	t.Run("ZeroDuration", func(t *testing.T) {
		result := stringutil.FormatDuration(0)
		assert.Equal(t, "0s", result)
	})

	t.Run("PositiveDurations", func(t *testing.T) {
		tests := []struct {
			name     string
			duration time.Duration
			expected string
		}{
			// Milliseconds
			{"1ms", 1 * time.Millisecond, "1ms"},
			{"100ms", 100 * time.Millisecond, "100ms"},
			{"999ms", 999 * time.Millisecond, "999ms"},

			// Seconds
			{"1s", 1 * time.Second, "1.0s"},
			{"1.5s", 1500 * time.Millisecond, "1.5s"},
			{"59.9s", 59900 * time.Millisecond, "59.9s"},

			// Minutes
			{"1m", 1 * time.Minute, "1m 0s"},
			{"1m30s", 90 * time.Second, "1m 30s"},
			{"59m59s", 59*time.Minute + 59*time.Second, "59m 59s"},

			// Hours
			{"1h", 1 * time.Hour, "1h 0m"},
			{"1h30m", 90 * time.Minute, "1h 30m"},
			{"2h15m", 2*time.Hour + 15*time.Minute, "2h 15m"},
			{"24h", 24 * time.Hour, "24h 0m"},
			{"100h", 100 * time.Hour, "100h 0m"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := stringutil.FormatDuration(tt.duration)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("NegativeDurations", func(t *testing.T) {
		tests := []struct {
			name     string
			duration time.Duration
			expected string
		}{
			// Negative milliseconds
			{"-1ms", -1 * time.Millisecond, "-1ms"},
			{"-100ms", -100 * time.Millisecond, "-100ms"},
			{"-999ms", -999 * time.Millisecond, "-999ms"},

			// Negative seconds
			{"-1s", -1 * time.Second, "-1.0s"},
			{"-1.5s", -1500 * time.Millisecond, "-1.5s"},
			{"-59.9s", -59900 * time.Millisecond, "-59.9s"},

			// Negative minutes
			{"-1m", -1 * time.Minute, "-1m 0s"},
			{"-1m30s", -90 * time.Second, "-1m 30s"},
			{"-59m59s", -(59*time.Minute + 59*time.Second), "-59m 59s"},

			// Negative hours
			{"-1h", -1 * time.Hour, "-1h 0m"},
			{"-1h30m", -90 * time.Minute, "-1h 30m"},
			{"-2h15m", -(2*time.Hour + 15*time.Minute), "-2h 15m"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := stringutil.FormatDuration(tt.duration)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("EdgeCases", func(t *testing.T) {
		tests := []struct {
			name     string
			duration time.Duration
			expected string
		}{
			// Boundary values
			{"999us", 999 * time.Microsecond, "0ms"},
			{"1000us", 1000 * time.Microsecond, "1ms"},
			{"59.999s", 59999 * time.Millisecond, "60.0s"}, // Rounds to 60.0s
			{"60s", 60 * time.Second, "1m 0s"},
			{"3599s", 3599 * time.Second, "59m 59s"},
			{"3600s", 3600 * time.Second, "1h 0m"},

			// Very small durations
			{"1ns", 1 * time.Nanosecond, "0ms"},
			{"1us", 1 * time.Microsecond, "0ms"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := stringutil.FormatDuration(tt.duration)
				assert.Equal(t, tt.expected, result)
			})
		}
	})
}
