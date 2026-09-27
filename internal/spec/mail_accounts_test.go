// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMailAccounts(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
mail_accounts:
  Support@Example.com:
    provider: microsoft
    oauth:
      provider: microsoft_refresh
      client_id: client
      refresh_token: ${SUPPORT_TOKEN}
      scopes: [https://outlook.office.com/IMAP.AccessAsUser.All]
  team@gmail.com:
    provider: google
    smtp: {security: starttls}
    password: app-password
  billing@example.com:
    username: billing
    imap: {host: imap.example.com, skip_tls_verify: true}
    smtp: {host: smtp.example.com, port: 2525, security: starttls}
    password: ${BILLING_PASSWORD}
steps:
  - run: "true"
`))
	require.NoError(t, err)

	assert.Equal(t, ir.MailAccounts{
		"support@example.com": {
			Provider: ir.MailProviderMicrosoft,
			IMAP:     &ir.MailServer{Host: "outlook.office365.com", Port: "993", Security: ir.MailSecurityTLS},
			SMTP:     &ir.MailServer{Host: "smtp.office365.com", Port: "587", Security: ir.MailSecurityStartTLS},
			Username: "support@example.com",
			OAuth: &oauthconfig.Config{
				Provider: oauthconfig.ProviderMicrosoftRefresh, ClientID: "client", RefreshToken: "${SUPPORT_TOKEN}",
				Scopes: []string{"https://outlook.office.com/IMAP.AccessAsUser.All"},
			},
		},
		"team@gmail.com": {
			Provider: ir.MailProviderGoogle,
			IMAP:     &ir.MailServer{Host: "imap.gmail.com", Port: "993", Security: ir.MailSecurityTLS},
			// Changing only the security mode moves to that mode's standard port.
			SMTP:     &ir.MailServer{Host: "smtp.gmail.com", Port: "587", Security: ir.MailSecurityStartTLS},
			Username: "team@gmail.com",
			Password: "app-password",
		},
		"billing@example.com": {
			Provider: ir.MailProviderIMAP,
			IMAP:     &ir.MailServer{Host: "imap.example.com", Port: "993", Security: ir.MailSecurityTLS, SkipTLSVerify: true},
			SMTP:     &ir.MailServer{Host: "smtp.example.com", Port: "2525", Security: ir.MailSecurityStartTLS},
			Username: "billing",
			Password: "${BILLING_PASSWORD}",
		},
	}, dag.MailAccounts)
}

func TestMailAccountsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		account string
		wantErr string
	}{
		{
			name:    "PasswordAndOAuth",
			account: "{imap: {host: h}, password: p, oauth: {provider: microsoft_refresh, client_id: c, refresh_token: r}}",
			wantErr: `mail account "ops@example.com": set exactly one of password or oauth`,
		},
		{
			name:    "NoCredentials",
			account: "{imap: {host: h}}",
			wantErr: `mail account "ops@example.com": set exactly one of password or oauth`,
		},
		{
			name:    "IMAPWithoutHost",
			account: "{password: p}",
			wantErr: `mail account "ops@example.com": imap.host is required`,
		},
		{
			name:    "UnknownProvider",
			account: "{provider: yahoo, password: p}",
			wantErr: `mail account "ops@example.com": provider must be google, microsoft, or imap`,
		},
		{
			name:    "UnknownSecurity",
			account: "{imap: {host: h, security: none}, password: p}",
			wantErr: `mail account "ops@example.com": imap.security must be tls or starttls`,
		},
		{
			name:    "SMTPOnlyOAuthProvider",
			account: "{provider: microsoft, oauth: {provider: microsoft, tenant_id: t, client_id: c, client_secret: s}}",
			wantErr: `mail account "ops@example.com": oauth.provider must be google_refresh or microsoft_refresh`,
		},
		{
			name:    "MissingOAuthField",
			account: "{provider: google, oauth: {provider: google_refresh, client_id: c, refresh_token: r}}",
			wantErr: `mail account "ops@example.com": oauth.client_secret is required`,
		},
		{
			// Without a port there is nothing to derive the standard port from.
			name:    "ReferencedSecurityWithoutPort",
			account: "{imap: {host: h, security: '${IMAP_SECURITY}'}, password: p}",
			wantErr: `mail account "ops@example.com": imap.port is required when imap.security is a value reference`,
		},
		{
			name:    "ScopesOnGoogleRefresh",
			account: "{provider: google, oauth: {provider: google_refresh, client_id: c, client_secret: s, refresh_token: r, scopes: [x]}}",
			wantErr: `mail account "ops@example.com": oauth.scopes is not valid for provider "google_refresh"`,
		},
		{
			name:    "UnknownField",
			account: "{imap: {host: h, tls: true}, password: p}",
			wantErr: `mail account "ops@example.com": unknown field "imap.tls"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(`
mail_accounts:
  ops@example.com: `+tt.account+`
steps:
  - run: "true"
`))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestMailAccountsReferencedSecurity(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
mail_accounts:
  ops@example.com:
    imap: {host: imap.example.com, port: 993, security: '${IMAP_SECURITY}'}
    password: p
steps:
  - run: "true"
`))
	require.NoError(t, err)
	assert.Equal(t, &ir.MailServer{Host: "imap.example.com", Port: "993", Security: "${IMAP_SECURITY}"},
		dag.MailAccounts["ops@example.com"].IMAP)
}

func TestMailAccountsKeyErrors(t *testing.T) {
	t.Parallel()

	_, err := spec.LoadYAML(context.Background(), []byte(`
mail_accounts:
  not-an-address: {imap: {host: h}, password: p}
steps:
  - run: "true"
`))
	require.ErrorContains(t, err, `mail account "not-an-address": not a valid email address`)

	_, err = spec.LoadYAML(context.Background(), []byte(`
mail_accounts:
  ops@example.com: {imap: {host: a}, password: p}
  OPS@example.com: {imap: {host: b}, password: p}
steps:
  - run: "true"
`))
	require.ErrorContains(t, err, `mail account "ops@example.com" is defined more than once`)
}

// A DAG entry replaces the whole base entry for the same address, so base
// credentials never mix with the DAG's.
func TestMailAccountsInheritPerAddress(t *testing.T) {
	t.Parallel()

	base := createTempYAMLFile(t, `
mail_accounts:
  Ops@Example.com:
    provider: google
    oauth: {provider: google_refresh, client_id: c, client_secret: s, refresh_token: r}
  alerts@example.com:
    imap: {host: imap.example.com}
    password: base-password
`)
	dagPath := createTempYAMLFile(t, `
mail_accounts:
  ops@example.com:
    imap: {host: imap.ops.example.com}
    password: dag-password
steps:
  - run: "true"
`)

	dag, err := spec.Load(context.Background(), dagPath, spec.WithBaseConfig(base))
	require.NoError(t, err)

	require.Len(t, dag.MailAccounts, 2)
	ops := dag.MailAccounts["ops@example.com"]
	require.NotNil(t, ops)
	assert.Equal(t, ir.MailProviderIMAP, ops.Provider)
	assert.Equal(t, "imap.ops.example.com", ops.IMAP.Host)
	assert.Equal(t, "dag-password", ops.Password)
	assert.Nil(t, ops.OAuth)
	assert.Equal(t, "base-password", dag.MailAccounts["alerts@example.com"].Password)
}

func TestMailAccountsWorkspaceBaseReplacesGlobalEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	globalBase := filepath.Join(root, "base.yaml")
	require.NoError(t, os.WriteFile(globalBase, []byte(`
mail_accounts:
  OPS@example.com:
    provider: google
    oauth: {provider: google_refresh, client_id: c, client_secret: s, refresh_token: r}
`), 0600))
	workspaceDir := filepath.Join(root, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, "ops"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "ops", "base.yaml"), []byte(`
mail_accounts:
  ops@example.com:
    imap: {host: imap.example.com}
    password: workspace-password
`), 0600))
	dagPath := createTempYAMLFile(t, `
labels:
  - workspace=ops
steps:
  - run: "true"
`)

	dag, err := spec.Load(context.Background(), dagPath,
		spec.WithBaseConfig(globalBase),
		spec.WithWorkspaceBaseConfigDir(workspaceDir),
	)
	require.NoError(t, err)

	require.Len(t, dag.MailAccounts, 1)
	ops := dag.MailAccounts["ops@example.com"]
	require.NotNil(t, ops)
	assert.Equal(t, "workspace-password", ops.Password)
	assert.Nil(t, ops.OAuth)
}
