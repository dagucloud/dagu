// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailbox"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/test/mailtest"
)

const ticketEmail = "From: Carol <carol@example.com>\r\n" +
	"To: support@example.com\r\n" +
	"Subject: Printer is down\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"Third floor.\r\n"

func accountContext(imapServer *mailtest.IMAP, smtpServer *mailtest.SMTP) context.Context {
	account := &ir.MailAccount{
		Provider: ir.MailProviderIMAP,
		IMAP: &ir.MailServer{
			Host: imapServer.Host, Port: imapServer.Port, Security: ir.MailSecurityTLS, SkipTLSVerify: true,
		},
		Username: imapServer.Username,
		Password: imapServer.Password,
	}
	if smtpServer != nil {
		account.SMTP = &ir.MailServer{
			Host: smtpServer.Host, Port: smtpServer.Port, Security: ir.MailSecurityTLS, SkipTLSVerify: true,
		}
	}
	return runtime.NewContext(context.Background(), &ir.DAG{
		MailAccounts: ir.MailAccounts{"support@example.com": account},
	}, "", "")
}

func operationStep(operation string, with map[string]any) ir.Step {
	return ir.Step{
		Name:           operation,
		Commands:       []ir.CommandEntry{{Command: operation}},
		ExecutorConfig: ir.ExecutorConfig{Type: "mail", Config: with},
	}
}

func runStep(t *testing.T, ctx context.Context, step ir.Step) map[string]any {
	t.Helper()
	exec, err := newMail(ctx, step)
	require.NoError(t, err)
	exec.SetStdout(io.Discard)
	exec.SetStderr(io.Discard)
	require.NoError(t, exec.Run(ctx))
	provider, ok := exec.(executor.DeclaredOutputsProvider)
	require.True(t, ok)
	require.True(t, provider.PublishesDeclaredOutputs())
	return provider.GetOutputs()
}

// A found email passes through a loop as compact JSON and is marked read on
// its own, as a For each item body does.
func TestSearchThenOrganizeEachEmail(t *testing.T) {
	t.Parallel()

	imapServer := mailtest.StartIMAP(t)
	uid := imapServer.Append(t, "INBOX", ticketEmail)
	ctx := accountContext(imapServer, nil)

	outputs := runStep(t, ctx, operationStep(opSearch, map[string]any{
		"mailbox": "Support@Example.com",
		"unread":  true,
	}))
	assert.Equal(t, 1, outputs["count"])
	assert.Equal(t, false, outputs["truncated"])
	messages := outputs["messages"].([]mailbox.Message)
	require.Len(t, messages, 1)
	assert.Equal(t, "Printer is down", messages[0].Subject)
	assert.Equal(t, "Third floor.", messages[0].Text)

	item, err := json.Marshal(messages[0])
	require.NoError(t, err)
	outputs = runStep(t, ctx, operationStep(opOrganize, map[string]any{
		"mailbox": "support@example.com",
		"emails":  string(item),
		"mark":    "read",
	}))
	assert.Equal(t, 1, outputs["changed"])
	assert.Equal(t, []string{}, outputs["missing"])
	assert.True(t, imapServer.HasFlag(t, "INBOX", uid, imap.FlagSeen))
}

func TestOrganizeAcceptsAnAnswerList(t *testing.T) {
	t.Parallel()

	imapServer := mailtest.StartIMAP(t)
	imapServer.Append(t, "INBOX", ticketEmail)
	ctx := accountContext(imapServer, nil)
	messages := runStep(t, ctx, operationStep(opSearch, map[string]any{"mailbox": "support@example.com"}))["messages"].([]mailbox.Message)

	answer := `[{"id": "` + messages[0].ID + `", "move_to": "Hardware"}]`
	outputs := runStep(t, ctx, operationStep(opOrganize, map[string]any{
		"mailbox": "support@example.com",
		"emails":  answer,
		"move":    "folder",
	}))
	assert.Equal(t, 1, outputs["changed"])
	assert.Equal(t, []string{"Printer is down"}, imapServer.Subjects(t, "Hardware"))
}

func TestSendThroughMailbox(t *testing.T) {
	t.Parallel()

	imapServer := mailtest.StartIMAP(t)
	smtpServer := mailtest.StartSMTP(t)
	ctx := accountContext(imapServer, smtpServer)

	exec, err := newMail(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mail", Config: map[string]any{
		"mailbox": "support@example.com",
		"to":      "team@example.com",
		"subject": "Tickets filed",
		"message": "2 tickets",
	}}})
	require.NoError(t, err)
	exec.SetStdout(io.Discard)
	exec.SetStderr(io.Discard)
	require.NoError(t, exec.Run(ctx))

	deliveries := smtpServer.Deliveries()
	require.Len(t, deliveries, 1)
	assert.Equal(t, "support@example.com", deliveries[0].From, "from defaults to the mailbox")
	assert.Equal(t, []string{"team@example.com"}, deliveries[0].To)
	assert.Contains(t, deliveries[0].Data, "Subject: Tickets filed")
}

func TestMailboxStepErrors(t *testing.T) {
	t.Parallel()

	imapServer := mailtest.StartIMAP(t)
	imapServer.Append(t, "INBOX", ticketEmail)
	ctx := accountContext(imapServer, nil)
	found := runStep(t, ctx, operationStep(opSearch, map[string]any{"mailbox": "support@example.com"}))
	id := found["messages"].([]mailbox.Message)[0].ID

	tests := []struct {
		name    string
		step    ir.Step
		wantErr string
	}{
		{
			name:    "UnknownMailbox",
			step:    operationStep(opSearch, map[string]any{"mailbox": "other@example.com"}),
			wantErr: `mail account "other@example.com" is not configured`,
		},
		{
			name:    "LimitOutOfRange",
			step:    operationStep(opSearch, map[string]any{"mailbox": "support@example.com", "limit": "80"}),
			wantErr: "with.limit must be an integer from 1 to 50",
		},
		{
			name:    "SaveAttachmentsWithoutArtifacts",
			step:    operationStep(opSearch, map[string]any{"mailbox": "support@example.com", "save_attachments": true}),
			wantErr: "save_attachments requires artifact storage",
		},
		{
			name:    "MalformedID",
			step:    operationStep(opOrganize, map[string]any{"mailbox": "support@example.com", "emails": "nope", "mark": "read"}),
			wantErr: `malformed email ID "nope"`,
		},
		{
			name: "MoveWithoutFolder",
			step: operationStep(opOrganize, map[string]any{
				"mailbox": "support@example.com", "emails": `{"id": "` + id + `"}`, "move": "folder",
			}),
			wantErr: "move: folder needs with.folder or a move_to on every email",
		},
		{
			name: "SendWithoutSMTPServer",
			step: ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mail", Config: map[string]any{
				"mailbox": "support@example.com", "to": "team@example.com", "subject": "s", "message": "m",
			}}},
			wantErr: `mail account "support@example.com" has no SMTP server`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := newMail(ctx, tt.step)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestSearchReportsWrongPasswordWithAccount(t *testing.T) {
	t.Parallel()

	imapServer := mailtest.StartIMAP(t)
	imapServer.Password = "wrong"
	ctx := accountContext(imapServer, nil)

	exec, err := newMail(ctx, operationStep(opSearch, map[string]any{"mailbox": "support@example.com"}))
	require.NoError(t, err)
	exec.SetStdout(io.Discard)
	err = exec.Run(ctx)
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), `mail account "support@example.com": authentication failed`), err.Error())
}
