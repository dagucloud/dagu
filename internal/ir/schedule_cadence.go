// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"math/bits"
	"strings"

	"github.com/robfig/cron/v3"
)

// cronStarBit marks a cron field written as "*". A star in the day of the
// month or the day of the week changes how the two combine, so it is part
// of when a schedule fires.
const cronStarBit = 1 << 63

// allHours has a bit for each hour of the day.
const allHours = 1<<24 - 1

// SameCadence reports whether two cron expressions fire on the same days,
// once per day or once per hour, and differ only in the time they fire at.
// A run fired by one then stands for the same period as a run fired by the
// other.
func SameCadence(a, b string) bool {
	sa, okA := cadenceSpec(a)
	sb, okB := cadenceSpec(b)
	if !okA || !okB {
		return false
	}
	if sa.Dom != sb.Dom || sa.Month != sb.Month || sa.Dow != sb.Dow {
		return false
	}
	if !singleValue(sa.Minute) || !singleValue(sb.Minute) {
		return false
	}
	daily := singleValue(sa.Hour) && singleValue(sb.Hour)
	hourly := sa.Hour&allHours == allHours && sb.Hour&allHours == allHours
	return daily || hourly
}

// cadenceSpec parses a cron expression without its time zone prefix.
func cadenceSpec(expr string) (*cron.SpecSchedule, bool) {
	_, body := splitTimezonePrefix(strings.Join(strings.Fields(expr), " "))
	parsed, err := standardCronParser.Parse(body)
	if err != nil {
		return nil, false
	}
	spec, ok := parsed.(*cron.SpecSchedule)
	return spec, ok
}

// singleValue reports whether a cron field names exactly one value.
func singleValue(field uint64) bool {
	return field&cronStarBit == 0 && bits.OnesCount64(field) == 1
}
