// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailbox"
	"github.com/dagucloud/dagu/v2/internal/test/mailtest"
)

func dialGmail(t *testing.T, server *mailtest.Gmail) *mailbox.Gmail {
	t.Helper()
	client, err := mailbox.DialGmailAt(context.Background(), mailbox.Account{
		Token: func(context.Context) (*oauth2.Token, error) {
			return &oauth2.Token{AccessToken: server.Token}, nil
		},
	}, server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func gmailSubjects(t *testing.T, client *mailbox.Gmail, opts mailbox.SearchOptions) []string {
	t.Helper()
	opts.Limit = max(opts.Limit, 1)
	messages, err := client.Search(opts)
	require.NoError(t, err)
	subjects := []string{}
	for _, msg := range messages {
		subjects = append(subjects, msg.Subject)
	}
	return subjects
}

func TestGmailSearch(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	first := server.Append(t, plainInvoice, "INBOX", "UNREAD")
	server.Append(t, seenGreeting, "INBOX")
	server.Append(t, htmlInvoiceWithAttachment, "INBOX", "UNREAD", "STARRED")
	client := dialGmail(t, server)

	messages, err := client.Search(mailbox.SearchOptions{Unread: true, Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 2)

	invoice := messages[0]
	assert.True(t, mailbox.ValidID(invoice.ID))
	assert.Equal(t, "invoice-1@example.com", invoice.MessageID, "without angle brackets")
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
	assert.Empty(t, html.MessageID, "an email without Message-ID has none")
	assert.True(t, html.Flagged, "starred")
	assert.Equal(t, "Total: 200 & tax\n\n- Item A", html.Text)
	assert.Equal(t, []mailbox.Attachment{{Name: "invoice 2.pdf", ContentType: "application/pdf", Size: 9}}, html.Attachments)
	assert.NotEmpty(t, html.Date, "the time Gmail received it stands in for a missing Date")

	// Searching reads email without marking it read.
	assert.Equal(t, []string{"INBOX", "UNREAD"}, server.Labels(t, first))
}

func TestGmailSearchFilters(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	server.Append(t, plainInvoice, "INBOX", "UNREAD")
	server.Append(t, seenGreeting, "INBOX")
	server.Append(t, htmlInvoiceWithAttachment, "INBOX", "UNREAD")
	client := dialGmail(t, server)

	assert.Equal(t, []string{"Invoice 1", "Hello", "Invoice 2"}, gmailSubjects(t, client, mailbox.SearchOptions{Limit: 20}))
	assert.Equal(t, []string{"Invoice 1"}, gmailSubjects(t, client, mailbox.SearchOptions{Limit: 1}), "oldest first")
	assert.Equal(t, []string{"Hello"}, gmailSubjects(t, client, mailbox.SearchOptions{From: "BOB", Limit: 20}))
	assert.Equal(t, []string{"Invoice 1", "Invoice 2"}, gmailSubjects(t, client, mailbox.SearchOptions{From: "ice@example", Limit: 20}),
		"part of an address matches")
	assert.Equal(t, []string{"Invoice 1", "Invoice 2"}, gmailSubjects(t, client, mailbox.SearchOptions{Subject: "voice", Limit: 20}),
		"part of a word matches")
	assert.Equal(t, []string{"Invoice 2"}, gmailSubjects(t, client, mailbox.SearchOptions{HasAttachments: true, Limit: 20}))
	assert.Equal(t, []string{"Hello"}, gmailSubjects(t, client, mailbox.SearchOptions{From: "bob", Limit: 1}),
		"the limit counts matches only")
}

func TestGmailSearchWithin(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	server.AppendAt(t, plainInvoice, time.Now().Add(-30*time.Hour), "INBOX")
	server.AppendAt(t, seenGreeting, time.Now().Add(-2*time.Hour), "INBOX")
	client := dialGmail(t, server)

	assert.Equal(t, []string{"Hello"}, gmailSubjects(t, client, mailbox.SearchOptions{Within: 24 * time.Hour, Limit: 20}))
}

func TestGmailSearchFolders(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	server.CreateLabel(t, "Receipts")
	server.Append(t, plainInvoice, "Receipts")
	server.Append(t, seenGreeting, "INBOX")
	server.Append(t, htmlInvoiceWithAttachment, "INBOX", "TRASH")
	client := dialGmail(t, server)

	assert.Equal(t, []string{"Invoice 1"}, gmailSubjects(t, client, mailbox.SearchOptions{Folder: "Receipts", Limit: 20}))
	assert.Equal(t, []string{"Hello"}, gmailSubjects(t, client, mailbox.SearchOptions{Folder: "inbox", Limit: 20}),
		"INBOX in any case, without what is in the trash")
	assert.Equal(t, []string{"Invoice 1", "Hello"}, gmailSubjects(t, client, mailbox.SearchOptions{Folder: "[Gmail]/All Mail", Limit: 20}))
	assert.Equal(t, []string{"Invoice 2"}, gmailSubjects(t, client, mailbox.SearchOptions{Folder: "[Gmail]/Trash", Limit: 20}))

	messages, err := client.Search(mailbox.SearchOptions{Folder: "Receipts", Limit: 20})
	require.NoError(t, err)
	assert.Equal(t, "Receipts", messages[0].Folder)

	_, err = client.Search(mailbox.SearchOptions{Folder: "Nope", Limit: 20})
	require.ErrorContains(t, err, `open folder "Nope": no such label`)
}

func TestGmailSearchSavesAttachments(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	server.Append(t, htmlInvoiceWithAttachment, "INBOX")
	client := dialGmail(t, server)
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
}

func TestGmailOrganize(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	first := server.Append(t, plainInvoice, "INBOX", "UNREAD")
	second := server.Append(t, htmlInvoiceWithAttachment, "INBOX", "UNREAD")
	third := server.Append(t, seenGreeting, "INBOX")
	server.CreateLabel(t, "Receipts")
	client := dialGmail(t, server)
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
	assert.Equal(t, []string{"Invoices"}, server.Labels(t, first), "created on demand, read, and out of INBOX")
	assert.Equal(t, []string{"Receipts"}, server.Labels(t, second))

	// The moved email no longer carries INBOX, so a second pass reports it missing.
	result, err = client.Organize(mailbox.OrganizeOptions{
		Items: []mailbox.Item{{ID: messages[0].ID}, {ID: messages[2].ID}},
		Mark:  mailbox.MarkFlagged,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changed)
	assert.Equal(t, []string{messages[0].ID}, result.Missing)
	assert.Equal(t, []string{"INBOX", "STARRED"}, server.Labels(t, third))
}

func TestGmailOrganizeArchiveAndTrash(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	archived := server.Append(t, plainInvoice, "INBOX")
	server.Append(t, seenGreeting, "INBOX")
	server.Append(t, htmlInvoiceWithAttachment, "INBOX")
	client := dialGmail(t, server)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 3)

	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: messages[0].ID}}, Move: mailbox.MoveArchive})
	require.NoError(t, err)
	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: messages[1].ID}}, Move: mailbox.MoveTrash})
	require.NoError(t, err)
	_, err = client.Organize(mailbox.OrganizeOptions{
		Items: []mailbox.Item{{ID: messages[2].ID}}, Move: mailbox.MoveFolder, Folder: "[Gmail]/Trash",
	})
	require.NoError(t, err)

	assert.Equal(t, []string{}, server.Subjects(t, "INBOX"))
	assert.Empty(t, server.Labels(t, archived), "archived email keeps no label")
	assert.Equal(t, []string{"Hello", "Invoice 2"}, server.Subjects(t, "TRASH"))

	// Email in the trash is gone from the folder it was found in.
	result, err := client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: messages[1].ID}}, Mark: mailbox.MarkRead})
	require.NoError(t, err)
	assert.Equal(t, []string{messages[1].ID}, result.Missing)
}

