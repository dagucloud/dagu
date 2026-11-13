// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

func TestParseSSHAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        any
		password   any
		passphrase any
		want       ir.SSHAuth
		wantErr    string
	}{
		{name: "Empty"},
		{
			name:     "Literals",
			key:      "~/.ssh/id_ed25519",
			password: "pw",
			want:     ir.SSHAuth{Key: "~/.ssh/id_ed25519", Password: "pw"},
		},
		{
			name: "KeyFile",
			key:  map[string]any{"file": "/keys/deploy"},
			want: ir.SSHAuth{Key: "/keys/deploy"},
		},
		{
			name:       "Secrets",
			key:        map[string]any{"secret": "ssh/deploy-key"},
			password:   map[string]any{"secret": "ssh/password"},
			passphrase: map[any]any{"secret": "ssh/passphrase"},
			want: ir.SSHAuth{
				KeySecret:        "ssh/deploy-key",
				PasswordSecret:   "ssh/password",
				PassphraseSecret: "ssh/passphrase",
			},
		},
		{
			name:    "BadRefGrammar",
			key:     map[string]any{"secret": "SSH/Key"},
			wantErr: "field 'ssh.key.secret': invalid secret ref: must be a slash-separated lowercase slug ref",
		},
		{
			name:    "InterpolatedRef",
			key:     map[string]any{"secret": "ssh/${HOST}"},
			wantErr: "field 'ssh.key.secret': secret ref must be literal",
		},
		{
			name:    "UnknownField",
			key:     map[string]any{"secrte": "ssh/key"},
			wantErr: `field 'ssh.key': unknown field "secrte"; use secret or file`,
		},
		{
			name:     "FileNotAllowedForPassword",
			password: map[string]any{"file": "/pw"},
			wantErr:  `field 'ssh.password': unknown field "file"; use secret`,
		},
		{
			name:    "SecretAndFile",
			key:     map[string]any{"secret": "ssh/key", "file": "/k"},
			wantErr: "field 'ssh.key': set either secret or file, not both",
		},
		{
			name:    "EmptyObject",
			key:     map[string]any{},
			wantErr: "field 'ssh.key': must be a path, {file: <path>}, or {secret: <ref>}",
		},
		{
			name:       "NonString",
			passphrase: 1234,
			wantErr:    "field 'ssh.passphrase': must be a string or {secret: <ref>}",
		},
		{
			name:    "EmptySecret",
			key:     map[string]any{"secret": ""},
			wantErr: "field 'ssh.key.secret': must be a non-empty secret ref",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ir.ParseSSHAuth("ssh", tt.key, tt.password, tt.passphrase)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSSHConfigSecretRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *ir.SSHConfig
		want []secretref.Ref
	}{
		{name: "Nil"},
		{
			name: "KeyUsesKeyAndPassphrase",
			cfg: &ir.SSHConfig{SSHAuth: ir.SSHAuth{
				KeySecret:        "ssh/key",
				PasswordSecret:   "ssh/password",
				PassphraseSecret: "ssh/passphrase",
			}},
			want: []secretref.Ref{
				{Name: "ssh.key", Ref: "ssh/key"},
				{Name: "ssh.passphrase", Ref: "ssh/passphrase"},
			},
		},
		{
			name: "PasswordWithoutKeyIgnoresPassphrase",
			cfg: &ir.SSHConfig{SSHAuth: ir.SSHAuth{
				Password:         "literal",
				PassphraseSecret: "ssh/passphrase",
			}},
		},
		{
			name: "DefaultKeyUsesPassphrase",
			cfg:  &ir.SSHConfig{SSHAuth: ir.SSHAuth{PassphraseSecret: "ssh/passphrase"}},
			want: []secretref.Ref{{Name: "ssh.passphrase", Ref: "ssh/passphrase"}},
		},
		{
			name: "PathKeyWithSecretPassphrase",
			cfg: &ir.SSHConfig{
				SSHAuth: ir.SSHAuth{Key: "/keys/deploy", PassphraseSecret: "ssh/passphrase"},
				Bastion: &ir.BastionConfig{SSHAuth: ir.SSHAuth{PasswordSecret: "ssh/bastion"}},
			},
			want: []secretref.Ref{
				{Name: "ssh.passphrase", Ref: "ssh/passphrase"},
				{Name: "ssh.bastion.password", Ref: "ssh/bastion"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.cfg.SecretRefs("ssh"))
		})
	}
}
