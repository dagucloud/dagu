// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"path/filepath"
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

// TestNamedDAGHint explains that the test file at path, which holds
// top-level steps and no cases, does not run as a DAG, and suggests a name
// under which it does. The text continues a sentence that starts with the
// file's name.
func TestNamedDAGHint(path string) string {
	name := filepath.Base(path)
	stem, _ := CutTestFileSuffix(name)
	return fmt.Sprintf("reads as a DAG, with steps and no cases, but Dagu treats it as a test file and does not run it as a DAG; rename it, for example to %s-test%s", stem, filepath.Ext(name))
}
