// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildShellCommandForPowerShellUsesStableInvocation(t *testing.T) {
	cmd := value.BuildShellCommandForTest("pwsh", "echo hello")

	assert.Equal(t, "pwsh", shellBaseNameForTest(cmd.Path))
	assert.Equal(t, []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"}, cmd.Args[1:])
}

func TestBuildShellCommandParsesConfiguredShellArgs(t *testing.T) {
	cmd := value.BuildShellCommandForTest("powershell -ExecutionPolicy Bypass", "echo hello")

	assert.Equal(t, "powershell", shellBaseNameForTest(cmd.Path))
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
	cmd := value.BuildShellCommandForTest("pwsh -NoProfile -Command", "echo hello")

	assert.Equal(t, []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"}, cmd.Args[1:])
}

func TestBuildShellCommandKeepsExistingShellPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shell dir")
	require.NoError(t, os.MkdirAll(dir, 0750))
	shellPath := filepath.Join(dir, "pwsh")
	require.NoError(t, os.WriteFile(shellPath, []byte{}, 0600))

	cmd := value.BuildShellCommandForTest(shellPath, "echo hello")

	assert.Equal(t, shellPath, cmd.Path)
	assert.Equal(t, []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"}, cmd.Args[1:])
}

func shellBaseNameForTest(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	return name
}
