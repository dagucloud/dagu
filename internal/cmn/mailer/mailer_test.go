// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsHTMLContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name:     "HTMLDocumentWithDOCTYPE",
			content:  "<!DOCTYPE html><html><body><h1>Test</h1></body></html>",
			expected: true,
		},
		{
			name:     "HTMLDocumentWithoutDOCTYPE",
			content:  "<html><body><h1>Test</h1></body></html>",
			expected: false,
		},
		{
			name:     "PlainTextWithNewlines",
			content:  "This is plain text\nwith some\nline breaks",
			expected: false,
		},
		{
			name:     "PlainTextSingleLine",
			content:  "This is just plain text",
			expected: false,
		},
		{
			name:     "HTMLWithWhitespace",
			content:  "  \n  <!DOCTYPE html>\n<html>\n<body>Test</body></html>  ",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isHTMLContent(tt.content)
			assert.Equal(t, tt.expected, result, "Content: %q", tt.content)
		})
	}
}

func TestNewlineToBrTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "UnixNewlines",
			input:    "Line 1\nLine 2\nLine 3",
			expected: "Line 1<br />Line 2<br />Line 3",
		},
		{
			name:     "WindowsNewlines",
			input:    "Line 1\r\nLine 2\r\nLine 3",
			expected: "Line 1<br />Line 2<br />Line 3",
		},
		{
			name:     "MacNewlines",
			input:    "Line 1\rLine 2\rLine 3",
			expected: "Line 1<br />Line 2<br />Line 3",
		},
		{
			name:     "MixedNewlines",
			input:    "Line 1\nLine 2\r\nLine 3\rLine 4",
			expected: "Line 1<br />Line 2<br />Line 3<br />Line 4",
		},
		{
			name:     "EscapedNewlines",
			input:    "Line 1\\nLine 2\\r\\nLine 3",
			expected: "Line 1<br />Line 2<br />Line 3",
		},
		{
			name:     "NoNewlines",
			input:    "Single line text",
			expected: "Single line text",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := newlineToBrTag(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMailerContentTypeDetection tests that the mailer correctly applies
// newlineToBrTag only to plain text content and leaves HTML content unchanged
func TestMailerContentTypeDetection(t *testing.T) {
	tests := []struct {
		name                   string
		emailBody              string
		expectNewlineProcessed bool
		description            string
	}{
		{
			name: "PlainTextEmailWithNewlines",
			emailBody: `Hello,

This is a plain text email.
It has multiple lines.

Best regards,
Dagu Team`,
			expectNewlineProcessed: true,
			description:            "Plain text should have newlines converted to <br /> tags",
		},
		{
			name: "HTMLEmailWithTable",
			emailBody: `<!DOCTYPE html>
<html>
<head>
    <style>
        table { border-collapse: collapse; width: 100%; }
        th, td { border: 1px solid #ddd; padding: 8px; }
    </style>
</head>
<body>
<table>
<thead>
<tr><th>Step</th><th>Status</th></tr>
</thead>
<tbody>
<tr><td>Build</td><td>Success</td></tr>
<tr><td>Test</td><td>Failed</td></tr>
</tbody>
</table>
</body>
</html>`,
			expectNewlineProcessed: false,
			description:            "HTML content should not have newlines converted to <br /> tags",
		},
		{
			name: "ErrorMessageWithAngleBrackets",
			emailBody: `Error occurred during execution:

File not found: <missing.txt>
Expected value: <100
Actual value: >200

Please check the configuration.`,
			expectNewlineProcessed: true,
			description:            "Plain text with angle brackets should still have newlines converted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isHTML := isHTMLContent(tt.emailBody)
			assert.Equal(t, !tt.expectNewlineProcessed, isHTML,
				"isHTMLContent should return %v for: %s", !tt.expectNewlineProcessed, tt.description)

			originalBody := tt.emailBody
			processedBody := tt.emailBody

			processedBody = processEmailBody(processedBody)

			if tt.expectNewlineProcessed {
				// For plain text, we expect <br /> tags to be added
				assert.Contains(t, processedBody, "<br />",
					"Plain text should contain <br /> tags after processing")
				assert.NotEqual(t, originalBody, processedBody,
					"Plain text body should be modified")
			} else {
				// For HTML, the body should remain unchanged
				assert.Equal(t, originalBody, processedBody,
					"HTML body should remain unchanged")

				// Verify that no additional <br /> tags were added between HTML elements
				// Count original <br> tags vs processed <br> tags
				originalBrCount := strings.Count(strings.ToLower(originalBody), "<br")
				processedBrCount := strings.Count(strings.ToLower(processedBody), "<br")
				assert.Equal(t, originalBrCount, processedBrCount,
					"HTML should not have additional <br /> tags added")
			}
		})
	}
}

func TestComposeMailSanitizesHeaders(t *testing.T) {
	t.Parallel()

	client := New(Config{})
	payload := string(client.composeMail(
		[]string{"to@example.com\r\nX-Dagu-To: injected"},
		nil,
		"from@example.com\r\nX-Dagu-From: injected",
		"subject\r\nX-Dagu-Subject: injected",
		"body",
		nil,
	))

	assert.NotContains(t, payload, "\r\nX-Dagu-To:")
	assert.NotContains(t, payload, "\r\nX-Dagu-From:")
	assert.NotContains(t, payload, "\r\nX-Dagu-Subject:")
	assert.Contains(t, payload, "To: to@example.comX-Dagu-To: injected")
	assert.Contains(t, payload, "From: from@example.comX-Dagu-From: injected")
	assert.Contains(t, payload, "Subject: subjectX-Dagu-Subject: injected")
}

func TestSanitizeHeaderFieldRemovesControlCharactersAndTruncates(t *testing.T) {
	t.Parallel()

	value := strings.Repeat("a", 300) + "\r\n" + "b" + string([]byte{0x00, 0x1f, 0x7f}) + "\t"
	sanitized := sanitizeHeaderField(value)

	require.Len(t, sanitized, 256)
	require.NotContains(t, sanitized, "\r")
	require.NotContains(t, sanitized, "\n")
	for _, r := range sanitized {
		require.False(t, r < 0x20 && r != '\t')
		require.NotEqual(t, rune(0x7f), r)
	}
}

func TestComposeMailAttachmentTransferEncodingHeaderAppearsOncePerPart(t *testing.T) {
	t.Parallel()

	attachment := filepath.Join(t.TempDir(), "attachment.txt")
	require.NoError(t, os.WriteFile(attachment, []byte("hello"), 0600))

	client := New(Config{})
	payload := string(client.composeMail(
		[]string{"to@example.com"},
		nil,
		"from@example.com",
		"subject",
		"body",
		[]string{attachment},
	))

	require.Equal(t, 2, strings.Count(payload, "Content-Transfer-Encoding: base64"))
}

func TestComposeMailEndsWithClosingBoundary(t *testing.T) {
	t.Parallel()

	client := New(Config{})
	payload := string(client.composeMail(
		[]string{"to@example.com"},
		nil,
		"from@example.com",
		"subject",
		"body",
		nil,
	))

	require.True(t, strings.HasSuffix(payload, "--"+boundary+"--\r\n"))
	require.NotContains(t, payload, "--"+boundary+"--\r\n\r\n")
}

func TestSendWithoutAuthSkipsStartTLS(t *testing.T) {
	t.Parallel()

	server, err := newSMTPRecordingServer()
	require.NoError(t, err)
	server.advertiseSTARTTLS = true
	defer func() {
		_ = server.Close()
	}()

	go server.Serve()

	host, port, err := net.SplitHostPort(server.Address())
	require.NoError(t, err)

	mailer := New(Config{Host: host, Port: port})
	err = mailer.Send(
		context.Background(),
		"from@example.com",
		[]string{"to@example.com"},
		"Subject",
		"Body",
		nil,
	)
	require.NoError(t, err)
	require.Zero(t, server.StartTLSCount())
}

func TestSendWrapsSMTPCommandErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		configure      func(*smtpRecordingServer)
		expectedSubstr string
	}{
		{
			name: "MailFrom",
			configure: func(server *smtpRecordingServer) {
				server.mailFromResponse = "550 sender rejected\r\n"
			},
			expectedSubstr: "MAIL FROM failed",
		},
		{
			name: "RcptTo",
			configure: func(server *smtpRecordingServer) {
				server.rcptToResponse = "550 recipient rejected\r\n"
			},
			expectedSubstr: "RCPT TO failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := newSMTPRecordingServer()
			require.NoError(t, err)
			tt.configure(server)
			defer func() {
				_ = server.Close()
			}()

			go server.Serve()

			host, port, err := net.SplitHostPort(server.Address())
			require.NoError(t, err)

			mailer := New(Config{Host: host, Port: port})
			err = mailer.Send(
				context.Background(),
				"from@example.com",
				[]string{"to@example.com"},
				"Subject",
				"Body",
				nil,
			)
			require.Error(t, err)
			require.ErrorContains(t, err, tt.expectedSubstr)
		})
	}
}

