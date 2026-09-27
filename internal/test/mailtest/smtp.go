// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailtest

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
)

// SMTP is an in-process SMTP server over implicit TLS that accepts AUTH
// LOGIN and AUTH PLAIN for one user and records delivered messages.
type SMTP struct {
	Host     string
	Port     string
	Username string
	Password string

	mu       sync.Mutex
	messages []Delivery
}

// Delivery is one message the server accepted.
type Delivery struct {
	From string
	To   []string
	Data string
}

// StartSMTP starts a server that stops when the test ends.
func StartSMTP(t testing.TB) *SMTP {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", ServerTLSConfig(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	s := &SMTP{Username: "user@example.com", Password: "password"}
	s.Host, s.Port = splitAddr(t, listener.Addr())
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

// Deliveries returns the messages accepted so far.
func (s *SMTP) Deliveries() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Delivery(nil), s.messages...)
}

func (s *SMTP) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	reply := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}
	readLine := func() (string, bool) {
		line, err := reader.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err == nil
	}
	decode := func(value string) string {
		data, _ := base64.StdEncoding.DecodeString(value)
		return string(data)
	}

	reply("220 mailtest ESMTP")
	authenticated := false
	var current Delivery
	for {
		line, ok := readLine()
		if !ok {
			return
		}
		command := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			reply("250-mailtest")
			reply("250 AUTH LOGIN PLAIN")
		case strings.HasPrefix(command, "AUTH LOGIN"):
			reply("334 VXNlcm5hbWU6")
			user, _ := readLine()
			reply("334 UGFzc3dvcmQ6")
			pass, _ := readLine()
			authenticated = decode(user) == s.Username && decode(pass) == s.Password
		case strings.HasPrefix(command, "AUTH PLAIN "):
			parts := strings.Split(decode(strings.TrimSpace(line[len("AUTH PLAIN "):])), "\x00")
			authenticated = len(parts) == 3 && parts[1] == s.Username && parts[2] == s.Password
		case strings.HasPrefix(command, "MAIL FROM:"):
			if !authenticated {
				reply("530 Authentication required")
				continue
			}
			current = Delivery{From: strings.Trim(line[len("MAIL FROM:"):], "<> ")}
			reply("250 OK")
			continue
		case strings.HasPrefix(command, "RCPT TO:"):
			current.To = append(current.To, strings.Trim(line[len("RCPT TO:"):], "<> "))
			reply("250 OK")
			continue
		case command == "DATA":
			reply("354 End data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				dataLine, ok := readLine()
				if !ok {
					return
				}
				if dataLine == "." {
					break
				}
				data.WriteString(dataLine + "\n")
			}
			current.Data = data.String()
			s.mu.Lock()
			s.messages = append(s.messages, current)
			s.mu.Unlock()
			reply("250 OK")
			continue
		case command == "QUIT":
			reply("221 Bye")
			return
		default:
			reply("250 OK")
			continue
		}
		if strings.HasPrefix(command, "AUTH") {
			if authenticated {
				reply("235 Authentication successful")
			} else {
				reply("535 Authentication failed")
			}
		}
	}
}
