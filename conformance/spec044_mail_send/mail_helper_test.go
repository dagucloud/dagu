// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec044_mail_send_test

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeSMTPServer is a minimal, self-contained SMTP server implementing just
// enough of the protocol (HELO/EHLO, optional AUTH LOGIN, MAIL FROM, RCPT
// TO, DATA, QUIT) to exercise dagu's real SMTP client end to end, without
// depending on a real mail service or a third-party test library. Modeled
// on internal/cmn/mailer/mailer_test.go's smtpRecordingServer, which proves
// the same client library against the same protocol subset at the unit
// level.
type fakeSMTPServer struct {
	listener      net.Listener
	advertiseAuth bool

	mu                 sync.Mutex
	recordedFrom       []string
	recordedRecipients []string
	recordedDataBodies []string
	recordedAuthUser   string
	recordedAuthPass   string
}

// newFakeSMTPServer starts listening on a Docker-free, OS-assigned local
// port and registers cleanup. advertiseAuth controls whether HELO/EHLO
// advertises "AUTH LOGIN", which is what makes dagu's mailer client
// authenticate at all (see useAuth in internal/cmn/mailer/mailer.go).
func newFakeSMTPServer(t *testing.T, advertiseAuth bool) *fakeSMTPServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("starting fake SMTP server: %v", err)
	}
	s := &fakeSMTPServer{listener: listener, advertiseAuth: advertiseAuth}
	go s.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return s
}

// Addr returns "host:port" for the DAG-level smtp.host/smtp.port fields.
func (s *fakeSMTPServer) Addr() string {
	return s.listener.Addr().String()
}

func (s *fakeSMTPServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConnection(conn)
	}
}

func (s *fakeSMTPServer) handleConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	_, _ = writer.WriteString("220 fake.smtp ESMTP\r\n")
	_ = writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		switch {
		case strings.HasPrefix(line, "HELO") || strings.HasPrefix(line, "EHLO"):
			_, _ = writer.WriteString("250-fake.smtp\r\n")
			if s.advertiseAuth {
				_, _ = writer.WriteString("250-AUTH LOGIN PLAIN\r\n")
			}
			_, _ = writer.WriteString("250 OK\r\n")
		case strings.HasPrefix(line, "AUTH LOGIN"):
			if !s.handleAuthLogin(reader, writer) {
				return
			}
		case strings.HasPrefix(line, "MAIL FROM:"):
			s.mu.Lock()
			s.recordedFrom = append(s.recordedFrom, smtpPathAddress(line, "MAIL FROM:"))
			s.mu.Unlock()
			_, _ = writer.WriteString("250 OK\r\n")
		case strings.HasPrefix(line, "RCPT TO:"):
			s.mu.Lock()
			s.recordedRecipients = append(s.recordedRecipients, smtpPathAddress(line, "RCPT TO:"))
			s.mu.Unlock()
			_, _ = writer.WriteString("250 OK\r\n")
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

// handleAuthLogin drives the LOGIN challenge/response exchange dagu's
// client speaks (see loginAuth in internal/cmn/mailer/mailer.go): a
// base64 "Username:" challenge, then a base64 "Password:" challenge, each
// answered with a base64-encoded line.
func (s *fakeSMTPServer) handleAuthLogin(reader *bufio.Reader, writer *bufio.Writer) bool {
	_, _ = writer.WriteString("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")) + "\r\n")
	_ = writer.Flush()
	userLine, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	user, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(userLine))
	if decodeErr != nil {
		return false
	}

	_, _ = writer.WriteString("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")) + "\r\n")
	_ = writer.Flush()
	passLine, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	pass, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(passLine))
	if decodeErr != nil {
		return false
	}

	s.mu.Lock()
	s.recordedAuthUser = string(user)
	s.recordedAuthPass = string(pass)
	s.mu.Unlock()

	_, _ = writer.WriteString("235 Authentication successful\r\n")
	return true
}

func smtpPathAddress(line, prefix string) string {
	address := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	return strings.Trim(address, "<>")
}

func (s *fakeSMTPServer) Recipients() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.recordedRecipients...)
}

func (s *fakeSMTPServer) From() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.recordedFrom...)
}

func (s *fakeSMTPServer) DataBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.recordedDataBodies...)
}

func (s *fakeSMTPServer) AuthCredentials() (user, pass string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordedAuthUser, s.recordedAuthPass
}
