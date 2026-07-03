// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gogitssh_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/gogitssh"
	"github.com/dagucloud/dagu/internal/testutil"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/require"
	cryptossh "golang.org/x/crypto/ssh"
)

func TestNewPublicKeysFromFileLeavesHostVerificationToTransport(t *testing.T) {
	keyPath := testutil.WriteSSHPrivateKey(t, t.TempDir(), "id_ed25519")

	auth, err := gogitssh.NewPublicKeysFromFile("git", keyPath, "")
	require.NoError(t, err)
	require.Equal(t, gitssh.PublicKeysName, auth.Name())

	cfg, err := auth.ClientConfig()
	require.NoError(t, err)
	require.Equal(t, "git", cfg.User)
	require.Len(t, cfg.Auth, 1)
	require.Nil(t, cfg.HostKeyCallback)
	require.Empty(t, cfg.HostKeyAlgorithms)
}

func TestNewPublicKeysLeavesHostVerificationToTransport(t *testing.T) {
	keyBytes := testutil.SSHPrivateKeyPEM(t)

	auth, err := gogitssh.NewPublicKeys("git", keyBytes, "")
	require.NoError(t, err)

	cfg, err := auth.ClientConfig()
	require.NoError(t, err)
	require.Nil(t, cfg.HostKeyCallback)
	require.Empty(t, cfg.HostKeyAlgorithms)
}

func TestNewPublicKeysAcceptsPassphrase(t *testing.T) {
	keyBytes := encryptedPrivateKeyPEM(t, "secret")

	auth, err := gogitssh.NewPublicKeys("git", keyBytes, "secret")
	require.NoError(t, err)

	cfg, err := auth.ClientConfig()
	require.NoError(t, err)
	require.Len(t, cfg.Auth, 1)
	require.Nil(t, cfg.HostKeyCallback)
	require.Empty(t, cfg.HostKeyAlgorithms)
}

func TestUserFromURL(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		fallback string
		want     string
	}{
		{
			name:     "scp style url",
			repoURL:  "deploy@example.com:org/repo.git",
			fallback: "git",
			want:     "deploy",
		},
		{
			name:     "ssh url",
			repoURL:  "ssh://builder@example.com:2222/org/repo.git",
			fallback: "git",
			want:     "builder",
		},
		{
			name:     "no user",
			repoURL:  "ssh://example.com/org/repo.git",
			fallback: "git",
			want:     "git",
		},
		{
			name:     "invalid url",
			repoURL:  "%",
			fallback: "git",
			want:     "git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, gogitssh.UserFromURL(tt.repoURL, tt.fallback))
		})
	}
}

func encryptedPrivateKeyPEM(t *testing.T, passphrase string) []byte {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	block, err := cryptossh.MarshalPrivateKeyWithPassphrase(privateKey, "", []byte(passphrase))
	require.NoError(t, err)

	return pem.EncodeToMemory(block)
}
