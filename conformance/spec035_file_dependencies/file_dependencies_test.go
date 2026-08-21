// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec035_file_dependencies_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestLocalFileDependencies(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell command")
	}

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "local.yaml")
	result.ExpectExitCode(0)
}

func TestFileDependenciesRejectValueReferences(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "value-reference.yaml")
	result.ExpectExitCode(1)
	result.ExpectStderrContains("dependencies", "literal")
}
