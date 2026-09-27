// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailbox"
	"github.com/dagucloud/dagu/v2/internal/test/mailtest"
)

const plainInvoice = "From: Alice <alice@example.com>\r\n" +
	"To: billing@example.com\r\n" +
	"Cc: audit@example.com\r\n" +
	"Subject: Invoice 1\r\n" +
	"Date: Mon, 21 Sep 2026 09:00:00 +0000\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Total: 100 USD\r\n"

const seenGreeting = "From: Bob <bob@example.com>\r\n" +
	"To: billing@example.com\r\n" +
	"Subject: Hello\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"Hi\r\n"

const htmlInvoiceWithAttachment = "From: Alice <alice@example.com>\r\n" +
	"To: billing@example.com\r\n" +
	"Subject: Invoice 2\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=b1\r\n" +
	"\r\n" +
	"--b1\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<html><head><style>p{}</style></head><body><p>Total: <b>200</b> &amp; tax</p><ul><li>Item A</li></ul></body></html>\r\n" +
	"--b1\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment; filename=\"invoice 2.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"UERGIGJ5dGVz\r\n" +
	"--b1--\r\n"

func dial(t *testing.T, server *mailtest.IMAP, security string) *mailbox.Client {
	t.Helper()
	client, err := mailbox.Dial(context.Background(), mailbox.Account{
		Server: mailbox.Server{
			Host: server.Host, Port: server.Port, Security: security, SkipTLSVerify: true,
		},
		Username: server.Username,
		Password: server.Password,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestSearch(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	first := server.Append(t, "INBOX", plainInvoice)
	server.Append(t, "INBOX", seenGreeting, imap.FlagSeen)
	server.Append(t, "INBOX", htmlInvoiceWithAttachment)
	client := dial(t, server, mailbox.SecurityTLS)

	messages, err := client.Search(mailbox.SearchOptions{Unread: true, Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 2)

	invoice := messages[0]
	assert.NotEmpty(t, invoice.ID)
	assert.Equal(t, "INBOX", invoice.Folder)
	assert.Equal(t, "Alice", invoice.FromName)
	assert.Equal(t, "alice@example.com", invoice.FromAddress)
	assert.Equal(t, []string{"billing@example.com"}, invoice.To)
	assert.Equal(t, []string{"audit@example.com"}, invoice.Cc)
	assert.Equal(t, "Invoice 1", invoice.Subject)
	assert.Equal(t, "2026-09-21T09:00:00Z", invoice.Date)
	assert.True(t, invoice.Unread)
	assert.False(t, invoice.Flagged)
	assert.Equal(t, "Total: 100 USD", invoice.Text)
	assert.Empty(t, invoice.Attachments)

	html := messages[1]
	assert.Equal(t, "Invoice 2", html.Subject)
	assert.Equal(t, "Total: 200 & tax\n\n- Item A", html.Text)
	assert.Equal(t, []mailbox.Attachment{{Name: "invoice 2.pdf", ContentType: "application/pdf", Size: 9}}, html.Attachments)

	// Searching reads bodies without marking them read.
	assert.False(t, server.HasFlag(t, "INBOX", first, imap.FlagSeen))
}

func TestSearchFilters(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.Append(t, "INBOX", plainInvoice)
	server.Append(t, "INBOX", seenGreeting, imap.FlagSeen)
	server.Append(t, "INBOX", htmlInvoiceWithAttachment)
	client := dial(t, server, mailbox.SecurityTLS)

	subjects := func(opts mailbox.SearchOptions) []string {
		t.Helper()
		opts.Limit = max(opts.Limit, 1)
		messages, err := client.Search(opts)
		require.NoError(t, err)
		out := []string{}
		for _, msg := range messages {
			out = append(out, msg.Subject)
		}
		return out
	}

	assert.Equal(t, []string{"Invoice 1", "Hello", "Invoice 2"}, subjects(mailbox.SearchOptions{Limit: 20}))
	assert.Equal(t, []string{"Invoice 1"}, subjects(mailbox.SearchOptions{Limit: 1}), "oldest first")
	assert.Equal(t, []string{"Hello"}, subjects(mailbox.SearchOptions{From: "BOB", Limit: 20}))
	assert.Equal(t, []string{"Invoice 1", "Invoice 2"}, subjects(mailbox.SearchOptions{Subject: "invoice", Limit: 20}))
	assert.Equal(t, []string{"Invoice 2"}, subjects(mailbox.SearchOptions{HasAttachments: true, Limit: 20}))
}

func TestSearchWithin(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.AppendAt(t, "INBOX", plainInvoice, time.Now().Add(-30*time.Hour))
	server.AppendAt(t, "INBOX", seenGreeting, time.Now().Add(-2*time.Hour))
	client := dial(t, server, mailbox.SecurityTLS)

	messages, err := client.Search(mailbox.SearchOptions{Within: 24 * time.Hour, Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Hello", messages[0].Subject)
}

func TestSearchSavesAttachments(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.Append(t, "INBOX", htmlInvoiceWithAttachment)
	client := dial(t, server, mailbox.SecurityTLS)
	dir := filepath.Join(t.TempDir(), "mail", "find")

	messages, err := client.Search(mailbox.SearchOptions{AttachmentsDir: dir, Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Attachments, 1)
	path := messages[0].Attachments[0].Path
	assert.Equal(t, filepath.Join(dir, "01-invoice_2.pdf"), path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "PDF bytes", string(data))

	// A later search into the same directory, as in a loop, keeps both files.
	messages, err = client.Search(mailbox.SearchOptions{AttachmentsDir: dir, Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "02-invoice_2.pdf"), messages[0].Attachments[0].Path)
}

func TestDialWithSTARTTLS(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t, mailtest.WithSTARTTLS())
	server.Append(t, "INBOX", plainInvoice)
	client := dial(t, server, mailbox.SecurityStartTLS)

	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 1)
}

func TestDialVerifiesCertificate(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	_, err := mailbox.Dial(context.Background(), mailbox.Account{
		Server:   mailbox.Server{Host: server.Host, Port: server.Port, Security: mailbox.SecurityTLS},
		Username: server.Username,
		Password: server.Password,
	})
	require.ErrorContains(t, err, "certificate")
}

func TestDialRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	_, err := mailbox.Dial(context.Background(), mailbox.Account{
		Server:   mailbox.Server{Host: server.Host, Port: server.Port, Security: mailbox.SecurityTLS, SkipTLSVerify: true},
		Username: server.Username,
		Password: "wrong",
	})
	require.ErrorContains(t, err, "authentication failed")
}

func TestOrganize(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.Append(t, "INBOX", plainInvoice)
	server.Append(t, "INBOX", htmlInvoiceWithAttachment)
	server.Append(t, "INBOX", seenGreeting)
	client := dial(t, server, mailbox.SecurityTLS)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 3)

	// The second email names its own destination, as an AI step's answer would.
	result, err := client.Organize(mailbox.OrganizeOptions{
		Items: []mailbox.Item{
			{ID: messages[0].ID},
			{ID: messages[1].ID, MoveTo: "Receipts"},
		},
		Mark:   mailbox.MarkRead,
		Move:   mailbox.MoveFolder,
		Folder: "Invoices",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Changed)
	assert.Empty(t, result.Missing)

	assert.Equal(t, []string{"Hello"}, server.Subjects(t, "INBOX"))
	assert.Equal(t, []string{"Invoice 1"}, server.Subjects(t, "Invoices"), "created on demand")
	assert.Equal(t, []string{"Invoice 2"}, server.Subjects(t, "Receipts"))
	assert.True(t, server.HasFlag(t, "Invoices", 1, imap.FlagSeen))

	// The moved email is no longer in INBOX, so a second pass reports it missing.
	result, err = client.Organize(mailbox.OrganizeOptions{
		Items: []mailbox.Item{{ID: messages[0].ID}, {ID: messages[2].ID}},
		Mark:  mailbox.MarkFlagged,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changed)
	assert.Equal(t, []string{messages[0].ID}, result.Missing)
	assert.True(t, server.HasFlag(t, "INBOX", 3, imap.FlagFlagged))
}

func TestOrganizeSpecialUseFolders(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.CreateFolder(t, "All Mail", imap.MailboxAttrAll)
	server.CreateFolder(t, "Bin", imap.MailboxAttrTrash)
	server.Append(t, "INBOX", plainInvoice)
	server.Append(t, "INBOX", seenGreeting)
	client := dial(t, server, mailbox.SecurityTLS)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)

	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: messages[0].ID}}, Move: mailbox.MoveArchive})
	require.NoError(t, err)
	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: messages[1].ID}}, Move: mailbox.MoveTrash})
	require.NoError(t, err)

	assert.Equal(t, []string{}, server.Subjects(t, "INBOX"))
	assert.Equal(t, []string{"Invoice 1"}, server.Subjects(t, "All Mail"), "\\All stands in for \\Archive")
	assert.Equal(t, []string{"Hello"}, server.Subjects(t, "Bin"))
}

