// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/test/mailtest"
)

// startSilentLogoutServer accepts one IMAP session that answers every
// command except LOGOUT, which it leaves waiting forever.
func startSilentLogoutServer(t *testing.T) (host, port string) {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", mailtest.ServerTLSConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("* OK [CAPABILITY IMAP4rev1] ready\r\n"))
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			tag, command := fields[0], strings.ToUpper(fields[1])
			if command == "LOGOUT" {
				continue
			}
			if command == "CAPABILITY" {
				_, _ = conn.Write([]byte("* CAPABILITY IMAP4rev1\r\n"))
			}
			_, _ = conn.Write([]byte(tag + " OK done\r\n"))
		}
	}()
	host, port, err = net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return host, port
}

func TestCloseDoesNotWaitForeverForLogout(t *testing.T) {
	t.Parallel()

	host, port := startSilentLogoutServer(t)
	client, err := Dial(context.Background(), Account{
		Server:   Server{Host: host, Port: port, Security: SecurityTLS, SkipTLSVerify: true},
		Username: "user",
		Password: "password",
	})
	require.NoError(t, err)
	client.logoutTimeout = 100 * time.Millisecond

	closed := make(chan struct{})
	go func() {
		_ = client.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for a LOGOUT reply that never came")
	}
}
