// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eval_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/stretchr/testify/assert"
)

func TestBuildShellCommandForPowerShellUsesStableInvocation(t *testing.T) {
	cmd := eval.BuildShellCommandForTest("pwsh", "echo hello")

	assert.Equal(t, "pwsh", cmd.Path)
	assert.Equal(t, []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"}, cmd.Args[1:])
}

func TestBuildShellCommandParsesConfiguredShellArgs(t *testing.T) {
	cmd := eval.BuildShellCommandForTest("powershell -ExecutionPolicy Bypass", "echo hello")

	assert.Equal(t, "powershell", cmd.Path)
	assert.Equal(t, []string{
		"-ExecutionPolicy",
		"Bypass",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		"echo hello",
	}, cmd.Args[1:])
}

func TestBuildShellCommandDoesNotDuplicateCommandFlag(t *testing.T) {
	cmd := eval.BuildShellCommandForTest("pwsh -NoProfile -Command", "echo hello")

	assert.Equal(t, []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"}, cmd.Args[1:])
}
