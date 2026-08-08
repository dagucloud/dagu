// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "regexp"

// DAGNameMaxLen is the maximum supported DAG name length.
const DAGNameMaxLen = 40

var dagNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// ValidateDAGName validates a DAG name according to shared rules.
// Empty name is allowed because callers may provide one from context.
func ValidateDAGName(name string) error {
	if name == "" {
		return nil
	}
	if name == "." || name == ".." {
		return ErrNameInvalidChars
	}
	if len(name) > DAGNameMaxLen {
		return ErrNameTooLong
	}
	if !dagNameRegex.MatchString(name) {
		return ErrNameInvalidChars
	}
	return nil
}
