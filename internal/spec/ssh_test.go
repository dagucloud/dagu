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
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
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

func TestSSHCredentialRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want []secretref.Ref
	}{
		{
			name: "DAGLevelKeyAndPassphrase",
			yaml: `
ssh:
  host: app
  key: {secret: ssh/key}
  passphrase: {secret: ssh/passphrase}
  password: {secret: ssh/unused-password}
steps:
  - run: "true"
`,
			want: []secretref.Ref{
				{Name: "ssh.key", Ref: "ssh/key"},
				{Name: "ssh.passphrase", Ref: "ssh/passphrase"},
			},
		},
		{
			name: "PasswordIgnoresPassphrase",
			yaml: `
ssh:
  host: app
  password: {secret: ssh/password}
  passphrase: {secret: ssh/unused-passphrase}
steps:
  - run: "true"
`,
			want: []secretref.Ref{{Name: "ssh.password", Ref: "ssh/password"}},
		},
		{
			name: "StepsHandlersAndForeachBodies",
			yaml: `
steps:
  - id: deploy
    action: ssh.run
    with:
      host: app
      command: "true"
      key: {secret: ssh/key}
      bastion:
        host: jump
        password: {secret: ssh/jump}
  - id: copy
    action: sftp.upload
    with:
      host: app
      source: a
      destination: b
      key: {secret: ssh/key}
  - id: each
    foreach:
      items: [a, b]
      steps:
        - id: item
          action: ssh.run
          with:
            host: ${foreach.item}
            command: "true"
            passphrase: {secret: ssh/default-key-passphrase}
handler_on:
  failure:
    action: ssh.run
    with:
      host: app
      command: "true"
      password: {secret: ssh/handler}
`,
			want: []secretref.Ref{
				{Name: "steps.deploy.with.key", Ref: "ssh/key"},
				{Name: "steps.deploy.with.bastion.password", Ref: "ssh/jump"},
				{Name: "steps.each.foreach.steps.item.with.passphrase", Ref: "ssh/default-key-passphrase"},
				{Name: "handler_on.failure.with.password", Ref: "ssh/handler"},
			},
		},
		{
			// The base case of the issue: a path key leaves an inherited
			// passphrase unused, so nothing is collected.
			name: "PathKey",
			yaml: `
ssh:
  host: app
  key: /keys/plain
steps:
  - run: "true"
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			assert.Equal(t, tt.want, dag.CredentialRefs)
		})
	}
}

func TestSSHCredentialRefErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "StepBadRef",
			yaml: `
steps:
  - id: deploy
    action: ssh.run
    with: {host: app, command: "true", key: {secret: Bad_Ref}}
`,
			wantErr: "field 'steps.deploy.with.key.secret': invalid secret ref",
		},
		{
			name: "StepInterpolatedRef",
			yaml: `
steps:
  - id: deploy
    action: sftp.download
    with: {host: app, source: a, destination: b, password: {secret: "ssh/${HOST}"}}
`,
			wantErr: "field 'steps.deploy.with.password.secret': secret ref must be literal",
		},
		{
			name: "BastionUnknownField",
			yaml: `
steps:
  - id: deploy
    action: ssh.run
    with: {host: app, command: "true", bastion: {host: jump, key: {secrte: ssh/key}}}
`,
			wantErr: `field 'steps.deploy.with.bastion.key': unknown field "secrte"; use secret or file`,
		},
		{
			name: "DAGLevelBothForms",
			yaml: `
ssh:
  host: app
  key: {secret: ssh/key, file: /keys/k}
steps:
  - run: "true"
`,
			wantErr: "field 'ssh.key': set either secret or file, not both",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
