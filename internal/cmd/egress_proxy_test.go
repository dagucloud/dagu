// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bufio"
	"context"
	"errors"
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
	return rootOf(cmd.EgressProxy())
}

func rootOf(command *cobra.Command) *cobra.Command {
	root := &cobra.Command{Use: "root", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(command)
	return root
}

// servingRoot returns a root command whose serve takes over a listener on a
// local port, and the address of that port. The port stays bound from the
// start, so no other test can take it before serve runs.
func servingRoot(t *testing.T) (*cobra.Command, string) {
	t.Helper()
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	addr := held.Addr().String()
	listen := func(_, address string) (net.Listener, error) {
		if address != addr {
			return nil, fmt.Errorf("serve listens on %q, not on %q", address, addr)
		}
		return held, nil
	}
	var dialer net.Dialer
	return rootOf(cmd.EgressProxyForTest(listen, dialer.DialContext)), addr
}

// refusingRoot returns a root command whose wait finds no address that
// accepts connections.
func refusingRoot() *cobra.Command {
	refuse := func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	return rootOf(cmd.EgressProxyForTest(net.Listen, refuse))
}

// connect sends CONNECT for the target through the proxy, with the given header
// lines, and returns the status line of the answer. The connection stays open
// until the test ends.
func connect(t *testing.T, proxyAddr, target string, headers ...string) string {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	for _, header := range headers {
		request += header + "\r\n"
	}
	_, err = io.WriteString(conn, request+"\r\n")
	require.NoError(t, err)
	status, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	return strings.TrimSpace(status)
}

// runInBackground executes root with ctx in a goroutine and returns the channel
// that gets the result of the command.
func runInBackground(ctx context.Context, root *cobra.Command) chan error {
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	served := make(chan error, 1)
	go func() { served <- root.ExecuteContext(ctx) }()
	return served
}

// requireEnded fails unless the command ends without an error soon after its
// context ends.
func requireEnded(t *testing.T, served chan error) {
	t.Helper()
	select {
	case err := <-served:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the command does not end when its context ends")
	}
}

// startEcho serves a local port that sends back each byte and returns its
// address.
func startEcho(t *testing.T) string {
	t.Helper()
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
	return echo.Addr().String()
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
		{"no connection allowed", []string{"serve", "--listen", "127.0.0.1:1", "--allow=a.example.com", "--max-connections", "0"}, []string{"flag --max-connections", "above zero", "got 0"}},
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
// returns when the address accepts connections.
func TestEgressProxyCommandServesAndWaits(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	echo := startEcho(t)
	root, proxyAddr := servingRoot(t)
	root.SetArgs([]string{"egress-proxy", "serve", "--listen", proxyAddr, "--allow=" + echo})
	served := runInBackground(ctx, root)

	waitRoot := egressRoot()
	waitRoot.SetArgs([]string{"egress-proxy", "wait", "--addr", proxyAddr, "--timeout", "10s"})
	require.NoError(t, waitRoot.ExecuteContext(t.Context()))

	assert.Equal(t, "HTTP/1.1 200 Connection Established", connect(t, proxyAddr, echo))
	assert.Equal(t, "HTTP/1.1 403 Forbidden", connect(t, proxyAddr, "203.0.113.9:443"))

	cancel()
	requireEnded(t, served)
}

// wait fails after its timeout when no attempt connects, with the error of the
// last attempt.
func TestEgressProxyCommandWaitTimesOut(t *testing.T) {
	t.Parallel()

	start := time.Now()
	root := refusingRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"egress-proxy", "wait", "--addr", "proxy.test:8081", "--timeout", "300ms"})
	err := root.ExecuteContext(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proxy.test:8081 does not accept connections after 300ms")
	assert.Contains(t, err.Error(), "connection refused")
	assert.Less(t, time.Since(start), 5*time.Second)
}

// A wait whose context ends before its timeout returns the cause, and does not
// claim that the timeout passed.
func TestEgressProxyCommandWaitStopsWhenCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := refusingRoot()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"egress-proxy", "wait", "--addr", "proxy.test:8081", "--timeout", "1h"})
	err := root.ExecuteContext(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), "does not accept connections")
}

// --max-connections bounds the connections that serve holds: while one tunnel
// is open, a second connection is closed at once.
func TestEgressProxyCommandBoundsConnections(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	echo := startEcho(t)
	root, proxyAddr := servingRoot(t)
	root.SetArgs([]string{"egress-proxy", "serve", "--listen", proxyAddr, "--allow=" + echo, "--max-connections", "1"})
	served := runInBackground(ctx, root)

	require.Equal(t, "HTTP/1.1 200 Connection Established", connect(t, proxyAddr, echo))

	extra, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = extra.Close() })
	require.NoError(t, extra.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = extra.Read(make([]byte, 1))
	require.Error(t, err)
	if netErr, ok := errors.AsType[net.Error](err); ok {
		assert.False(t, netErr.Timeout(), "serve closes the connection over the bound")
	}

	cancel()
	requireEnded(t, served)
}
