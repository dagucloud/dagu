// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"math"
	"time"
)

// CalculateBackoffInterval returns the delay for the given retry attempt.
// A non-positive backoff keeps the interval fixed.
func CalculateBackoffInterval(interval time.Duration, backoff float64, maxInterval time.Duration, attemptCount int) time.Duration {
	if attemptCount < 0 {
		attemptCount = 0
	}
	if backoff > 0 {
		sleepTime := float64(interval) * math.Pow(backoff, float64(attemptCount))
		// Compare before converting: a large exponent overflows int64
		// nanoseconds and would wrap negative, bypassing maxInterval.
		if maxInterval > 0 && sleepTime > float64(maxInterval) {
			return maxInterval
		}
		// float64(math.MaxInt64) rounds up to 2^63, so include equality.
		if sleepTime >= float64(math.MaxInt64) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(sleepTime)
	}
	return interval
}