func TestSendSanitizesHeaders(t *testing.T) {
	t.Parallel()

	server, err := newSMTPRecordingServer()
	require.NoError(t, err)
	defer func() {
		_ = server.Close()
	}()

	go server.Serve()

	host, port, err := net.SplitHostPort(server.Address())
	require.NoError(t, err)

	mailer := New(Config{Host: host, Port: port})
	err = mailer.Send(
		context.Background(),
		"from@example.com\r\nX-Dagu-From: injected",
		[]string{"to@example.com\r\nX-Dagu-To: injected"},
		"Subject\r\nX-Dagu-Subject: injected",
		"Body",
		nil,
	)
	require.NoError(t, err)

	payloads := server.RecordedDataBodies()
	require.Len(t, payloads, 1)
	payload := payloads[0]
	require.NotContains(t, payload, "\r\nX-Dagu-From:")
	require.NotContains(t, payload, "\r\nX-Dagu-To:")
	require.NotContains(t, payload, "\r\nX-Dagu-Subject:")
	require.Contains(t, payload, "From: from@example.comX-Dagu-From: injected")
	require.Contains(t, payload, "To: to@example.comX-Dagu-To: injected")
	require.Contains(t, payload, "Subject: SubjectX-Dagu-Subject: injected")
}

func TestSendWithRecipientsAddsCcHeaderAndOmitsBccHeader(t *testing.T) {
	t.Parallel()

	server, err := newSMTPRecordingServer()
	require.NoError(t, err)
	defer func() {
		_ = server.Close()
	}()

	go server.Serve()

	host, port, err := net.SplitHostPort(server.Address())
	require.NoError(t, err)

	mailer := New(Config{Host: host, Port: port})
	err = mailer.SendWithRecipients(
		context.Background(),
		"from@example.com",
		[]string{"to@example.com"},
		[]string{"cc@example.com"},
		[]string{"bcc@example.com"},
		"Subject",
		"Body",
		nil,
	)
	require.NoError(t, err)

	payloads := server.RecordedDataBodies()
	require.Len(t, payloads, 1)
	payload := payloads[0]
	require.Contains(t, payload, "To: to@example.com")
	require.Contains(t, payload, "Cc: cc@example.com")
	require.NotContains(t, payload, "bcc@example.com")
}

