// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mailoauth "github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestXOAUTH2Auth(t *testing.T) {
	t.Parallel()

	auth := &xoauth2Auth{username: "sender@example.com", token: "access-token"}
	mechanism, response, err := auth.Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: true})
	require.NoError(t, err)
	assert.Equal(t, "XOAUTH2", mechanism)
	assert.Equal(t, "user=sender@example.com\x01auth=Bearer access-token\x01\x01", string(response))

	response, err = auth.Next([]byte(`{"status":"401"}`), true)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Empty(t, response)
	assert.Equal(t, `{"status":"401"}`, auth.challenge)

	_, _, err = auth.Start(&smtp.ServerInfo{Name: "smtp.example.com", TLS: false})
	assert.ErrorContains(t, err, "requires a TLS connection")
}

func TestBuildConfigWithOAuth(t *testing.T) {
	t.Parallel()

	oauthConfig := &mailoauth.Config{
		Provider:     mailoauth.ProviderMicrosoft,
		TenantID:     "tenant",
		ClientID:     "client",
		ClientSecret: "secret",
	}
	config, err := BuildConfig("", "", "sender@example.com", "", oauthConfig)
	require.NoError(t, err)
	assert.Equal(t, "smtp.office365.com", config.Host)
	assert.Equal(t, "587", config.Port)
	assert.Equal(t, "sender@example.com", config.Username)
	assert.NotNil(t, config.Token)

	_, err = BuildConfig("smtp.example.com", "587", "sender@example.com", "", oauthConfig)
	assert.ErrorContains(t, err, "requires host")
	_, err = BuildConfig("smtp.office365.com", "25", "sender@example.com", "", oauthConfig)
	assert.ErrorContains(t, err, "requires port")
	_, err = BuildConfig("", "", "sender@example.com", "password", oauthConfig)
	assert.ErrorContains(t, err, "mutually exclusive")
	_, err = BuildConfig("", "", "", "", oauthConfig)
	assert.ErrorContains(t, err, "username is required")
	_, err = BuildConfig("", "", "sender@example.com", "", &mailoauth.Config{
		Provider: mailoauth.ProviderGoogleServiceAccount, ServiceAccountJSON: "{}",
	})
	assert.ErrorContains(t, err, "invalid Google service account JSON")
}