func TestGmailOrganizeDryRunChangesNothing(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	id := server.Append(t, plainInvoice, "INBOX", "UNREAD")
	client := dialGmail(t, server)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)

	result, err := client.Organize(mailbox.OrganizeOptions{
		Items: []mailbox.Item{{ID: messages[0].ID}}, Mark: mailbox.MarkRead, Move: mailbox.MoveFolder, Folder: "Invoices", DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Changed)
	assert.Equal(t, []string{"INBOX", "UNREAD"}, server.Labels(t, id))
}

func TestGmailOrganizeRejectsBadInput(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	server.Append(t, plainInvoice, "INBOX")
	client := dialGmail(t, server)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)

	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: "not-an-id"}}, Mark: mailbox.MarkRead})
	require.ErrorContains(t, err, "malformed email ID")

	imapID := base64.RawURLEncoding.EncodeToString([]byte("v1\x001\x001\x00INBOX"))
	require.True(t, mailbox.ValidID(imapID))
	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: imapID}}, Mark: mailbox.MarkRead})
	require.ErrorContains(t, err, "malformed email ID", "an IMAP mailbox's ID names no Gmail email")

	_, err = client.Organize(mailbox.OrganizeOptions{Items: []mailbox.Item{{ID: messages[0].ID}}, Move: mailbox.MoveFolder})
	require.ErrorContains(t, err, "needs a folder for every email")
}

