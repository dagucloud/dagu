// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func egressRoot() *cobra.Command {
	root := &cobra.Command{Use: "root", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(cmd.EgressProxy())
	return root
}

// freePort returns a port that is free now.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// The command of the helper has a closed configuration. An unknown flag, a missing
// flag and a value that is not valid stop it before it serves. The error names the
// flag, the expected form and the value.
func TestEgressProxyCommandRejectsABadConfiguration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"unknown flag", []string{"serve", "--listen", "127.0.0.1:1", "--allow=a.example.com", "--bogus"}, []string{"unknown flag", "--bogus"}},
		{"no listen", []string{"serve", "--allow=a.example.com"}, []string{"flag --listen is required"}},
		{"listen without a port", []string{"serve", "--listen", "127.0.0.1", "--allow=a.example.com"}, []string{"flag --listen", "host:port", `"127.0.0.1"`}},
		{"listen port out of range", []string{"serve", "--listen", "127.0.0.1:70000", "--allow=a.example.com"}, []string{"flag --listen", "from 1 to 65535", `"127.0.0.1:70000"`}},
		{"no allow", []string{"serve", "--listen", "127.0.0.1:1"}, []string{"allow list is empty"}},
		{"bad allow", []string{"serve", "--listen", "127.0.0.1:1", "--allow=https://a.example.com"}, []string{"flag --allow", `"https://a.example.com"`}},
		{"wait without addr", []string{"wait"}, []string{"flag --addr is required"}},
		{"wait bad timeout", []string{"wait", "--addr", "127.0.0.1:1", "--timeout", "0s"}, []string{"flag --timeout", "above zero"}},
		{"unknown command", []string{"serve-all"}, []string{"unknown command"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := egressRoot()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append([]string{"egress-proxy"}, tc.args...))
			err := root.ExecuteContext(t.Context())
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The command is hidden: it is for the helper container, not for the author of a DAG.
func TestEgressProxyCommandIsHidden(t *testing.T) {
	t.Parallel()

	assert.True(t, cmd.EgressProxy().Hidden)
}

// serve forwards a CONNECT to a listed destination and refuses another. wait
// returns when the address accepts connections, and fails after the timeout
// when it does not.
func TestEgressProxyCommandServesAndWaits(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	run := func(args ...string) chan error {
		root := egressRoot()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"egress-proxy"}, args...))
		done := make(chan error, 1)
		go func() { done <- root.ExecuteContext(ctx) }()
		return done
	}
	waitFor := func(addr string) {
		t.Helper()
		root := egressRoot()
		root.SetArgs([]string{"egress-proxy", "wait", "--addr", addr, "--timeout", "10s"})
		require.NoError(t, root.ExecuteContext(t.Context()))
	}

	proxyAddr := freePort(t)
	served := run("serve", "--listen", proxyAddr, "--allow="+echo.Addr().String())
	waitFor(proxyAddr)

	connect := func(target string) string {
		conn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		require.NoError(t, err)
		status, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		return strings.TrimSpace(status)
	}
	assert.Equal(t, "HTTP/1.1 200 Connection Established", connect(echo.Addr().String()))
	assert.Equal(t, "HTTP/1.1 403 Forbidden", connect("203.0.113.9:443"))

	// A wait for an address that nobody serves fails after the timeout.
	start := time.Now()
	root := egressRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"egress-proxy", "wait", "--addr", freePort(t), "--timeout", "300ms"})
	err = root.ExecuteContext(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not accept connections")
	assert.Less(t, time.Since(start), 5*time.Second)

	cancel()
	for _, done := range []chan error{served} {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the command does not end when its context ends")
		}
	}
}