func TestAuthenticateOAuthPreservesServerChallenge(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	certificate := newTestTLSCertificate(t)
	serverDone := make(chan error, 1)
	authPayload := make(chan string, 1)
	challenge := `{"status":"401","schemes":"bearer"}`
	go func() {
		defer func() { _ = serverConn.Close() }()
		reader := bufio.NewReader(serverConn)
		writer := bufio.NewWriter(serverConn)
		write := func(response string) error {
			if _, err := writer.WriteString(response); err != nil {
				return err
			}
			return writer.Flush()
		}
		readPrefix := func(prefix string) (string, error) {
			line, err := reader.ReadString('\n')
			if err != nil {
				return "", err
			}
			if !strings.HasPrefix(line, prefix) {
				return "", fmt.Errorf("expected %q, got %q", prefix, line)
			}
			return line, nil
		}

		if err := write("220 mock.server ESMTP\r\n"); err != nil {
			serverDone <- err
			return
		}
		if _, err := readPrefix("EHLO"); err != nil {
			serverDone <- err
			return
		}
		if err := write("250-mock.server\r\n250-STARTTLS\r\n250 AUTH LOGIN\r\n"); err != nil {
			serverDone <- err
			return
		}
		if _, err := readPrefix("STARTTLS"); err != nil {
			serverDone <- err
			return
		}
		if err := write("220 Ready to start TLS\r\n"); err != nil {
			serverDone <- err
			return
		}

		tlsConn := tls.Server(serverConn, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		})
		if err := tlsConn.Handshake(); err != nil {
			serverDone <- err
			return
		}
		reader = bufio.NewReader(tlsConn)
		writer = bufio.NewWriter(tlsConn)
		if _, err := readPrefix("EHLO"); err != nil {
			serverDone <- err
			return
		}
		if err := write("250-mock.server\r\n250 AUTH XOAUTH2\r\n"); err != nil {
			serverDone <- err
			return
		}
		authLine, err := readPrefix("AUTH XOAUTH2 ")
		if err != nil {
			serverDone <- err
			return
		}
		parts := strings.Fields(authLine)
		if len(parts) != 3 {
			serverDone <- fmt.Errorf("unexpected AUTH command %q", authLine)
			return
		}
		payload, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			serverDone <- err
			return
		}
		authPayload <- string(payload)
		if err := write("334 " + base64.StdEncoding.EncodeToString([]byte(challenge)) + "\r\n"); err != nil {
			serverDone <- err
			return
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			serverDone <- err
			return
		}
		if strings.TrimSpace(line) != "" {
			serverDone <- fmt.Errorf("expected empty challenge response, got %q", line)
			return
		}
		if err := write("535 5.7.8 Authentication failed\r\n"); err != nil {
			serverDone <- err
			return
		}
		if _, err := readPrefix("*"); err != nil {
			serverDone <- err
			return
		}
		if err := write("501 Authentication canceled\r\n"); err != nil {
			serverDone <- err
			return
		}
		if _, err := readPrefix("QUIT"); err != nil {
			serverDone <- err
			return
		}
		serverDone <- write("221 Bye\r\n")
	}()

	client, err := smtp.NewClient(clientConn, "localhost")
	require.NoError(t, err)
	require.NoError(t, client.Hello("localhost"))
	require.NoError(t, client.StartTLS(&tls.Config{ //nolint:gosec // the test server uses an ephemeral self-signed certificate.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}))
	tokenCalls := 0
	mailer := &Client{
		host: "localhost", port: "587", username: "sender@example.com",
		token: func(context.Context) (*oauth2.Token, error) {
			tokenCalls++
			return &oauth2.Token{AccessToken: "access-token"}, nil
		},
	}
	err = mailer.authenticateOAuth(context.Background(), client)
	require.Error(t, err)
	assert.ErrorContains(t, err, "535")
	assert.ErrorContains(t, err, challenge)
	assert.Equal(t, 1, tokenCalls)
	assert.Equal(t, "user=sender@example.com\x01auth=Bearer access-token\x01\x01", <-authPayload)
	require.NoError(t, <-serverDone)
}

func TestAuthenticateOAuthRequiresAdvertisedXOAUTH2BeforeToken(t *testing.T) {
	t.Parallel()

	for _, response := range []string{
		"250 mock.server\r\n",
		"250-mock.server\r\n250 AUTH LOGIN PLAIN\r\n",
	} {
		clientConn, serverConn := net.Pipe()
		go func() {
			defer func() { _ = serverConn.Close() }()
			reader := bufio.NewReader(serverConn)
			writer := bufio.NewWriter(serverConn)
			_, _ = writer.WriteString("220 mock.server ESMTP\r\n")
			_ = writer.Flush()
			_, _ = reader.ReadString('\n')
			_, _ = writer.WriteString(response)
			_ = writer.Flush()
		}()

		client, err := smtp.NewClient(clientConn, "localhost")
		require.NoError(t, err)
		require.NoError(t, client.Hello("localhost"))
		tokenCalls := 0
		mailer := &Client{token: func(context.Context) (*oauth2.Token, error) {
			tokenCalls++
			return &oauth2.Token{AccessToken: "token"}, nil
		}}
		err = mailer.authenticateOAuth(context.Background(), client)
		assert.ErrorContains(t, err, "does not advertise AUTH XOAUTH2")
		assert.Zero(t, tokenCalls)
		_ = client.Close()
	}
}

func TestPrepareSessionRequiresSTARTTLSBeforeOAuthToken(t *testing.T) {
	t.Parallel()

	clientConn, serverConn := net.Pipe()
	go func() {
		defer func() { _ = serverConn.Close() }()
		reader := bufio.NewReader(serverConn)
		writer := bufio.NewWriter(serverConn)
		_, _ = writer.WriteString("220 mock.server ESMTP\r\n")
		_ = writer.Flush()
		_, _ = reader.ReadString('\n')
		_, _ = writer.WriteString("250-mock.server\r\n250 AUTH XOAUTH2\r\n")
		_ = writer.Flush()
	}()

	client, err := smtp.NewClient(clientConn, "localhost")
	require.NoError(t, err)
	tokenCalls := 0
	mailer := &Client{token: func(context.Context) (*oauth2.Token, error) {
		tokenCalls++
		return &oauth2.Token{AccessToken: "token"}, nil
	}}
	err = mailer.prepareSession(context.Background(), client)
	assert.ErrorContains(t, err, "requires STARTTLS")
	assert.Zero(t, tokenCalls)
	_ = client.Close()
}

func newTestTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	require.NoError(t, err)
	return certificate
}

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
	assert.ElementsMatch(t, []string{
		"to@example.com",
		"cc@example.com",
		"bcc@example.com",
	}, server.RecordedRecipients())
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
	recordedRecipients []string
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

func (s *smtpRecordingServer) RecordedRecipients() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.recordedRecipients...)
}

func smtpPathAddress(line string) string {
	address := strings.TrimSpace(strings.TrimPrefix(line, "RCPT TO:"))
	address = strings.Trim(address, "<>")
	return strings.TrimSpace(address)
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
			s.mu.Lock()
			s.recordedRecipients = append(s.recordedRecipients, smtpPathAddress(line))
			s.mu.Unlock()
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
