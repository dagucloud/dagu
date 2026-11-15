// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"fmt"
	"net/url"
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
// underscores, and hyphens. A name that the host maps to a device, such as
// NUL on Windows, gets a trailing underscore.
func SafeName(str string) string {
	// Replace any character not in [a-zA-Z0-9_\-] with underscore
	safe := allowedCharRegex.ReplaceAllString(str, "_")

	// Truncate to a safe length
	runes := []rune(safe)
	if len(runes) > MaxSafeNameLength {
		safe = string(runes[:MaxSafeNameLength])
	}

	// A device name the host stores as a file keeps its name, so the paths
	// stored under it before still resolve.
	if filenameReservedWindowsNamesRegex.MatchString(safe) && opensDevice(safe) {
		safe += "_"
	}

	return safe
}

// EscapeName returns the name under which this host stores a file or
// directory called name. A name the host stores as written is returned
// unchanged, as are "." and "..". Otherwise each byte the host cannot store
// in a name, including a path separator, and the first byte of a name the
// host maps to a device are written as %XX.
func EscapeName(name string) string {
	if name == "." || name == ".." {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if c := name[i]; unstorableByte(c, i == len(name)-1) {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	escaped := b.String()
	if escaped != "" && opensDevice(escaped) {
		escaped = fmt.Sprintf("%%%02X", escaped[0]) + escaped[1:]
	}
	return escaped
}

// UnescapeName returns the name that EscapeName stores as escaped. A name
// that EscapeName does not produce is returned as is.
func UnescapeName(escaped string) string {
	name, err := url.PathUnescape(escaped)
	if err != nil || EscapeName(name) != escaped {
		return escaped
	}
	return name
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
