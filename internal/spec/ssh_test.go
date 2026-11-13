// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

func TestSSHCredentialInheritance(t *testing.T) {
	t.Parallel()

	const base = `
ssh:
  user: deploy
  host: app.example.com
  key: {secret: ssh/deploy-key}
  passphrase: {secret: ssh/deploy-passphrase}
  password: {secret: ssh/deploy-password}
  bastion:
    host: jump.example.com
    key: {secret: ssh/jump-key}
    passphrase: {secret: ssh/jump-passphrase}
`

	tests := []struct {
		name        string
		dag         string
		wantAuth    ir.SSHAuth
		wantBastion ir.SSHAuth
	}{
		{
			name: "InheritsAll",
			dag:  "ssh: {host: other.example.com}\n",
			wantAuth: ir.SSHAuth{
				KeySecret:        "ssh/deploy-key",
				PassphraseSecret: "ssh/deploy-passphrase",
				PasswordSecret:   "ssh/deploy-password",
			},
			wantBastion: ir.SSHAuth{KeySecret: "ssh/jump-key", PassphraseSecret: "ssh/jump-passphrase"},
		},
		{
			// A key replaces the inherited key in any form and drops the
			// inherited passphrase, which belonged to the old key.
			name: "KeyDropsInheritedPassphrase",
			dag: `
ssh:
  key: /keys/plain
  bastion: {key: {file: /keys/jump}}
`,
			wantAuth:    ir.SSHAuth{Key: "/keys/plain", PasswordSecret: "ssh/deploy-password"},
			wantBastion: ir.SSHAuth{Key: "/keys/jump"},
		},
		{
			name: "PasswordAndPassphraseReplaceAlone",
			dag: `
ssh:
  password: literal
  passphrase: {secret: ssh/other-passphrase}
`,
			wantAuth: ir.SSHAuth{
				KeySecret:        "ssh/deploy-key",
				PassphraseSecret: "ssh/other-passphrase",
				Password:         "literal",
			},
			wantBastion: ir.SSHAuth{KeySecret: "ssh/jump-key", PassphraseSecret: "ssh/jump-passphrase"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			basePath := filepath.Join(dir, "base.yaml")
			require.NoError(t, os.WriteFile(basePath, []byte(base), 0600))
			dagPath := filepath.Join(dir, "dag.yaml")
			require.NoError(t, os.WriteFile(dagPath, []byte(tt.dag+"steps:\n  - run: \"true\"\n"), 0600))

			dag, err := spec.Load(context.Background(), dagPath, spec.WithBaseConfig(basePath))
			require.NoError(t, err)
			require.NotNil(t, dag.SSH)
			require.NotNil(t, dag.SSH.Bastion)
			assert.Equal(t, tt.wantAuth, dag.SSH.SSHAuth)
			assert.Equal(t, tt.wantBastion, dag.SSH.Bastion.SSHAuth)
		})
	}
}

// A workspace base config is merged into the global one as raw maps before
// either is built, so its credentials must replace the global ones whole.
func TestSSHCredentialInheritanceAcrossBaseConfigs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	globalBase := filepath.Join(root, "base.yaml")
	require.NoError(t, os.WriteFile(globalBase, []byte(`
ssh:
  user: deploy
  host: app.example.com
  key: {secret: ssh/deploy-key}
  passphrase: {secret: ssh/deploy-passphrase}
  bastion:
    host: jump.example.com
    key: {secret: ssh/jump-key}
    passphrase: {secret: ssh/jump-passphrase}
`), 0600))
	workspaceDir := filepath.Join(root, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, "ops"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "ops", "base.yaml"), []byte(`
ssh:
  key: {file: /keys/ops}
  bastion:
    passphrase: {secret: ssh/ops-jump-passphrase}
`), 0600))
	dagPath := filepath.Join(root, "dag.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
labels:
  - workspace=ops
steps:
  - run: "true"
`), 0600))

	dag, err := spec.Load(context.Background(), dagPath,
		spec.WithBaseConfig(globalBase),
		spec.WithWorkspaceBaseConfigDir(workspaceDir),
	)
	require.NoError(t, err)

	require.NotNil(t, dag.SSH)
	assert.Equal(t, ir.SSHAuth{Key: "/keys/ops"}, dag.SSH.SSHAuth)
	require.NotNil(t, dag.SSH.Bastion)
	assert.Equal(t, "jump.example.com", dag.SSH.Bastion.Host)
	assert.Equal(t, ir.SSHAuth{
		KeySecret:        "ssh/jump-key",
		PassphraseSecret: "ssh/ops-jump-passphrase",
	}, dag.SSH.Bastion.SSHAuth)
}
