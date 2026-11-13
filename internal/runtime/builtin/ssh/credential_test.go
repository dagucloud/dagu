// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// testKey is a generated ed25519 key in plain and passphrase-protected
// OpenSSH PEM form.
type testKey struct {
	public    gossh.PublicKey
	plain     string
	encrypted string
}

const testPassphrase = "correct horse"

func newTestKey(t *testing.T) testKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	public, err := gossh.NewPublicKey(pub)
	require.NoError(t, err)
	plain, err := gossh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	encrypted, err := gossh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(testPassphrase))
	require.NoError(t, err)
	return testKey{
		public:    public,
		plain:     string(pem.EncodeToMemory(plain)),
		encrypted: string(pem.EncodeToMemory(encrypted)),
	}
}

func TestKeySigner(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)
	encryptedPath := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(encryptedPath, []byte(key.encrypted), 0600))

	ctx := WithSecretValues(context.Background(), map[string]string{
		"ssh/key":            key.plain,
		"ssh/encrypted-key":  key.encrypted,
		"ssh/passphrase":     testPassphrase,
		"ssh/bad-passphrase": "wrong",
		"ssh/not-a-key":      "-----BEGIN OPENSSH PRIVATE KEY----- flattened onto one line",
	})

	tests := []struct {
		name    string
		auth    ir.SSHAuth
		wantErr string
	}{
		{name: "SecretKey", auth: ir.SSHAuth{KeySecret: "ssh/key"}},
		{
			name: "SecretKeyIgnoresPassphraseWhenUnencrypted",
			auth: ir.SSHAuth{KeySecret: "ssh/key", Passphrase: "unused"},
		},
		{
			name: "EncryptedSecretKeyWithSecretPassphrase",
			auth: ir.SSHAuth{KeySecret: "ssh/encrypted-key", PassphraseSecret: "ssh/passphrase"},
		},
		{
			name: "EncryptedPathKeyWithLiteralPassphrase",
			auth: ir.SSHAuth{Key: encryptedPath, Passphrase: testPassphrase},
		},
		{
			name:    "EncryptedWithoutPassphrase",
			auth:    ir.SSHAuth{KeySecret: "ssh/encrypted-key"},
			wantErr: `key: secret "ssh/encrypted-key": the key is encrypted and no passphrase is set`,
		},
		{
			name:    "WrongPassphrase",
			auth:    ir.SSHAuth{KeySecret: "ssh/encrypted-key", PassphraseSecret: "ssh/bad-passphrase"},
			wantErr: `key: secret "ssh/encrypted-key": the passphrase does not unlock the key`,
		},
		{
			name:    "NotAKey",
			auth:    ir.SSHAuth{KeySecret: "ssh/not-a-key"},
			wantErr: `key: secret "ssh/not-a-key": the value is not a private key; check that its line breaks were kept`,
		},
		{
			name:    "UnresolvedRef",
			auth:    ir.SSHAuth{KeySecret: "ssh/missing"},
			wantErr: `key: secret "ssh/missing" was not resolved at run start`,
		},
		{
			name:    "UnresolvedPassphraseRef",
			auth:    ir.SSHAuth{KeySecret: "ssh/key", PassphraseSecret: "ssh/missing"},
			wantErr: `passphrase: secret "ssh/missing" was not resolved at run start`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			signer, err := keySigner(ctx, tt.auth, "")
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, key.public.Marshal(), signer.PublicKey().Marshal())
		})
	}
}

func TestFromMapConfigReadsSecretCredentials(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)
	ctx := WithSecretValues(context.Background(), map[string]string{
		"ssh/key":     key.encrypted,
		"ssh/phrase":  testPassphrase,
		"ssh/jump-pw": "jump-password",
	})

	client, err := FromMapConfig(ctx, map[string]any{
		"host":            "app.example.com",
		"user":            "deploy",
		"strict_host_key": false,
		"key":             map[string]any{"secret": "ssh/key"},
		"passphrase":      map[string]any{"secret": "ssh/phrase"},
		"bastion": map[string]any{
			"host":     "jump.example.com",
			"user":     "ops",
			"password": map[string]any{"secret": "ssh/jump-pw"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, client.bastionCfg)

	_, err = FromMapConfig(context.Background(), map[string]any{
		"host":            "app.example.com",
		"strict_host_key": false,
		"password":        map[string]any{"secret": "ssh/password"},
	})
	require.EqualError(t, err, `password: secret "ssh/password" was not resolved at run start`)

	_, err = FromMapConfig(ctx, map[string]any{
		"host":            "app.example.com",
		"strict_host_key": false,
		"bastion":         map[string]any{"host": "jump", "key": map[string]any{"secret": "Bad"}},
	})
	require.ErrorContains(t, err, "field 'with.bastion.key.secret'")
}
