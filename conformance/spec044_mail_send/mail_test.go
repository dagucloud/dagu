// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec044_mail_send_test

import (
	"net/mail"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestMailSend(t *testing.T) {
	t.Parallel()

	env, messages := receiveMail(t)
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "basic_send.yaml")
	result.ExpectExitCode(0)
	select {
	case body := <-messages:
		message, err := mail.ReadMessage(strings.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, "Test Subject", message.Header.Get("Subject"))
		require.Equal(t, "recipient@example.com", message.Header.Get("To"))
	default:
		t.Fatal("mail.send succeeded without delivering a message")
	}
}

func TestMailConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		command   string
		wantError string
	}{
		{"no_recipients.yaml", "start", "no valid recipients specified"},
		{"missing_attachment.yaml", "start", `attachment "missing.pdf"`},
		{"oauth_password_conflict.yaml", "validate", "mutually exclusive"},
		{"oauth_missing_username.yaml", "validate", "username is required with oauth"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run(tc.command, tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.wantError)
		})
	}
}
