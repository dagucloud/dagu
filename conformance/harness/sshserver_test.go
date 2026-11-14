// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// within fails the test unless fn returns before the timeout.
func within(t *testing.T, timeout time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s did not return within %s", what, timeout)
	}
}

// A client that leaves stdin open still gets the exit status once the
// command exits.
func TestSSHServerReturnsExitStatusWithStdinOpen(t *testing.T) {
	t.Parallel()

	server := StartSSHServer(t, SSHServerOptions{User: "tester", Password: "secret"})
	client, err := ssh.Dial("tcp", net.JoinHostPort("127.0.0.1", server.Port), &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.FixedHostKey(server.HostKey),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	session, err := client.NewSession()
	require.NoError(t, err)
	_, err = session.StdinPipe()
	require.NoError(t, err)

	var runErr error
	within(t, 10*time.Second, "the command", func() { runErr = session.Run("exit 3") })
	exitErr, ok := errors.AsType[*ssh.ExitError](runErr)
	require.True(t, ok, "want an exit status, got %v", runErr)
	require.Equal(t, 3, exitErr.ExitStatus())
	require.Equal(t, 1, server.AuthAttempts())
}

// Stopping the server closes a connection that never finished its
// handshake, instead of waiting for the client.
func TestSSHServerCloseDoesNotWaitForIdleConnections(t *testing.T) {
	t.Parallel()

	server := StartSSHServer(t, SSHServerOptions{User: "tester", Password: "secret"})
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", server.Port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.Eventually(t, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.conns) == 1
	}, 5*time.Second, 10*time.Millisecond)

	within(t, 10*time.Second, "closing the server", server.close)
}
