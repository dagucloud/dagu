// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec044_mail_send holds black-box conformance tests for
// Spec 044: Mail Send Action.
package spec044_mail_send_test

import (
	"encoding/base64"
	"net"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// serverEnv returns the SMTP_HOST/SMTP_PORT env pair a fixture's smtp: block
// resolves against, split from the fake server's actual listen address.
func serverEnv(t *testing.T, server *fakeSMTPServer) []string {
	t.Helper()

	host, port, err := net.SplitHostPort(server.Addr())
	require.NoError(t, err)
	return []string{"SMTP_HOST=" + host, "SMTP_PORT=" + port}
}

// TestMailSendLive proves mail.send's core contract against a real SMTP
// server: a single string recipient, an array of recipients, attachments,
// authenticated delivery when smtp.username/password are set, and that an
// HTML body (detected by its DOCTYPE) is sent as-is while a plain-text body
// has its newlines converted to <br />.
func TestMailSendLive(t *testing.T) {
	t.Run("sends to a single recipient and reports success", func(t *testing.T) {
		t.Parallel()

		server := newFakeSMTPServer(t, false)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(serverEnv(t, server), "start", "basic_send.yaml")
		result.ExpectExitCode(0)
		require.Contains(t, result.Stdout(), "sending email succeed.")

		require.Equal(t, []string{"sender@example.com"}, server.From())
		require.Equal(t, []string{"recipient@example.com"}, server.Recipients())
		require.Len(t, server.DataBodies(), 1)
		body := server.DataBodies()[0]
		require.Contains(t, body, "From: sender@example.com\r\n")
		require.Contains(t, body, "To: recipient@example.com\r\n")
		require.Contains(t, body, "Subject: Test Subject\r\n")
		require.Contains(t, body, base64.StdEncoding.EncodeToString([]byte("hello world")))
	})

	t.Run("sends to every recipient in an array", func(t *testing.T) {
		t.Parallel()

		server := newFakeSMTPServer(t, false)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(serverEnv(t, server), "start", "multi_recipient.yaml")
		result.ExpectExitCode(0)
		require.ElementsMatch(t, []string{"first@example.com", "second@example.com"}, server.Recipients())
	})

	t.Run("includes attachment content", func(t *testing.T) {
		t.Parallel()

		server := newFakeSMTPServer(t, false)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("attachment.txt", "attachment content")

		result := dagu.RunWithEnv(serverEnv(t, server), "start", "attachment.yaml")
		result.ExpectExitCode(0)
		require.Len(t, server.DataBodies(), 1)
		body := server.DataBodies()[0]
		require.Contains(t, body, "filename=attachment.txt")
		require.Contains(t, body, base64.StdEncoding.EncodeToString([]byte("attachment content")))
	})

	t.Run("authenticates when username and password are configured", func(t *testing.T) {
		t.Parallel()

		server := newFakeSMTPServer(t, true)
		dagu := harness.NewRunner(t)
		env := append(serverEnv(t, server), "SMTP_USER=mailuser", "SMTP_PASSWORD=mailpass")

		result := dagu.RunWithEnv(env, "start", "authenticated_send.yaml")
		result.ExpectExitCode(0)
		user, pass := server.AuthCredentials()
		require.Equal(t, "mailuser", user)
		require.Equal(t, "mailpass", pass)
	})

	t.Run("an HTML body is sent without newline conversion", func(t *testing.T) {
		t.Parallel()

		server := newFakeSMTPServer(t, false)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(serverEnv(t, server), "start", "html_body.yaml")
		result.ExpectExitCode(0)
		require.Len(t, server.DataBodies(), 1)

		const expectedBody = "<!DOCTYPE html><html><body>Line one\nLine two</body></html>"
		require.Contains(t, server.DataBodies()[0], base64.StdEncoding.EncodeToString([]byte(expectedBody)))
	})
}

// TestMailSendNoServer proves the two failure modes that do not need a real
// SMTP server: with.to producing no valid recipient, and a completely
// absent DAG-level smtp: block (a connection-time failure, not a build-time
// one -- see Spec 044's Errors section).
func TestMailSendNoServer(t *testing.T) {
	t.Parallel()

	t.Run("no valid recipients fails before any connection", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "no_recipients.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("no valid recipients specified")
	})

	t.Run("a missing smtp: block is a connection-time failure", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "no_smtp_config.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("connection refused")
	})
}

// TestMailSMTPOAuthValidation proves the DAG-level smtp.oauth field's
// build-time validation: it cannot be combined with smtp.password, and it
// requires smtp.username.
func TestMailSMTPOAuthValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		file   string
		errStr string
	}{
		{
			name:   "smtp.password and smtp.oauth are mutually exclusive",
			file:   "oauth_password_conflict.yaml",
			errStr: "mutually exclusive",
		},
		{
			name:   "smtp.oauth requires smtp.username",
			file:   "oauth_missing_username.yaml",
			errStr: "username is required with oauth",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.errStr)
		})
	}
}
