// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DefaultName is the name of the default project.
const DefaultName = "default"

// maxDisplayNameLength is the most characters a display name holds.
const maxDisplayNameLength = 200

var (
	// namePattern keeps a name usable as a directory name on every platform
	// and as a CLI argument and API path segment.
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	// deviceNamePattern matches the names Windows reserves for devices.
	deviceNamePattern = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])$`)
)

// ValidateName returns an error wrapping ErrInvalidName when name may not
// name a new project.
func ValidateName(name string) error {
	switch {
	case !namePattern.MatchString(name):
		return fmt.Errorf("%w: %q must be 1 to 64 letters, digits, '-', and '_', starting with a letter or digit",
			ErrInvalidName, name)
	case deviceNamePattern.MatchString(name):
		return fmt.Errorf("%w: %q is a Windows device name", ErrInvalidName, name)
	case strings.EqualFold(name, DefaultName):
		return fmt.Errorf("%w: %q is reserved", ErrInvalidName, name)
	}
	return nil
}

// ValidateDisplayName returns an error wrapping ErrInvalidDisplayName when
// displayName may not be a project's display name.
func ValidateDisplayName(displayName string) error {
	if !utf8.ValidString(displayName) {
		return fmt.Errorf("%w: not valid UTF-8", ErrInvalidDisplayName)
	}
	if n := utf8.RuneCountInString(displayName); n > maxDisplayNameLength {
		return fmt.Errorf("%w: %d characters, more than %d", ErrInvalidDisplayName, n, maxDisplayNameLength)
	}
	return nil
}

// CompareNames orders the default project first, then the others by name.
func CompareNames(a, b string) int {
	aDefault, bDefault := a == DefaultName, b == DefaultName
	switch {
	case aDefault == bDefault:
		return strings.Compare(a, b)
	case aDefault:
		return -1
	default:
		return 1
	}
}
