// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
// underscores, and hyphens. On Windows, a reserved device name such as CON or
// LPT1 gets a trailing underscore.
func SafeName(str string) string {
	// Replace any character not in [a-zA-Z0-9_\-] with underscore
	safe := allowedCharRegex.ReplaceAllString(str, "_")

	// Truncate to a safe length
	runes := []rune(safe)
	if len(runes) > MaxSafeNameLength {
		safe = string(runes[:MaxSafeNameLength])
	}

	if reservesDeviceName(safe) {
		safe += "_"
	}

	return safe
}

// EscapeName returns the name under which this host stores a file or
// directory called name. Each "%", each byte the host cannot store in a
// name, including a path separator, and on Windows the first byte of a
// reserved device name are written as %XX. Any other byte is kept, and "."
// and ".." are returned unchanged. Two different names never share an entry,
// even on a file system that ignores case.
func EscapeName(name string) string {
	if name == "." || name == ".." {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if c := name[i]; c == '%' || unstorableByte(c, i == len(name)-1) {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	escaped := b.String()
	if reservesDeviceName(escaped) {
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

// ExistingName returns the name of the entry in dir that holds name. It is
// name, unless dir holds no entry called name but holds the entry that
// LegacyEntry finds for legacy, the name that earlier versions used.
func ExistingName(dir, name, legacy string) string {
	if name == legacy {
		return name
	}
	if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
		return name
	}
	if rel, ok := LegacyEntry(dir, legacy); ok {
		return rel
	}
	return name
}

// LegacyEntry reports whether dir holds a regular file or directory called
// legacy, a name stored as written before names were escaped, and returns
// its path relative to dir. It never looks outside dir or opens a device, and
// it skips a name that, ignoring case, is the escaped form of another name,
// since that entry may hold the other name's data.
func LegacyEntry(dir, legacy string) (string, bool) {
	path := filepath.Join(dir, legacy)
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if opensDevice(filepath.Base(path)) {
		return "", false
	}
	if other, err := url.PathUnescape(legacy); err == nil && other != legacy && strings.EqualFold(EscapeName(other), legacy) {
		return "", false
	}
	info, err := os.Lstat(path)
	if err != nil || !(info.IsDir() || info.Mode().IsRegular()) {
		return "", false
	}
	return rel, true
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