func TestOrganizeDryRunChangesNothing(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	uid := server.Append(t, "INBOX", plainInvoice)
	client := dial(t, server, mailbox.SecurityTLS)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)

	result, err := client.Organize(mailbox.OrganizeOptions{
		Items: []mailbox.Item{{ID: messages[0].ID}}, Mark: mailbox.MarkRead, Move: mailbox.MoveFolder, Folder: "Invoices", DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changed)
	assert.False(t, server.HasFlag(t, "INBOX", uid, imap.FlagSeen))
	assert.Equal(t, []string{"Invoice 1"}, server.Subjects(t, "INBOX"))
}

func TestOrganizeRefusesUnsafeMove(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t, mailtest.WithoutMove())
	server.Append(t, "INBOX", plainInvoice)
	client := dial(t, server, mailbox.SecurityTLS)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)

	_, err = client.Organize(mailbox.OrganizeOptions{
		Items: []mailbox.Item{{ID: messages[0].ID}}, Move: mailbox.MoveFolder, Folder: "Invoices",
	})
	require.ErrorIs(t, err, mailbox.ErrUnsafeMove)
	assert.Equal(t, []string{"Invoice 1"}, server.Subjects(t, "INBOX"))
}

func TestOrganizeRejectsBadInput(t *testing.T) {
	t.Parallel()

	server := mailtest.StartIMAP(t)
	server.Append(t, "INBOX", plainInvoice)
	client := dial(t, server, mailbox.SecurityTLS)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)

	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: "not-an-id"}}, Mark: mailbox.MarkRead})
	require.ErrorContains(t, err, "malformed email ID")

	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: messages[0].ID}}, Move: mailbox.MoveFolder})
	require.ErrorContains(t, err, "needs a folder for every email")
}

