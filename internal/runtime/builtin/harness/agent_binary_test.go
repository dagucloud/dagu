// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An agent worker runs the binary of its option --agent for its provider, not a
// binary that PATH finds (Spec 092, "Start").
func TestInvocationCommandUsesTheAgentBinary(t *testing.T) {
	t.Parallel()
	if goruntime.GOOS == "windows" {
		t.Skip("the test binary is a shell script")
	}

	binary := filepath.Join(t.TempDir(), "claude-cli")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o600))
	require.NoError(t, os.Chmod(binary, 0o755))
	claude, err := getProvider("claude")
	require.NoError(t, err)
	codex, err := getProvider("codex")
	require.NoError(t, err)

	exec := &harnessExecutor{workDir: t.TempDir()}
	ctx := runtime.WithAgentProviderBinary(runtime.WithEnv(t.Context(), runtime.Env{}), "claude", binary)

	cmd, err := exec.invocationCommand(ctx, providerConfig{name: "claude", provider: claude}, []string{"-p", "hi"}, nil, io.Discard, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, binary, cmd.Path)
	assert.Equal(t, "claude", cmd.Args[0])

	// Another provider keeps the lookup in PATH: it never gets the binary of claude.
	other, err := exec.invocationCommand(ctx, providerConfig{name: "codex", provider: codex}, nil, nil, io.Discard, io.Discard)
	if err == nil {
		assert.NotEqual(t, binary, other.Path)
	} else {
		assert.Contains(t, err.Error(), "codex")
	}

	_, ok := runtime.AgentProviderBinary(ctx, "codex")
	assert.False(t, ok)
	_, ok = runtime.AgentProviderBinary(t.Context(), "claude")
	assert.False(t, ok)
}
