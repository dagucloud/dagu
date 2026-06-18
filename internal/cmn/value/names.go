// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import "regexp"

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName reports whether name is a valid environment variable name.
func ValidEnvName(name string) bool {
	return envNamePattern.MatchString(name)
}
