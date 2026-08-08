// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import "time"

// TimeInUTC is a wrapper for time.Time that ensures the time is in UTC.
type TimeInUTC struct{ time.Time }

// NewUTC creates a new timeInUTC from a time.Time.
func NewUTC(t time.Time) TimeInUTC {
	return TimeInUTC{t.UTC()}
}
