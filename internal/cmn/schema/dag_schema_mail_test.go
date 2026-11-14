// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDAGSchemaMailAccounts(t *testing.T) {
	t.Parallel()
	const source = `
mail_accounts:
  support@example.com:
    provider: microsoft
    oauth:
      provider: microsoft_refresh
      client_id: client
      refresh_token: ${SUPPORT_TOKEN}
      scopes: [https://outlook.office.com/IMAP.AccessAsUser.All]
  billing@example.com:
    imap: {host: imap.example.com, port: 993, security: tls, skip_tls_verify: true}
    smtp: {host: smtp.example.com, port: "587", security: starttls}
    username: billing
    password: ${BILLING_PASSWORD}
  me@gmail.com:
    provider: google
    oauth: {provider: google_refresh, client_id: c, client_secret: s, refresh_token: r}
steps:
  - run: "true"
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	for _, tc := range []struct{ name, from, to string }{
		{"password and oauth", "    password: ${BILLING_PASSWORD}", "    password: p\n    oauth: {provider: google_refresh, client_id: c, refresh_token: r}"},
		{"no credentials", "    password: ${BILLING_PASSWORD}", ""},
		{"unknown provider", "provider: microsoft", "provider: yahoo"},
		{"unknown security", "security: tls", "security: none"},
		{"SMTP-only OAuth provider", "provider: microsoft_refresh", "provider: microsoft"},
		{"unknown server field", "skip_tls_verify: true", "verify: false"},
		{"scopes not a list", "scopes: [https://outlook.office.com/IMAP.AccessAsUser.All]", "scopes: imap"},
		{"server on Gmail API account", "client_secret: s, refresh_token: r}", "client_secret: s, refresh_token: r}\n    imap: {host: imap.gmail.com}"},
		{"SMTP on Gmail API account", "client_secret: s, refresh_token: r}", "client_secret: s, refresh_token: r}\n    smtp: {security: starttls}"},
		{"username on Gmail API account", "client_secret: s, refresh_token: r}", "client_secret: s, refresh_token: r}\n    username: me"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}

func TestDAGSchemaMailActions(t *testing.T) {
	t.Parallel()
	const source = `
steps:
  - id: find
    action: mail.search
    with:
      mailbox: ops@example.com
      folder: INBOX
      unread: true
      from: billing@
      subject: invoice
      within: 24h
      has_attachments: true
      save_attachments: true
      limit: 20
  - id: file
    action: mail.organize
    with:
      mailbox: ops@example.com
      emails: ${steps.find.outputs.messages}
      mark: read
      move: folder
      folder: Invoices
      dry_run: false
  - action: mail.send
    with:
      mailbox: ops@example.com
      to: team@example.com
      subject: Filed
      message: done
  - action: mail.send
    with:
      mailbox: ops@example.com
      in_reply_to: ${steps.find.outputs.messages}
      message: Thanks, we are on it.
`
	resolved := mustResolveDAGSchema(t)
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, source)))
	for _, tc := range []struct{ name, from, to string }{
		{"search without mailbox", "      mailbox: ops@example.com\n      folder: INBOX", "      folder: INBOX"},
		{"limit above 50", "limit: 20", "limit: 80"},
		{"unknown search field", "      limit: 20", "      label: x"},
		{"organize without mark or move", "      mark: read\n      move: folder\n", ""},
		{"unknown mark", "mark: read", "mark: starred"},
		{"unknown move", "move: folder", "move: delete"},
		{"send without mailbox or from", "      mailbox: ops@example.com\n      to: team", "      to: team"},
		{"reply without mailbox", "      mailbox: ops@example.com\n      in_reply_to", "      from: a@example.com\n      in_reply_to"},
		{"send without to or reply", "      to: team@example.com\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParseYAMLDocument(t, strings.Replace(source, tc.from, tc.to, 1))
			require.Error(t, resolved.Validate(doc))
		})
	}
}
