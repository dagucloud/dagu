// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import "os/exec"

// BuildShellCommandForTest exposes shell command construction to external tests.
func BuildShellCommandForTest(shell, cmdStr string) *exec.Cmd {
	return buildShellCommand(shell, cmdStr)
}
