// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"strings"
)

// ErrTestFile reports that a DAG name or path resolves to a test file.
var ErrTestFile = errors.New("a test file is not a DAG; run it with dagu test")

// testFileSuffixes end the name of a test file. DAG names may contain dots,
// so only a full suffix marks a test file.
var testFileSuffixes = []string{".test.yaml", ".test.yml"}

// IsTestFile reports whether the file at path is a workflow test file, one
// whose name ends in .test.yaml or .test.yml. A test file is never a DAG.
func IsTestFile(path string) bool {
	_, ok := CutTestFileSuffix(path)
	return ok
}

// CutTestFileSuffix returns path without its .test.yaml or .test.yml suffix
// and reports whether path is a test file. It returns path unchanged and
// false otherwise.
func CutTestFileSuffix(path string) (string, bool) {
	for _, suffix := range testFileSuffixes {
		if before, ok := strings.CutSuffix(path, suffix); ok {
			return before, true
		}
	}
	return path, false
}
