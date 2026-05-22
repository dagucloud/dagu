// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMail exercises mail executor construction: NewMail verifies that valid
// configs (including the attachments field) decode onto mailConfig, and
// MultipleRecipients covers the supported shapes of the `to` field (string,
// []string, []any, and the empty-recipient error path).
func TestMail(t *testing.T) {
	t.Parallel()

	// Create temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "email-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Create a test email attachment
	attachFile := filepath.Join(tmpDir, "email.txt")
	content := []byte("Test email")

	err = os.WriteFile(attachFile, content, 0600)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Remove(attachFile)
	})

	t.Run("NewMail", func(t *testing.T) {
		tests := []struct {
			name string
			step core.Step
		}{
			{
				name: "ValidConfig",
				step: core.Step{
					ExecutorConfig: core.ExecutorConfig{
						Config: map[string]any{
							"from":        "test@example.com",
							"to":          "recipient@example.com",
							"subject":     "Test Subject",
							"message":     "Test Message",
							"attachments": attachFile,
						},
					},
				},
			},
			{
				name: "ValidConfigWithEnv",
				step: core.Step{
					ExecutorConfig: core.ExecutorConfig{
						Config: map[string]any{
							"from":        "test@example.com",
							"to":          "recipient@example.com",
							"subject":     "Test Subject",
							"message":     "Test Message",
							"attachments": attachFile,
						},
					},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := context.Background()
				ctx = runtime.NewContext(ctx, &core.DAG{
					SMTP: &core.SMTPConfig{},
				}, "", "")

				exec, err := newMail(ctx, tt.step)

				assert.NoError(t, err)
				assert.NotNil(t, exec)

				mailExec, ok := exec.(*mail)
				assert.True(t, ok)
				assert.Equal(t, "test@example.com", mailExec.cfg.From)
				assert.Equal(t, "recipient@example.com", mailExec.cfg.To)
				assert.Equal(t, "Test Subject", mailExec.cfg.Subject)
				assert.Equal(t, "Test Message", mailExec.cfg.Message)
				assert.Equal(t, attachFile, mailExec.cfg.Attachments[0])
			})
		}
	})

	t.Run("MultipleRecipients", func(t *testing.T) {
		tests := []struct {
			name      string
			toField   any
			expected  []string
			expectErr bool
		}{
			{
				name:     "SingleRecipientString",
				toField:  "single@example.com",
				expected: []string{"single@example.com"},
			},
			{
				name:     "MultipleRecipientsArray",
				toField:  []string{"user1@example.com", "user2@example.com", "user3@example.com"},
				expected: []string{"user1@example.com", "user2@example.com", "user3@example.com"},
			},
			{
				name:     "MultipleRecipientsAnyArray",
				toField:  []any{"user1@example.com", "user2@example.com"},
				expected: []string{"user1@example.com", "user2@example.com"},
			},
			{
				name:      "EmptyString",
				toField:   "",
				expected:  nil,
				expectErr: true, // Should error because no valid recipients
			},
			{
				name:      "EmptyArray",
				toField:   []string{},
				expected:  nil,
				expectErr: true, // Should error because no valid recipients
			},
			{
				name:     "ArrayWithEmptyStrings",
				toField:  []any{"user1@example.com", "", "user2@example.com"},
				expected: []string{"user1@example.com", "user2@example.com"},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				step := core.Step{
					ExecutorConfig: core.ExecutorConfig{
						Config: map[string]any{
							"from":    "test@example.com",
							"to":      tt.toField,
							"subject": "Test Subject",
							"message": "Test Message",
						},
					},
				}

				ctx := context.Background()
				ctx = runtime.NewContext(ctx, &core.DAG{
					SMTP: &core.SMTPConfig{},
				}, "", "")

				exec, err := newMail(ctx, step)
				assert.NoError(t, err)
				assert.NotNil(t, exec)

				mailExec, ok := exec.(*mail)
				assert.True(t, ok)

				// Test Run method to validate the to field handling
				if tt.expectErr {
					err := mailExec.Run(ctx)
					assert.Error(t, err)
				} else {
					// We can't actually run the mail sending without mocking,
					// but we can verify the config is set correctly
					assert.Equal(t, tt.toField, mailExec.cfg.To)
				}
			})
		}
	})
}

