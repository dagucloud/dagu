// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtimeprofile

import "regexp"

const namePattern = `^[a-z0-9][a-z0-9._-]*$`

var namePatternRe = regexp.MustCompile(namePattern)

// IsValidName reports whether name follows the runtime profile naming rule.
func IsValidName(name string) bool {
	return namePatternRe.MatchString(name)
}
