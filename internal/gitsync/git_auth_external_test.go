// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/gitsync"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/require"
)

func TestSSHAuthLeavesHostVerificationToTransport(t *testing.T) {
	auth, err := gitsync.AuthForTest(&gitsync.Config{
		Auth: gitsync.AuthConfig{
			Type:       gitsync.AuthTypeSSH,
			SSHKeyPath: writePrivateKey(t),
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
			SSHKeyPath: writePrivateKey(t),
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

func writePrivateKey(t *testing.T) string {
	t.Helper()

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyPath, privateKeyPEM(t), 0o600))
	return keyPath
}

func privateKeyPEM(t *testing.T) []byte {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
