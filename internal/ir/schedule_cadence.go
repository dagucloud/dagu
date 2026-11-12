// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"strconv"
	"strings"
)

// SameCadence reports whether two cron expressions fire on the same days,
// once per day or once per hour, and differ only in the time they fire at.
// A run fired by one then stands for the same period as a run fired by the
// other.
func SameCadence(a, b string) bool {
	fa, okA := cadenceFields(a)
	fb, okB := cadenceFields(b)
	if !okA || !okB {
		return false
	}
	// Day of month, month, and day of week must match exactly.
	if fa[2] != fb[2] || fa[3] != fb[3] || fa[4] != fb[4] {
		return false
	}
	if !isCronNumber(fa[0]) || !isCronNumber(fb[0]) {
		return false
	}
	daily := isCronNumber(fa[1]) && isCronNumber(fb[1])
	hourly := fa[1] == "*" && fb[1] == "*"
	return daily || hourly
}

// cadenceFields returns the five fields of a cron expression without its
// time zone prefix.
func cadenceFields(expr string) ([]string, bool) {
	_, body := splitTimezonePrefix(strings.Join(strings.Fields(expr), " "))
	fields := strings.Fields(body)
	return fields, len(fields) == 5
}

func isCronNumber(field string) bool {
	_, err := strconv.Atoi(field)
	return err == nil
}
