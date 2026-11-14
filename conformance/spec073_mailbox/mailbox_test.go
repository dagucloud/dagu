// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec073_mailbox_test

import (
	"encoding/json"
	"net/mail"
	"os"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/test/mailtest"
)

func email(subject, body string) string {
	return "From: Carol <carol@example.com>\r\n" +
		"To: user@example.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Message-ID: <" + strings.ReplaceAll(strings.ToLower(subject), " ", "-") + "@example.com>\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		body + "\r\n"
}

const emailWithAttachment = "From: Carol <carol@example.com>\r\n" +
	"To: user@example.com\r\n" +
	"Subject: Invoice\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=b1\r\n" +
	"\r\n" +
	"--b1\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"See attached.\r\n" +
	"--b1\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
	"\r\n" +
	"PDF\r\n" +
	"--b1--\r\n"

func accountEnv(server *mailtest.IMAP) []string {
	return []string{
		"IMAP_HOST=" + server.Host,
		"IMAP_PORT=" + server.Port,
		"MAIL_PASSWORD=" + server.Password,
	}
}

func readFile(t *testing.T, dagu *harness.Runner, name string) string {
	t.Helper()
	data, err := os.ReadFile(dagu.ProjectPath(name))
	require.NoError(t, err)
	return string(data)
}

func TestSearch(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	first := server.Append(t, "INBOX", email("Printer is down", "Third floor."))
	server.Append(t, "INBOX", email("Old news", "Read already."), imap.FlagSeen)
	server.Append(t, "INBOX", email("VPN broken", "Since morning."))

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(accountEnv(server), "start", "search.yaml").ExpectExitCode(0)

	header, body, _ := strings.Cut(readFile(t, dagu, "search.out"), "\n")
	assert.Equal(t, "count=2 truncated=false", header)
	var messages []map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &messages))
	require.Len(t, messages, 2)
	assert.Equal(t, "Printer is down", messages[0]["subject"], "oldest first")
	assert.Equal(t, "carol@example.com", messages[0]["from_address"])
	assert.Equal(t, "Carol", messages[0]["from_name"])
	assert.Equal(t, "Third floor.", messages[0]["text"])
	assert.Equal(t, true, messages[0]["unread"])
	assert.NotEmpty(t, messages[0]["id"])
	assert.Equal(t, "printer-is-down@example.com", messages[0]["message_id"])
	assert.Equal(t, "VPN broken", messages[1]["subject"])

	assert.False(t, server.HasFlag(t, "INBOX", first, imap.FlagSeen), "searching never marks email read")
}

// One loop item fails: the other email is marked read, and the failed one
// stays unread so the next run takes it again. The loop is partially
// succeeded (Spec 018), so the run reports that rather than a failure.
func TestEachEmailMarkedAfterItsWork(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	good := server.Append(t, "INBOX", email("Printer is down", "Third floor."))
	broken := server.Append(t, "INBOX", email("Broken", "This one fails."))

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(accountEnv(server), "start", "each_mark_read.yaml")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "Partially Succeeded")

	assert.True(t, server.HasFlag(t, "INBOX", good, imap.FlagSeen))
	assert.False(t, server.HasFlag(t, "INBOX", broken, imap.FlagSeen))
}

func TestOrganize(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.Append(t, "INBOX", email("Printer is down", "Third floor."))
	server.Append(t, "INBOX", email("VPN broken", "Since morning."))

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(accountEnv(server), "start", "organize.yaml").ExpectExitCode(0)

	assert.Equal(t, []string{}, server.Subjects(t, "INBOX"))
	assert.Equal(t, []string{"Printer is down", "VPN broken"}, server.Subjects(t, "Processed"), "the folder is created")

	// The moved emails are no longer in INBOX, so the second pass reports them missing.
	header, body, _ := strings.Cut(readFile(t, dagu, "organize.out"), "\n")
	assert.Equal(t, "changed=2 again=0", header)
	var missing []string
	require.NoError(t, json.Unmarshal([]byte(body), &missing))
	assert.Len(t, missing, 2)
}

func TestSaveAttachments(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.Append(t, "INBOX", emailWithAttachment)

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(accountEnv(server), "start", "save_attachments.yaml").ExpectExitCode(0)

	assert.Equal(t, "01-invoice.pdf", strings.TrimSpace(readFile(t, dagu, "attachments.out")))
}

func TestSendThroughMailbox(t *testing.T) {
	t.Parallel()

	imapServer := mailtest.StartIMAP(t)
	smtpServer := mailtest.StartSMTP(t)
	env := append(accountEnv(imapServer), "SMTP_HOST="+smtpServer.Host, "SMTP_PORT="+smtpServer.Port)

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(env, "start", "send.yaml").ExpectExitCode(0)

	deliveries := smtpServer.Deliveries()
	require.Len(t, deliveries, 1)
	assert.Equal(t, "user@example.com", deliveries[0].From, "from defaults to the mailbox")
	assert.Equal(t, []string{"team@example.com"}, deliveries[0].To)
	assert.Contains(t, deliveries[0].Data, "Subject: Tickets filed")
}

// Each found email gets a threaded reply at its reply address.
func TestReplyToEachEmail(t *testing.T) {
	t.Parallel()

	imapServer := mailtest.StartIMAP(t)
	imapServer.Append(t, "INBOX", email("Printer is down", "Third floor."))
	smtpServer := mailtest.StartSMTP(t)
	env := append(accountEnv(imapServer), "SMTP_HOST="+smtpServer.Host, "SMTP_PORT="+smtpServer.Port)

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(env, "start", "reply.yaml").ExpectExitCode(0)

	deliveries := smtpServer.Deliveries()
	require.Len(t, deliveries, 1)
	assert.Equal(t, []string{"carol@example.com"}, deliveries[0].To)
	message, err := mail.ReadMessage(strings.NewReader(deliveries[0].Data))
	require.NoError(t, err)
	assert.Equal(t, "Re: Printer is down", message.Header.Get("Subject"))
	assert.Equal(t, "<printer-is-down@example.com>", message.Header.Get("In-Reply-To"))
	assert.Equal(t, "<printer-is-down@example.com>", message.Header.Get("References"))
}

func TestMailboxErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		command   string
		wantError string
	}{
		{"password_and_oauth.yaml", "validate", `mail account "user@example.com": set exactly one of password or oauth`},
		{"imap_without_host.yaml", "validate", `mail account "user@example.com": imap.host is required`},
		{"smtp_only_oauth_provider.yaml", "validate", "oauth.provider must be google_refresh or microsoft_refresh"},
		{"unknown_security.yaml", "validate", "imap.security must be tls or starttls"},
		{"limit_out_of_range.yaml", "validate", "with.limit must be an integer from 1 to 50"},
		{"bad_within.yaml", "validate", "with.within must be a duration"},
		{"organize_without_mark_or_move.yaml", "validate", "mail.organize requires with.mark or with.move"},
		{"not_configured.yaml", "start", `mail account "missing@example.com" is not configured`},
		{"reply_without_mailbox.yaml", "start", "in_reply_to requires mailbox"},
		{"scopes_on_google_refresh.yaml", "validate", `oauth.scopes is not valid for provider "google_refresh"`},
		{"server_on_gmail_api_account.yaml", "validate", `mail account "user@example.com": imap is not used by a google account with oauth, which uses the Gmail API`},
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
