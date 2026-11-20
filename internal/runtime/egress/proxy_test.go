// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver answers from a table and counts the lookups.
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][]netip.Addr
	calls   []string
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, host)
	if addrs, ok := r.answers[host]; ok {
		return addrs, nil
	}
	return nil, errors.New("no such host")
}

type dialRecord struct {
	mu    sync.Mutex
	addrs []string
}

func (d *dialRecord) dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, address)
	d.mu.Unlock()
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func (d *dialRecord) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addrs...)
}

// startProxy serves a proxy with the rules on a local port and returns its
// address.
func startProxy(t *testing.T, rules []string, resolver egress.Resolver, dial egress.DialFunc) string {
	t.Helper()
	parsed, err := egress.ParseRules(rules)
	require.NoError(t, err)
	var logs []string
	var logMu sync.Mutex
	proxy, err := egress.New(egress.Options{Rules: parsed, Resolver: resolver, Dial: dial, Log: func(format string, args ...any) {
		logMu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		logMu.Unlock()
	}})
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- proxy.Serve(listener) }()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return listener.Addr().String()
}

func proxyClient(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse("http://" + proxyAddr)
	require.NoError(t, err)
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true},
		Timeout:   10 * time.Second,
	}
}

func originServer(t *testing.T, body string) (host string, port string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body+" "+r.Host+" "+r.Header.Get("Proxy-Authorization")+r.Header.Get("X-Forwarded-For"))
	}))
	t.Cleanup(server.Close)
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	return host, port
}

// A proxy with no rule is not valid: it would deny everything, and a policy with
// no destination uses no proxy.
func TestNewNeedsRules(t *testing.T) {
	t.Parallel()

	_, err := egress.New(egress.Options{})
	require.Error(t, err)
	empty, err := egress.ParseRules(nil)
	require.NoError(t, err)
	_, err = egress.New(egress.Options{Rules: empty})
	require.Error(t, err)
}

// A plain HTTP request to a listed destination goes through. The proxy resolves
// the name itself and dials the address that it resolved. One that is not listed
// gets 403, and nothing is dialed or resolved.
func TestPlainRequestToAListedDestination(t *testing.T) {
	t.Parallel()

	host, port := originServer(t, "origin-ok")
	resolver := &fakeResolver{answers: map[string][]netip.Addr{"allowed.example.test": {netip.MustParseAddr(host)}}}
	dials := &dialRecord{}
	proxyAddr := startProxy(t, []string{"allowed.example.test:" + port}, resolver, dials.dial)
	client := proxyClient(t, proxyAddr)

	resp, err := client.Get("http://allowed.example.test:" + port + "/x")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "origin-ok allowed.example.test:"+port+" ", string(body), "the client sends no proxy credential and the proxy adds no address")
	assert.Equal(t, []string{net.JoinHostPort(host, port)}, dials.seen())
	assert.Equal(t, []string{"allowed.example.test"}, resolver.calls)

	before := len(dials.seen())
	resp, err = client.Get("http://denied.example.test:" + port + "/x")
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Contains(t, string(body), "not in the egress policy")
	assert.Len(t, dials.seen(), before, "a denied destination is not dialed")
	assert.Equal(t, []string{"allowed.example.test"}, resolver.calls, "a denied name is not resolved")

	// The right host with another port is another destination.
	resp, err = client.Get("http://allowed.example.test:1/x")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// CONNECT carries any TCP stream to a listed destination: the bytes pass in both
// directions. A destination that is not listed gets 403 before the proxy answers
// 200, and nothing is dialed.
func TestConnectToAListedDestination(t *testing.T) {
	t.Parallel()

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
	host, port, _ := net.SplitHostPort(echo.Addr().String())
	dials := &dialRecord{}
	proxyAddr := startProxy(t, []string{host + ":" + port}, nil, dials.dial)

	connect := func(target string) (*bufio.Reader, net.Conn, string) {
		conn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		require.NoError(t, err)
		reader := bufio.NewReader(conn)
		status, err := reader.ReadString('\n')
		require.NoError(t, err)
		return reader, conn, strings.TrimSpace(status)
	}

	reader, conn, status := connect(host + ":" + port)
	assert.Equal(t, "HTTP/1.1 200 Connection Established", status)
	_, err = reader.ReadString('\n') // the empty line
	require.NoError(t, err)
	_, err = io.WriteString(conn, "ping\n")
	require.NoError(t, err)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "ping\n", line)

	_, _, status = connect("203.0.113.9:443")
	assert.Equal(t, "HTTP/1.1 403 Forbidden", status)
	_, _, status = connect(host + ":1")
	assert.Equal(t, "HTTP/1.1 403 Forbidden", status, "the port is part of the destination")
	assert.Equal(t, []string{net.JoinHostPort(host, port)}, dials.seen(), "only the listed destination was dialed")
}