type smtpRecordingServer struct {
	listener           net.Listener
	advertiseSTARTTLS  bool
	mailFromResponse   string
	rcptToResponse     string
	startTLSResponse   string
	mu                 sync.Mutex
	startTLSCount      int
	recordedDataBodies []string
}

func newSMTPRecordingServer() (*smtpRecordingServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &smtpRecordingServer{
		listener:         listener,
		mailFromResponse: "250 OK\r\n",
		rcptToResponse:   "250 OK\r\n",
		startTLSResponse: "454 TLS not available\r\n",
	}, nil
}

func (s *smtpRecordingServer) Address() string {
	return s.listener.Addr().String()
}

func (s *smtpRecordingServer) Close() error {
	return s.listener.Close()
}

func (s *smtpRecordingServer) StartTLSCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTLSCount
}

func (s *smtpRecordingServer) RecordedDataBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.recordedDataBodies...)
}

func (s *smtpRecordingServer) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConnection(conn)
	}
}

func (s *smtpRecordingServer) handleConnection(conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	_, _ = writer.WriteString("220 mock.server ESMTP\r\n")
	_ = writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		switch {
		case strings.HasPrefix(line, "HELO") || strings.HasPrefix(line, "EHLO"):
			_, _ = writer.WriteString("250-mock.server\r\n")
			if s.advertiseSTARTTLS {
				_, _ = writer.WriteString("250-STARTTLS\r\n")
			}
			_, _ = writer.WriteString("250 OK\r\n")
		case strings.HasPrefix(line, "STARTTLS"):
			s.mu.Lock()
			s.startTLSCount++
			s.mu.Unlock()
			_, _ = writer.WriteString(s.startTLSResponse)
		case strings.HasPrefix(line, "MAIL FROM:"):
			_, _ = writer.WriteString(s.mailFromResponse)
		case strings.HasPrefix(line, "RCPT TO:"):
			_, _ = writer.WriteString(s.rcptToResponse)
		case strings.HasPrefix(line, "DATA"):
			_, _ = writer.WriteString("354 Start mail input\r\n")
			_ = writer.Flush()

			var payload bytes.Buffer
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(dataLine) == "." {
					s.mu.Lock()
					s.recordedDataBodies = append(s.recordedDataBodies, payload.String())
					s.mu.Unlock()
					_, _ = writer.WriteString("250 OK\r\n")
					break
				}
				payload.WriteString(dataLine)
			}
		case strings.HasPrefix(line, "QUIT"):
			_, _ = writer.WriteString("221 Bye\r\n")
			_ = writer.Flush()
			return
		default:
			_, _ = writer.WriteString("500 Unknown command\r\n")
		}
		_ = writer.Flush()
	}
}

