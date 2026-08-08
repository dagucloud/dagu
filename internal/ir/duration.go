// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var reDays = regexp.MustCompile(`(\d+)d`)

// ParseDuration extends time.ParseDuration with support for "d" (days = 24h).
// Rejects empty strings and zero/negative durations.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	var convErr error
	s = reDays.ReplaceAllStringFunc(s, func(v string) string {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil {
			convErr = fmt.Errorf("invalid day value in %q: %w", v, err)
			return v
		}
		return strconv.Itoa(n*24) + "h"
	})
	if convErr != nil {
		return 0, convErr
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return d, nil
}
