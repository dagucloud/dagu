// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoServer accepts connections and answers each with "hello" once the
// client wrote a byte, as a TLS server speaks only after its client.
func echoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				if _, err := conn.Read(make([]byte, 1)); err != nil {
					return
				}
				_, _ = conn.Write([]byte("hello"))
				// Closing with input still unread resets the connection, and a
				// relay that sees the reset can drop the greeting. End only the
				// write side and read until the peer closes.
				_ = conn.(*net.TCPConn).CloseWrite()
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

// resettingProxy returns the address of a proxy that reads a request and
// resets the connection without answering.
func resettingProxy(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Read(make([]byte, 1024))
			_ = conn.(*net.TCPConn).SetLinger(0)
			_ = conn.Close()
		}
	}()
	return listener.Addr().String()
}

func readGreeting(t *testing.T, conn net.Conn) string {
	t.Helper()
	defer func() { _ = conn.Close() }()
	_, err := conn.Write([]byte("?"))
	require.NoError(t, err)
	greeting, err := io.ReadAll(conn)
	require.NoError(t, err)
	return string(greeting)
}

// noCertificate configures TLS to a proxy on the way without a client
// certificate.
func noCertificate(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func TestDialRoutes(t *testing.T) {
	t.Parallel()

	target := echoServer(t)
	refusing := strings.TrimPrefix(closedURL, "http://")

	t.Run("FailsOverInOrder", func(t *testing.T) {
		t.Parallel()
		tunnel := newTunnelProxy(t, target, http.StatusOK)
		routes := []route{
			{kind: routeProxy, addr: refusing},
			{kind: routeProxy, addr: resettingProxy(t)},
			{kind: routeProxy, addr: tunnel.addr},
			{kind: routeDirect},
		}
		conn, via, err := dialRoutes(context.Background(), routes, target, noCertificate)
		require.NoError(t, err)
		assert.Equal(t, routes[2], via)
		assert.Equal(t, "hello", readGreeting(t, conn))
		assert.Equal(t, []string{target}, tunnel.requests())
	})

	// A proxy that answers the CONNECT request is not bypassed through a
	// later route, as Chrome does not.
	t.Run("StopsAtProxyAnswer", func(t *testing.T) {
		t.Parallel()
		for _, status := range []int{http.StatusForbidden, http.StatusProxyAuthRequired} {
			answering := newTunnelProxy(t, target, status)
			later := newTunnelProxy(t, target, http.StatusOK)
			_, _, err := dialRoutes(context.Background(), []route{
				{kind: routeProxy, addr: answering.addr},
				{kind: routeProxy, addr: later.addr},
				{kind: routeDirect},
			}, target, noCertificate)
			require.Error(t, err, status)
			assert.Empty(t, later.requests(), status)
		}
	})

	t.Run("TunnelsThroughProxy", func(t *testing.T) {
		t.Parallel()
		tunnel := newTunnelProxy(t, target, http.StatusOK)
		conn, via, err := dialRoutes(context.Background(), []route{{kind: routeProxy, addr: tunnel.addr}}, "portal.test:443", noCertificate)
		require.NoError(t, err)
		assert.Equal(t, routeProxy, via.kind)
		assert.Equal(t, "hello", readGreeting(t, conn))
		assert.Equal(t, []string{"portal.test:443"}, tunnel.requests())
	})

	t.Run("TunnelsThroughSOCKS5", func(t *testing.T) {
		t.Parallel()
		socks := newSOCKSProxy(t, target)
		conn, _, err := dialRoutes(context.Background(), []route{{kind: routeSOCKS5, addr: socks.addr}}, "portal.test:443", noCertificate)
		require.NoError(t, err)
		assert.Equal(t, "hello", readGreeting(t, conn))
		assert.Equal(t, []string{"portal.test:443"}, socks.requests())
	})

	t.Run("TunnelsThroughSOCKS4", func(t *testing.T) {
		t.Parallel()
		socks := newSOCKSProxy(t, target)
		conn, _, err := dialRoutes(context.Background(), []route{{kind: routeSOCKS4, addr: socks.addr}}, target, noCertificate)
		require.NoError(t, err)
		assert.Equal(t, "hello", readGreeting(t, conn))
		assert.Equal(t, []string{target}, socks.requests())
	})

	t.Run("ReportsEveryRoute", func(t *testing.T) {
		t.Parallel()
		authProxy := newTunnelProxy(t, target, http.StatusProxyAuthRequired)
		_, _, err := dialRoutes(context.Background(), []route{
			{kind: routeDirect},
			{kind: routeProxy, addr: authProxy.addr},
		}, refusing, noCertificate)
		require.Error(t, err)
		assert.ErrorIs(t, err, errProxyAuth)
		message := oneLine(err)
		directAt, proxyAt := strings.Index(message, "through DIRECT"), strings.Index(message, "through PROXY ")
		assert.True(t, directAt >= 0 && proxyAt > directAt, message)
	})

	t.Run("NoRoute", func(t *testing.T) {
		t.Parallel()
		_, _, err := dialRoutes(context.Background(), nil, target, noCertificate)
		assert.Error(t, err)
	})
}