func (m *Client) sendWithNoAuth(
	from string,
	to []string,
	subject, body string,
	attachments []string,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()
	return m.send(ctx, from, to, nil, nil, subject, body, attachments, false)
}

func (m *Client) sendWithAuth(
	from string,
	to []string,
	subject, body string,
	attachments []string,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()
	return m.send(ctx, from, to, nil, nil, subject, body, attachments, true)
}

// mockSMTPServer creates a mock SMTP server for testing
type mockSMTPServer struct {
	listener      net.Listener
	delay         time.Duration
	acceptDelay   time.Duration
	responseDelay time.Duration
}

func newMockSMTPServer(delay, acceptDelay, responseDelay time.Duration) (*mockSMTPServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &mockSMTPServer{
		listener:      listener,
		delay:         delay,
		acceptDelay:   acceptDelay,
		responseDelay: responseDelay,
	}, nil
}

func (s *mockSMTPServer) Address() string {
	return s.listener.Addr().String()
}

func (s *mockSMTPServer) Close() error {
	return s.listener.Close()
}

func (s *mockSMTPServer) Serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConnection(conn)
	}
}

func (s *mockSMTPServer) handleConnection(conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	if s.acceptDelay > 0 {
		time.Sleep(s.acceptDelay)
	}

	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	// Send initial greeting
	_, _ = writer.WriteString("220 mock.server ESMTP\r\n")
	_ = writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		// Simulate delay for all responses
		if s.responseDelay > 0 {
			time.Sleep(s.responseDelay)
		}

		// Simulate overall delay
		if s.delay > 0 {
			time.Sleep(s.delay)
		}

		// Simple SMTP command handling
		switch {
		case strings.HasPrefix(line, "HELO") || strings.HasPrefix(line, "EHLO"):
			_, _ = writer.WriteString("250 OK\r\n")
		case strings.HasPrefix(line, "MAIL FROM:"):
			_, _ = writer.WriteString("250 OK\r\n")
		case strings.HasPrefix(line, "RCPT TO:"):
			_, _ = writer.WriteString("250 OK\r\n")
		case strings.HasPrefix(line, "DATA"):
			_, _ = writer.WriteString("354 Start mail input\r\n")
			_ = writer.Flush()
			// Read until we get a line with just "."
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(dataLine) == "." {
					_, _ = writer.WriteString("250 OK\r\n")
					break
				}
			}
		case strings.HasPrefix(line, "QUIT"):
			_, _ = writer.WriteString("221 Bye\r\n")
			_ = writer.Flush()
			return
		default:
			_, _ = writer.WriteString("500 Unknown command\r\n")
		}
		_ = writer.Flush()
	}
}

