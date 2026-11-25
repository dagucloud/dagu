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
	"testing/synctest"
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

// noRoute records the address and fails the dial, as for an address that no
// route reaches.
func (d *dialRecord) noRoute(_ context.Context, _, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, address)
	d.mu.Unlock()
	return nil, errors.New("no route")
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

// pipeListener is a net.Listener of in-memory connections. Their reads and writes
// block without I/O, so a synctest bubble can move its clock past them.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// dial opens a connection to the listener.
func (l *pipeListener) dial() net.Conn {
	client, server := net.Pipe()
	l.conns <- server
	return client
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// A tunnel in which no byte moves ends, so a client that opens tunnels and sends
// nothing cannot hold the descriptors of the proxy for ever. A tunnel in use
// stays. The test runs in a synctest bubble on in-memory connections: the clock
// moves only when every goroutine waits, so a slow scheduler cannot make the
// tunnel in use look idle.
func TestIdleTunnelEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const idle = 300 * time.Millisecond
		const destination = "192.0.2.10:7"
		rules, err := egress.ParseRules([]string{destination})
		require.NoError(t, err)
		echo := func(context.Context, string, string) (net.Conn, error) {
			conn, peer := net.Pipe()
			go func() {
				defer func() { _ = peer.Close() }()
				_, _ = io.Copy(peer, peer)
			}()
			return conn, nil
		}
		proxy, err := egress.New(egress.Options{Rules: rules, Dial: echo})
		require.NoError(t, err)
		proxy.SetTunnelIdleForTest(idle)
		listener := newPipeListener()
		go func() { _ = proxy.Serve(listener) }()
		t.Cleanup(func() { _ = listener.Close() })

		conn := listener.dial()
		t.Cleanup(func() { _ = conn.Close() })
		_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", destination, destination)
		require.NoError(t, err)
		reader := bufio.NewReader(conn)
		status, err := reader.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "HTTP/1.1 200 Connection Established", strings.TrimSpace(status))
		_, err = reader.ReadString('\n')
		require.NoError(t, err)
		// Traffic every half idle time keeps the tunnel open for twice the idle time.
		for range 4 {
			time.Sleep(idle / 2)
			_, err = io.WriteString(conn, "ping\n")
			require.NoError(t, err)
			line, err := reader.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "ping\n", line)
		}
		// Then nothing moves, and the proxy closes the tunnel after the idle time.
		quiet := time.Now()
		require.NoError(t, conn.SetReadDeadline(quiet.Add(10*idle)))
		_, err = reader.ReadString('\n')
		require.ErrorIs(t, err, io.EOF)
		assert.GreaterOrEqual(t, time.Since(quiet), idle)
	})
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

