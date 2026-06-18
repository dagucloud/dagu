// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/runtime/builtin/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecCommandForTest_DirectBypassesShell(t *testing.T) {
	cmd := []string{"agent", "--prompt", "a && b"}

	got := docker.ExecCommandForTest(
		[]string{"/bin/sh", "-c"},
		cmd,
		docker.ExecOptions{Direct: true},
	)

	assert.Equal(t, cmd, got)
}

func TestExecCommandForTest_DefaultHonorsShell(t *testing.T) {
	got := docker.ExecCommandForTest(
		[]string{"/bin/sh"},
		[]string{"echo", "hello"},
		docker.ExecOptions{},
	)

	assert.Equal(t, []string{"/bin/sh", "-c", "echo hello"}, got)
}

func TestExecCommandForTest_PIDFileWrapsDirectCommand(t *testing.T) {
	cmd := []string{"agent", "--prompt", "a && b"}

	got := docker.ExecCommandForTest(
		[]string{"/bin/sh", "-c"},
		cmd,
		docker.ExecOptions{Direct: true, PIDFile: "/tmp/dagu/pid"},
	)

	require.GreaterOrEqual(t, len(got), 5+len(cmd))
	assert.Equal(t, []string{"sh", "-c"}, got[:2])
	assert.Contains(t, got[2], `printf '%s\n' "$child" > "$pidfile"`)
	assert.Equal(t, "dagu-exec-wrapper", got[3])
	assert.Equal(t, "/tmp/dagu/pid", got[4])
	assert.Equal(t, cmd, got[5:])
}

func TestExecCommandForTest_PIDFileWrapsShellCommand(t *testing.T) {
	got := docker.ExecCommandForTest(
		[]string{"/bin/sh"},
		[]string{"echo", "hello"},
		docker.ExecOptions{PIDFile: "/tmp/dagu/pid"},
	)

	require.GreaterOrEqual(t, len(got), 8)
	assert.Equal(t, []string{"sh", "-c"}, got[:2])
	assert.Equal(t, "dagu-exec-wrapper", got[3])
	assert.Equal(t, "/tmp/dagu/pid", got[4])
	assert.Equal(t, []string{"/bin/sh", "-c", "echo hello"}, got[5:])
}

func TestMergeEnvByKeyForTest_DeterministicOverride(t *testing.T) {
	got := docker.MergeEnvByKeyForTest(
		[]string{"PATH=/bin", "TOKEN=old", "KEEP=1"},
		[]string{"TOKEN=new", "BAD", "=ignored"},
		[]string{"PATH=/usr/bin"},
	)

	assert.Equal(t, []string{"PATH=/usr/bin", "TOKEN=new", "KEEP=1"}, got)
}
