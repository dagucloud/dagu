// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"regexp"
	"strings"
)

const (
	// MaxSafeNameLength is the maximum length of a safe filename
	MaxSafeNameLength = 100
)

var (
	// Only allow alphanumeric characters, underscores, and hyphens
	allowedCharRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

	// https://github.com/sindresorhus/filename-reserved-regex/blob/master/index.js
	filenameReservedRegex             = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
	filenameReservedWindowsNamesRegex = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)
)

// SafeName converts a string to a safe filename containing only alphanumeric characters,
// underscores, and hyphens
func SafeName(str string) string {
	// Replace any character not in [a-zA-Z0-9_\-] with underscore
	safe := allowedCharRegex.ReplaceAllString(str, "_")

	// Truncate to a safe length
	runes := []rune(safe)
	if len(runes) > MaxSafeNameLength {
		safe = string(runes[:MaxSafeNameLength])
	}

	return safe
}

// NormalizeFilename replaces OS-reserved characters and Windows reserved
// device names in a filename with the given replacement string.
func NormalizeFilename(name, replacement string) string {
	s := filenameReservedRegex.ReplaceAllString(name, replacement)
	s = strings.ReplaceAll(s, " ", replacement)

	// Windows reserves device names regardless of any extension, so the
	// segment before the first dot is what matters.
	device, rest, hasDot := strings.Cut(s, ".")
	if filenameReservedWindowsNamesRegex.MatchString(device) {
		device = replacement
	}
	if hasDot {
		return device + "." + rest
	}
	return device
}