// A name that only a wildcard allows reaches public addresses only. The
// special-purpose ranges are not public, and a NAT64 or 6to4 address is public
// only when the IPv4 address in it is. An IPv6 address outside 2000::/3 is not
// public, such as the IPv4-compatible and IPv4-translated forms of 127.0.0.1
// and 10.0.0.1, or a site-local address.
func TestWildcardRefusesSpecialAddresses(t *testing.T) {
	t.Parallel()

	refused := []string{
		"0.1.2.3", "192.0.0.8", "198.18.0.1", "203.0.113.5", "240.0.0.1", "255.255.255.255",
		"64:ff9b::a9fe:a9fe", "64:ff9b::a00:1", "64:ff9b:1::1", "2002:a9fe:a9fe::1",
		"2001::1", "2001:db8::1", "100::1",
		"::7f00:1", "::ffff:0:a00:1", "fec0::1",
	}
	allowed := []string{"8.8.8.8", "64:ff9b::808:808", "2002:808:808::1", "2606:4700::1111"}
	resolver := &fakeResolver{answers: map[string][]netip.Addr{}}
	name := func(addr string) string {
		return strings.NewReplacer(".", "-", ":", "-").Replace(addr) + ".wild.example.test"
	}
	for _, addr := range append(append([]string{}, refused...), allowed...) {
		resolver.answers[name(addr)] = []netip.Addr{netip.MustParseAddr(addr)}
	}
	dials := &dialRecord{}
	client := proxyClient(t, startProxy(t, []string{"*.wild.example.test"}, resolver, dials.noRoute))
	get := func(target string) int {
		resp, err := client.Get(target)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	for _, addr := range refused {
		assert.Equal(t, http.StatusForbidden, get("http://"+name(addr)+"/"), addr)
	}
	var want []string
	for _, addr := range allowed {
		assert.Equal(t, http.StatusBadGateway, get("http://"+name(addr)+"/"), "%s is dialed", addr)
		want = append(want, net.JoinHostPort(addr, "80"))
	}
	assert.Equal(t, want, dials.seen(), "only the public addresses are dialed")
}

// A wildcard whose text ends like an address, such as *.0.0.1, matches IP
// literals. An address that only such a rule allows reaches the network of the
// proxy no more than a name does: it must be public.
func TestWildcardRefusesPrivateAddressLiterals(t *testing.T) {
	t.Parallel()

	dials := &dialRecord{}
	resolver := &fakeResolver{answers: map[string][]netip.Addr{}}
	client := proxyClient(t, startProxy(t, []string{"*.0.0.1", "*.8.8"}, resolver, dials.noRoute))
	get := func(target string) int {
		resp, err := client.Get(target)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusForbidden, get("http://127.0.0.1/"))
	assert.Equal(t, http.StatusForbidden, get("http://10.0.0.1/"))
	assert.Equal(t, http.StatusBadGateway, get("http://8.8.8.8/"), "a public address is dialed")
	assert.Equal(t, []string{"8.8.8.8:80"}, dials.seen(), "only the public address is dialed")
}

// A wildcard also matches an IPv6 literal whose zone ends with its name. A zone
// hides no address from the check: such a literal is refused, and nothing is
// dialed. Without the check, the NAT64 form of 10.0.0.1 would be dialed.
func TestWildcardRefusesZonedAddressLiterals(t *testing.T) {
	t.Parallel()

	dials := &dialRecord{}
	resolver := &fakeResolver{answers: map[string][]netip.Addr{}}
	proxyAddr := startProxy(t, []string{"*.wild.example.test"}, resolver, dials.noRoute)
	status := func(request string) string {
		conn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = io.WriteString(conn, request)
		require.NoError(t, err)
		line, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		return strings.TrimSpace(line)
	}

	for _, target := range []string{"64:ff9b::a00:1%25x.wild.example.test", "2001:db8::1%25x.wild.example.test"} {
		assert.Equal(t, "HTTP/1.1 403 Forbidden",
			status("GET http://["+target+"]/ HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"), target)
		assert.Equal(t, "HTTP/1.1 403 Forbidden",
			status("CONNECT ["+target+"]:443 HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"), "CONNECT %s", target)
	}
	assert.Empty(t, dials.seen(), "a zoned literal is not dialed")
}

// A kept connection on which no next request comes ends, so a client cannot
// hold the descriptors of the proxy with idle connections.
func TestIdleKeepAliveEnds(t *testing.T) {
	t.Parallel()

	rules, err := egress.ParseRules([]string{"example.test"})
	require.NoError(t, err)
	proxy, err := egress.New(egress.Options{Rules: rules})
	require.NoError(t, err)
	proxy.SetKeepAliveIdleForTest(300 * time.Millisecond)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = proxy.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close() })

	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// A refused request of HTTP/1.1 keeps the connection.
	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.test\r\n\r\n")
	require.NoError(t, err)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = reader.ReadByte()
	require.ErrorIs(t, err, io.EOF, "the proxy closes the idle connection")
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

// A switched protocol would hold a raw connection that no idle limit covers, and
// no plain request needs one. The proxy sends the request without the upgrade,
// and a destination that switches protocols anyway gives 502 and loses its
// connection, which the proxy would otherwise keep open.
func TestProtocolUpgradeIsNotForwarded(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		always bool
		want   int
	}{
		{name: "switches when asked", want: http.StatusOK},
		{name: "always switches", always: true, want: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			upgrades := make(chan string, 1)
			released := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(released)
				upgrades <- r.Header.Get("Upgrade")
				if !tc.always && r.Header.Get("Upgrade") == "" {
					_, _ = io.WriteString(w, "ok")
					return
				}
				conn, buffered, err := http.NewResponseController(w).Hijack()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
				_, _ = io.Copy(io.Discard, buffered)
			}))
			t.Cleanup(server.Close)
			host, port, err := net.SplitHostPort(server.Listener.Addr().String())
			require.NoError(t, err)
			destination := "allowed.example.test:" + port
			resolver := &fakeResolver{answers: map[string][]netip.Addr{"allowed.example.test": {netip.MustParseAddr(host)}}}
			proxyAddr := startProxy(t, []string{destination}, resolver, nil)

			conn, err := net.Dial("tcp", proxyAddr)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			_, err = fmt.Fprintf(conn, "GET http://%s/ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", destination, destination)
			require.NoError(t, err)
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			require.NoError(t, err)
			_ = response.Body.Close()
			assert.Equal(t, tc.want, response.StatusCode)
			assert.Empty(t, <-upgrades, "the destination gets no upgrade")
			select {
			case <-released:
			case <-time.After(5 * time.Second):
				t.Fatal("the proxy keeps the connection of the destination open")
			}
		})
	}
}