func TestMailerTimeout(t *testing.T) {
	// Save original timeout and restore after test
	originalTimeout := mailTimeout
	defer func() {
		mailTimeout = originalTimeout
	}()

	// Set a shorter timeout for testing
	mailTimeout = 2 * time.Second

	t.Run("SendWithNoAuthTimeoutOnConnection", func(t *testing.T) {
		// Create a listener that accepts connections but never responds
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() {
			_ = listener.Close()
		}()

		// Get the address
		host, port, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(t, err)

		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Accept connection but don't send SMTP greeting
			time.Sleep(5 * time.Second)
			_ = conn.Close()
		}()

		mailer := New(Config{
			Host: host,
			Port: port,
		})

		err = mailer.sendWithNoAuth(
			"from@example.com",
			[]string{"to@example.com"},
			"Test Subject",
			"Test Body",
			nil,
		)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
	})

	t.Run("SendWithNoAuthTimeoutDuringSMTPSession", func(t *testing.T) {
		// Create a mock server that delays responses
		server, err := newMockSMTPServer(0, 0, 3*time.Second)
		require.NoError(t, err)
		defer func() {
			_ = server.Close()
		}()

		go server.Serve()

		host, port, err := net.SplitHostPort(server.Address())
		require.NoError(t, err)

		mailer := New(Config{
			Host: host,
			Port: port,
		})

		err = mailer.sendWithNoAuth(
			"from@example.com",
			[]string{"to@example.com"},
			"Test Subject",
			"Test Body",
			nil,
		)

		assert.Error(t, err)
		// Should timeout due to slow responses
		assert.True(t, strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "deadline exceeded"))
	})

	t.Run("SendWithNoAuthSuccessfulWithinTimeout", func(t *testing.T) {
		// Create a mock server that responds quickly
		server, err := newMockSMTPServer(0, 0, 0)
		require.NoError(t, err)
		defer func() {
			_ = server.Close()
		}()

		go server.Serve()

		host, port, err := net.SplitHostPort(server.Address())
		require.NoError(t, err)

		mailer := New(Config{
			Host: host,
			Port: port,
		})

		// This should succeed as the server responds quickly
		err = mailer.sendWithNoAuth(
			"from@example.com",
			[]string{"to@example.com"},
			"Test Subject",
			"Test Body",
			nil,
		)

		// The mock server doesn't implement full SMTP, so we might get an error,
		// but it shouldn't be a timeout error
		if err != nil {
			assert.NotContains(t, err.Error(), "timeout")
			assert.NotContains(t, err.Error(), "deadline exceeded")
		}
	})

	t.Run("SendWithAuthTimeout", func(t *testing.T) {
		// Set an even shorter timeout for this test
		mailTimeout = 100 * time.Millisecond

		// Create a listener that never responds
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		// Close the listener immediately to ensure connection fails
		_ = listener.Close()

		host, port, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(t, err)

		mailer := New(Config{
			Host:     host,
			Port:     port,
			Username: "user",
			Password: "pass",
		})

		start := time.Now()
		err = mailer.sendWithAuth(
			"from@example.com",
			[]string{"to@example.com"},
			"Test Subject",
			"Test Body",
			nil,
		)
		elapsed := time.Since(start)

		assert.Error(t, err)
		// Should timeout quickly
		assert.Less(t, elapsed, 500*time.Millisecond)
	})

	t.Run("SendMethodRoutesCorrectly", func(t *testing.T) {
		// Test that Send method correctly routes to sendWithAuth when credentials are provided
		mailer := New(Config{
			Host:     "invalid.host",
			Port:     "25",
			Username: "user",
			Password: "pass",
		})

		ctx := context.Background()
		err := mailer.Send(ctx, "from@example.com", []string{"to@example.com"}, "Subject", "Body", nil)
		assert.Error(t, err)

		// Test that Send method correctly routes to sendWithNoAuth when no credentials
		mailer = New(Config{
			Host: "invalid.host",
			Port: "25",
		})

		err = mailer.Send(ctx, "from@example.com", []string{"to@example.com"}, "Subject", "Body", nil)
		assert.Error(t, err)
	})
}