// TestMailRunSendsAttachments is an end-to-end regression test for a bug where
// the mail step decoded `attachments:` from YAML into cfg.Attachments but then
// passed []string{} to the mailer, silently dropping every attachment. It
// stands up an in-process fake SMTP server, drives the mail executor against
// it, and asserts the captured DATA payload references the attachment.
func TestMailRunSendsAttachments(t *testing.T) {
	t.Parallel()

	attachPath := filepath.Join(t.TempDir(), "report.txt")
	attachBody := []byte("hello-from-dagu-attachment-test")
	require.NoError(t, os.WriteFile(attachPath, attachBody, 0600))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	dataCh := make(chan string, 1)
	// serverErr signals fake-SMTP failures only; a clean run leaves it empty
	// so the select below can use it as an unambiguous fail-fast case without
	// racing the happy-path completion.
	serverErr := make(chan error, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if err := runFakeSMTP(conn, dataCh); err != nil {
			serverErr <- err
		}
	}()

	step := core.Step{
		ExecutorConfig: core.ExecutorConfig{
			Config: map[string]any{
				"from":        "sender@example.com",
				"to":          "rcpt@example.com",
				"subject":     "Attachment regression",
				"message":     "Body content",
				"attachments": []string{attachPath},
			},
		},
	}

	ctx := runtime.NewContext(context.Background(), &core.DAG{
		SMTP: &core.SMTPConfig{Host: host, Port: port},
	}, "", "")

	exec, err := newMail(ctx, step)
	require.NoError(t, err)
	exec.SetStdout(io.Discard)
	exec.SetStderr(io.Discard)

	require.NoError(t, exec.Run(ctx))

	select {
	case data := <-dataCh:
		assert.Contains(t, data, "Content-Disposition: attachment; filename=report.txt",
			"mail step must forward cfg.Attachments to the mailer")
		assert.Contains(t, data, base64.StdEncoding.EncodeToString(attachBody),
			"attached file contents must appear (base64) in the message")
	case err := <-serverErr:
		// Fail fast with the real server error instead of waiting for the
		// timeout to elapse and reporting it as "timeout".
		t.Fatalf("fake SMTP server error before DATA capture: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SMTP DATA payload")
	}
}

// runFakeSMTP implements just enough of RFC 5321 to capture the body of a
// single message: greeting → EHLO → MAIL FROM → RCPT TO → DATA → body → QUIT.
// The DATA payload (excluding the trailing "\r\n.\r\n" terminator) is sent on
// dataCh.
func runFakeSMTP(conn net.Conn, dataCh chan<- string) error {
	r := bufio.NewReader(conn)
	write := func(s string) error {
		_, err := conn.Write([]byte(s))
		return err
	}

	if err := write("220 fake.dagu.test ready\r\n"); err != nil {
		return err
	}

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if err := write("250-fake.dagu.test\r\n250 OK\r\n"); err != nil {
				return err
			}
		case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
			if err := write("250 OK\r\n"); err != nil {
				return err
			}
		case cmd == "DATA":
			if err := write("354 End data with <CR><LF>.<CR><LF>\r\n"); err != nil {
				return err
			}
			var body strings.Builder
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return err
				}
				if dataLine == ".\r\n" {
					break
				}
				body.WriteString(dataLine)
			}
			dataCh <- body.String()
			if err := write("250 OK\r\n"); err != nil {
				return err
			}
		case cmd == "QUIT":
			_ = write("221 Bye\r\n")
			return nil
		case cmd == "":
			// blank line — ignore
		default:
			if err := write("502 unrecognized\r\n"); err != nil {
				return err
			}
		}
	}
}