func TestGmailReplyInfo(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	question := server.Append(t, threadedQuestion, "INBOX")
	invoice := server.Append(t, plainInvoice, "INBOX")
	client := dialGmail(t, server)
	messages, err := client.Search(mailbox.SearchOptions{Limit: 20})
	require.NoError(t, err)
	require.Len(t, messages, 2)

	info, err := client.ReplyInfo(messages[0].ID)
	require.NoError(t, err)
	assert.Equal(t, &mailbox.ReplyInfo{
		MessageID:  "question-2@example.com",
		References: []string{"question-0@example.com", "question-1@example.com"},
		Subject:    "Re: Printer is down",
		ReplyTo:    "help@example.com",
		ThreadID:   "t-" + question,
	}, info, "Reply-To wins over From")

	info, err = client.ReplyInfo(messages[1].ID)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", info.ReplyTo, "From when there is no Reply-To")
	assert.Empty(t, info.References)

	server.Delete(t, invoice)
	_, err = client.ReplyInfo(messages[1].ID)
	require.ErrorIs(t, err, mailbox.ErrEmailGone)
}

func TestGmailSend(t *testing.T) {
	t.Parallel()

	server := mailtest.StartGmail(t)
	client := dialGmail(t, server)

	require.NoError(t, client.Send([]byte(seenGreeting), "t-m1"))
	assert.Equal(t, []mailtest.GmailSent{{Raw: seenGreeting, ThreadID: "t-m1"}}, server.Sent())
}

func TestGmailErrors(t *testing.T) {
	t.Parallel()

	t.Run("SignInFails", func(t *testing.T) {
		t.Parallel()
		revoked := errors.New("invalid_grant")
		_, err := mailbox.DialGmailAt(context.Background(), mailbox.Account{
			Token: func(context.Context) (*oauth2.Token, error) { return nil, revoked },
		}, mailtest.StartGmail(t).URL)
		require.ErrorIs(t, err, revoked)
	})

	t.Run("NoGmailScope", func(t *testing.T) {
		t.Parallel()
		server := mailtest.StartGmail(t)
		client := dialGmail(t, server)
		server.DenyScope()
		_, err := client.Search(mailbox.SearchOptions{Limit: 20})
		require.ErrorContains(t, err, "the sign-in does not grant Gmail access")
	})

	t.Run("OtherRefusal", func(t *testing.T) {
		t.Parallel()
		server := mailtest.StartGmail(t)
		client, err := mailbox.DialGmailAt(context.Background(), mailbox.Account{
			Token: func(context.Context) (*oauth2.Token, error) { return &oauth2.Token{AccessToken: "stale"}, nil },
		}, server.URL)
		require.NoError(t, err)
		_, err = client.Search(mailbox.SearchOptions{Limit: 20})
		require.ErrorContains(t, err, "gmail: Request had invalid authentication credentials.", "Google's own message")
	})
}