// A tunnel in which no byte moves ends, so a client that opens tunnels and sends
// nothing cannot hold the descriptors of the proxy for ever. A tunnel in use
// stays.
func TestIdleTunnelEnds(t *testing.T) {
	t.Parallel()

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
	host, port, _ := net.SplitHostPort(echo.Addr().String())
	rules, err := egress.ParseRules([]string{host + ":" + port})
	require.NoError(t, err)
	proxy, err := egress.New(egress.Options{Rules: rules})
	require.NoError(t, err)
	proxy.SetTunnelIdleForTest(300 * time.Millisecond)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = proxy.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close() })

	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = fmt.Fprintf(conn, "CONNECT %s:%s HTTP/1.1\r\nHost: %s:%s\r\n\r\n", host, port, host, port)
	require.NoError(t, err)
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "HTTP/1.1 200 Connection Established", strings.TrimSpace(status))
	_, err = reader.ReadString('\n')
	require.NoError(t, err)
	// Traffic keeps the tunnel open past the idle time.
	for range 4 {
		_, err = io.WriteString(conn, "ping\n")
		require.NoError(t, err)
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "ping\n", line)
		time.Sleep(100 * time.Millisecond)
	}
	// Then nothing moves, and the proxy closes the tunnel.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = reader.ReadString('\n')
	require.ErrorIs(t, err, io.EOF)
}

// A wildcard rule covers the subdomains. The proxy checks the name that the client
// gave, and a name that resolves to a listed address is not listed.
func TestWildcardAndAddresses(t *testing.T) {
	t.Parallel()

	host, port := originServer(t, "wild")
	resolver := &fakeResolver{answers: map[string][]netip.Addr{
		"a.cdn.example.test":   {netip.MustParseAddr(host)},
		"cdn.example.test":     {netip.MustParseAddr(host)},
		"rebind.other.test":    {netip.MustParseAddr(host)},
		"two.cdn.example.test": {netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr(host)},
	}}
	dials := &dialRecord{}
	proxyAddr := startProxy(t, []string{"*.cdn.example.test:" + port, host + ":" + port}, resolver, dials.dial)
	client := proxyClient(t, proxyAddr)

	get := func(target string) int {
		resp, err := client.Get(target)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	// Anybody can point a name below a wildcard to an address of the network of the
	// proxy, such as the loopback of the origin server here. A name that only a
	// wildcard allows gets public addresses only, and nothing is dialed.
	assert.Equal(t, http.StatusForbidden, get("http://a.cdn.example.test:"+port+"/"))
	assert.Empty(t, dials.seen())
	assert.Equal(t, http.StatusForbidden, get("http://cdn.example.test:"+port+"/"), "the wildcard is not the name itself")
	assert.Equal(t, http.StatusForbidden, get("http://rebind.other.test:"+port+"/"), "a name that resolves to a listed address is not listed")
	assert.Equal(t, http.StatusOK, get("http://"+host+":"+port+"/"), "an address that the policy lists")
	// The first address does not answer: the proxy tries the next one that the name
	// gave. A rule that names the host reaches any address.
	failing := func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasPrefix(address, "192.0.2.1:") {
			return nil, errors.New("no route")
		}
		return dials.dial(ctx, network, address)
	}
	proxyAddr2 := startProxy(t, []string{"two.cdn.example.test:" + port}, resolver, failing)
	assert.Equal(t, http.StatusOK, func() int {
		resp, err := proxyClient(t, proxyAddr2).Get("http://two.cdn.example.test:" + port + "/")
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}())
}

// A request that is not an absolute URL, and one with another scheme, is refused. A
// failed upstream gives 502 and a name that does not resolve gives 502.
func TestMalformedAndFailedRequests(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{answers: map[string][]netip.Addr{}}
	proxyAddr := startProxy(t, []string{"gone.example.test"}, resolver, nil)

	raw := func(request string) string {
		conn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = io.WriteString(conn, request)
		require.NoError(t, err)
		status, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		return strings.TrimSpace(status)
	}
	assert.Equal(t, "HTTP/1.1 400 Bad Request", raw("GET / HTTP/1.1\r\nHost: gone.example.test\r\nConnection: close\r\n\r\n"))
	assert.Equal(t, "HTTP/1.1 400 Bad Request", raw("GET https://gone.example.test/ HTTP/1.1\r\nHost: gone.example.test\r\nConnection: close\r\n\r\n"))
	assert.Equal(t, "HTTP/1.1 400 Bad Request", raw("CONNECT nohost HTTP/1.1\r\nHost: nohost\r\nConnection: close\r\n\r\n"))
	assert.Equal(t, "HTTP/1.1 502 Bad Gateway", raw("GET http://gone.example.test/ HTTP/1.1\r\nHost: gone.example.test\r\nConnection: close\r\n\r\n"))
	assert.Equal(t, "HTTP/1.1 502 Bad Gateway", raw("CONNECT gone.example.test:443 HTTP/1.1\r\nHost: gone.example.test:443\r\nConnection: close\r\n\r\n"))
}