func TestFit(t *testing.T) {
	t.Parallel()

	messages := []mailbox.Message{
		{Subject: "a", Text: strings.Repeat("x", mailbox.TextLimit)},
		{Subject: "b", Text: "short"},
	}
	fitted, truncated := mailbox.Fit(messages, 1<<20)
	assert.False(t, truncated)
	assert.Len(t, fitted[0].Text, mailbox.TextLimit)

	fitted, truncated = mailbox.Fit(messages, 2000)
	assert.True(t, truncated)
	require.Len(t, fitted, 2)
	assert.Less(t, len(fitted[0].Text), 2000)
	assert.Equal(t, "short", fitted[1].Text)
}

// Headers can exceed the budget even without text; the newest messages go so
// the oldest are kept for processing.
func TestFitDropsNewestMessagesWhenHeadersAloneAreTooLarge(t *testing.T) {
	t.Parallel()

	recipients := make([]string, 200)
	for i := range recipients {
		recipients[i] = "someone-with-a-long-address@example.com"
	}
	messages := []mailbox.Message{
		{Subject: "oldest", To: recipients},
		{Subject: "middle", To: recipients},
		{Subject: "newest", To: recipients},
	}
	budget := 20000

	fitted, truncated := mailbox.Fit(messages, budget)
	assert.True(t, truncated)
	require.NotEmpty(t, fitted)
	assert.Less(t, len(fitted), 3)
	assert.Equal(t, "oldest", fitted[0].Subject)
	encoded, err := json.Marshal(fitted)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), budget)
}
