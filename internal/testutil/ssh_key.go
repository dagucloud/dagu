// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// SSHPrivateKeyPEM returns a PEM-encoded private key for SSH auth tests.
func SSHPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// WriteSSHPrivateKey writes a private key under dir and returns its path.
func WriteSSHPrivateKey(t *testing.T, dir, name string) string {
	t.Helper()

	keyPath := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(keyPath, SSHPrivateKeyPEM(t), 0o600))
	return keyPath
}
