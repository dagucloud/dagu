// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/gitsync"
	"github.com/dagucloud/dagu/internal/testutil"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/require"
)

func TestSSHAuthLeavesHostVerificationToTransport(t *testing.T) {
	auth, err := gitsync.AuthForTest(&gitsync.Config{
		Auth: gitsync.AuthConfig{
			Type:       gitsync.AuthTypeSSH,
			SSHKeyPath: testutil.WriteSSHPrivateKey(t, t.TempDir(), "id_ed25519"),
		},
	})
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

func TestSSHAuthUsesRepositoryURLUser(t *testing.T) {
	auth, err := gitsync.AuthForTest(&gitsync.Config{
		Repository: "deploy@example.com:org/repo.git",
		Auth: gitsync.AuthConfig{
			Type:       gitsync.AuthTypeSSH,
			SSHKeyPath: testutil.WriteSSHPrivateKey(t, t.TempDir(), "id_ed25519"),
		},
	})
	require.NoError(t, err)

	sshAuth, ok := auth.(gitssh.AuthMethod)
	require.True(t, ok)
	cfg, err := sshAuth.ClientConfig()
	require.NoError(t, err)
	require.Equal(t, "deploy", cfg.User)
	require.Nil(t, cfg.HostKeyCallback)
	require.Empty(t, cfg.HostKeyAlgorithms)
}
