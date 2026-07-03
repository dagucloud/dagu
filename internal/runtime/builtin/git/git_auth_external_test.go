// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git_test

import (
	"testing"

	gitexecutor "github.com/dagucloud/dagu/internal/runtime/builtin/git"
	"github.com/dagucloud/dagu/internal/testutil"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/require"
)

func TestSSHAuthLeavesHostVerificationToTransport(t *testing.T) {
	workDir := t.TempDir()
	testutil.WriteSSHPrivateKey(t, workDir, "id_ed25519")

	auth, err := gitexecutor.AuthForTest(map[string]any{
		"ssh_key_path": "id_ed25519",
	}, workDir)
	require.NoError(t, err)

	sshAuth, ok := auth.(gitssh.AuthMethod)
	require.True(t, ok)
	cfg, err := sshAuth.ClientConfig()
	require.NoError(t, err)
	require.Equal(t, "git", cfg.User)
	require.Len(t, cfg.Auth, 1)
	require.Nil(t, cfg.HostKeyCallback)
	require.Empty(t, cfg.HostKeyAlgorithms)
}

func TestSSHAuthUsesConfiguredUsername(t *testing.T) {
	workDir := t.TempDir()
	testutil.WriteSSHPrivateKey(t, workDir, "id_ed25519")

	auth, err := gitexecutor.AuthForTest(map[string]any{
		"ssh_key_path": "id_ed25519",
		"username":     "deploy",
	}, workDir)
	require.NoError(t, err)

	sshAuth, ok := auth.(gitssh.AuthMethod)
	require.True(t, ok)
	cfg, err := sshAuth.ClientConfig()
	require.NoError(t, err)
	require.Equal(t, "deploy", cfg.User)
	require.Nil(t, cfg.HostKeyCallback)
	require.Empty(t, cfg.HostKeyAlgorithms)
}

func TestSSHAuthUsesRepositoryURLUser(t *testing.T) {
	workDir := t.TempDir()
	testutil.WriteSSHPrivateKey(t, workDir, "id_ed25519")

	auth, err := gitexecutor.AuthForTest(map[string]any{
		"repository":   "builder@example.com:org/repo.git",
		"ssh_key_path": "id_ed25519",
	}, workDir)
	require.NoError(t, err)

	sshAuth, ok := auth.(gitssh.AuthMethod)
	require.True(t, ok)
	cfg, err := sshAuth.ClientConfig()
	require.NoError(t, err)
	require.Equal(t, "builder", cfg.User)
	require.Nil(t, cfg.HostKeyCallback)
	require.Empty(t, cfg.HostKeyAlgorithms)
}
