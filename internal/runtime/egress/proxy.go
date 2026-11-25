// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Resolver looks up the addresses of a host name. *net.Resolver implements it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialFunc opens a connection to an address of the form ip:port.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Options configures a proxy.
type Options struct {
	// Rules is the allow list. A proxy with no rule is not valid, because it
	// would deny everything.
	Rules *Rules
	// Resolver resolves the names of the allowed destinations. The default is the
	// resolver of the process.
	Resolver Resolver
	// Dial opens the connection to the address that the proxy resolved. The
	// default is a TCP dial with a connect timeout.
	Dial DialFunc
	// Log receives one line for each decision. It can be nil.
	Log func(format string, args ...any)
	// MaxConnections bounds the client connections that the proxy holds at
	// once, tunnels included. The proxy closes a connection over the bound as
	// soon as it accepts it. Zero or less means DefaultMaxConnections.
	MaxConnections int
}

// Proxy is an HTTP proxy that forwards a connection to a listed destination
// only. It supports the method CONNECT, for TLS and for any TCP stream, and the
// forward of a plain HTTP request. A connection to another destination gets the
// status 403 and no connection leaves the proxy.
type Proxy struct {
	rules    *Rules
	resolver Resolver
	dial     DialFunc
	log      func(format string, args ...any)

	// tunnelIdle ends a tunnel in which no byte moved for this time.
	tunnelIdle time.Duration
	// keepAliveIdle ends a kept connection with no request for this time.
	keepAliveIdle time.Duration
	// maxConnections bounds the client connections that Serve holds at once.
	maxConnections int

	reverse *httputil.ReverseProxy
}

// connectTimeout limits the time to open one upstream connection.
const connectTimeout = 15 * time.Second

// DefaultMaxConnections is the bound of client connections when
// Options.MaxConnections is not set.
const DefaultMaxConnections = 512

// New makes a proxy.
func New(opts Options) (*Proxy, error) {
	if opts.Rules == nil || opts.Rules.Len() == 0 {
		return nil, errors.New("egress proxy: the allow list is empty; a policy with no destination uses no proxy")
	}
	p := &Proxy{
		rules: opts.Rules, resolver: opts.Resolver, dial: opts.Dial, log: opts.Log,
		tunnelIdle: tunnelIdleTimeout, keepAliveIdle: keepAliveIdleTimeout,
		maxConnections: opts.MaxConnections,
	}
	if p.maxConnections <= 0 {
		p.maxConnections = DefaultMaxConnections
	}
	if p.resolver == nil {
		p.resolver = net.DefaultResolver
	}
	if p.dial == nil {
		dialer := &net.Dialer{Timeout: connectTimeout}
		p.dial = dialer.DialContext
	}
	if p.log == nil {
		p.log = func(string, ...any) {}
	}
	transport := &http.Transport{
		// The dial goes through the same check as a CONNECT, so a redirect or a
		// second request on a kept connection cannot leave the policy.
		DialContext:           p.dialAllowed,
		Proxy:                 nil,
		DisableCompression:    true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   connectTimeout,
		ResponseHeaderTimeout: 5 * time.Minute,
	}
	p.reverse = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// The outgoing request keeps the absolute URL of the incoming one. The proxy
			// adds no header that names the client.
			r.Out.URL = r.In.URL
			r.Out.Host = r.In.URL.Host
			r.Out.Header.Del("Proxy-Authorization")
			r.Out.Header.Del("Proxy-Connection")
			declineUpgrade(r.Out.Header)
		},
		Transport: transport,
		// No forwarded request asks for an upgrade, so a 101 answer is an error.
		// Refusing it here also closes the connection that the answer hands
		// over, which the reverse proxy leaves open on its own refusal.
		ModifyResponse: func(res *http.Response) error {
			if res.StatusCode == http.StatusSwitchingProtocols {
				return errors.New("the destination switched protocols without an upgrade request")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.log("egress proxy: %s %s failed: %v", r.Method, r.URL.Host, err)
			if denied, ok := errors.AsType[*DeniedError](err); ok {
				http.Error(w, denied.Error(), http.StatusForbidden)
				return
			}
			http.Error(w, "egress proxy: the connection to the destination failed", http.StatusBadGateway)
		},
	}
	return p, nil
}

// declineUpgrade removes the protocol upgrade from an outgoing request, so the
// destination answers in HTTP/1.1. A switched connection would escape the idle
// limits of the proxy. The reverse proxy fails a 101 answer to a request without
// an upgrade. The hop-by-hop headers are gone by now, so Connection holds only
// the upgrade.
func declineUpgrade(header http.Header) {
	header.Del("Connection")
	header.Del("Upgrade")
}

// admit decides for a destination of a request in absolute form or of CONNECT.
// It writes the answer of a refused request and returns false.
func (p *Proxy) admit(w http.ResponseWriter, host string, port int) bool {
	if p.rules.Allows(host, port) {
		return true
	}
	p.deny(w, host, port)
	return false
}

// dialAllowed checks the destination and dials the address that the name
// resolves to. The address is the one that the proxy dials: it never resolves a
// second time.
func (p *Proxy) dialAllowed(ctx context.Context, network, address string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("the destination %q is not host:port: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("the destination %q has no valid port", address)
	}
	if !p.rules.Allows(host, port) {
		return nil, &DeniedError{Host: host, Port: port}
	}
	var addrs []netip.Addr
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		addrs = []netip.Addr{ip}
	} else {
		addrs, err = p.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
	}
	// Anybody can make a name below a wildcard point to an address of the network
	// of the proxy, and a wildcard such as *.0.0.1 matches address literals. A
	// destination that only a wildcard allows gets public addresses only. A rule
	// that names the host can reach any address.
	if !p.rules.AllowsExactly(host, port) {
		addrs = slices.DeleteFunc(addrs, func(addr netip.Addr) bool { return !publicAddr(addr) })
		if len(addrs) == 0 {
			return nil, &DeniedError{Host: host, Port: port}
		}
	}
	var lastErr error
	for _, addr := range addrs {
		conn, dialErr := p.dial(ctx, network, net.JoinHostPort(addr.String(), portText))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%s has no address", host)
	}
	return nil, lastErr
}

// specialPrefixes are the special-purpose ranges of the IANA registries that
// IsGlobalUnicast accepts and that are not addresses of the internet.
var specialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // this network
	netip.MustParsePrefix("100.64.0.0/10"),   // shared address space (carrier-grade NAT)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, and the broadcast address
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments, Teredo
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("3fff::/20"),       // documentation
	netip.MustParsePrefix("5f00::/16"),       // segment routing
}

var (
	// nat64Prefix is the well-known NAT64 prefix: the last 32 bits are an IPv4
	// address (RFC 6052).
	nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	// sixToFourPrefix is the 6to4 prefix: the 32 bits after it are an IPv4
	// address (RFC 3056).
	sixToFourPrefix = netip.MustParsePrefix("2002::/16")
	// globalUnicast6 is the only IPv6 range that IANA allocates for global
	// unicast. The rest is reserved, such as ::/96, which embeds an IPv4
	// address, and the site-local fec0::/10.
	globalUnicast6 = netip.MustParsePrefix("2000::/3")
)

// publicAddr reports whether addr is an address of the internet: not loopback,
// private, link-local, multicast, unspecified, special-purpose or reserved. A
// NAT64 or 6to4 address is public when the IPv4 address in it is. An address
// with a zone is not public: no prefix contains it, so it would pass the range
// checks.
func publicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.Zone() != "" || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, prefix := range specialPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	raw := addr.As16()
	switch {
	case nat64Prefix.Contains(addr):
		return publicAddr(netip.AddrFrom4([4]byte(raw[12:16])))
	case sixToFourPrefix.Contains(addr):
		return publicAddr(netip.AddrFrom4([4]byte(raw[2:6])))
	}
	return addr.Is4() || globalUnicast6.Contains(addr)
}

// DeniedError is the error of a destination that the policy does not list.
type DeniedError struct {
	Host string
	Port int
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("the destination %s:%d is not in the egress policy", e.Host, e.Port)
}

// ServeHTTP implements http.Handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.serveConnect(w, r)
		return
	}
	if !r.URL.IsAbs() || r.URL.Host == "" {
		http.Error(w, "egress proxy: send a request with an absolute URL, or use the method CONNECT", http.StatusBadRequest)
		return
	}
	if r.URL.Scheme != "http" {
		http.Error(w, "egress proxy: a plain request needs the scheme http; use CONNECT for TLS", http.StatusBadRequest)
		return
	}
	host, port := r.URL.Hostname(), 80
	if text := r.URL.Port(); text != "" {
		parsed, err := strconv.Atoi(text)
		if err != nil {
			http.Error(w, "egress proxy: the port is not a number", http.StatusBadRequest)
			return
		}
		port = parsed
	}
	if !p.admit(w, host, port) {
		return
	}
	p.log("egress proxy: allow %s %s:%d", r.Method, host, port)
	p.reverse.ServeHTTP(w, r)
}

func (p *Proxy) deny(w http.ResponseWriter, host string, port int) {
	p.log("egress proxy: deny %s:%d", host, port)
	http.Error(w, (&DeniedError{Host: host, Port: port}).Error(), http.StatusForbidden)
}

func (p *Proxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	host, portText, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "egress proxy: CONNECT needs host:port", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "egress proxy: the port is not valid", http.StatusBadRequest)
		return
	}
	if !p.admit(w, host, port) {
		return
	}
	upstream, err := p.dialAllowed(r.Context(), "tcp", net.JoinHostPort(host, portText))
	if err != nil {
		if _, ok := errors.AsType[*DeniedError](err); ok {
			p.deny(w, host, port)
			return
		}
		p.log("egress proxy: CONNECT %s:%d failed: %v", host, port, err)
		http.Error(w, "egress proxy: the connection to the destination failed", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "egress proxy: the server cannot hijack the connection", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	p.log("egress proxy: allow CONNECT %s:%d", host, port)
	_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
	pipe(client, buffered, upstream, p.tunnelIdle)
}

// tunnelIdleTimeout is the time after which a tunnel in which no byte moved in
// either direction ends. A client that opens tunnels and sends nothing cannot
// hold the descriptors of the proxy for ever.
const tunnelIdleTimeout = 10 * time.Minute

// keepAliveIdleTimeout ends a kept connection on which no next request came,
// for the same reason. It is longer than the idle time of common HTTP clients,
// so a client closes its idle connection first.
const keepAliveIdleTimeout = 2 * time.Minute

// pipe copies the bytes of both directions until each side has ended, and closes
// both connections. It closes both at once when no byte moved for idle.
func pipe(client net.Conn, buffered io.Reader, upstream net.Conn, idle time.Duration) {
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(idle / 4)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, last.Load())) >= idle {
					_ = client.Close()
					_ = upstream.Close()
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(activityWriter{upstream, &last}, buffered)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(activityWriter{client, &last}, upstream)
		closeWrite(client)
	}()
	wg.Wait()
	close(done)
	_ = client.Close()
	_ = upstream.Close()
}

// activityWriter notes the time of each write of a tunnel.
type activityWriter struct {
	w    io.Writer
	last *atomic.Int64
}

func (a activityWriter) Write(p []byte) (int, error) {
	a.last.Store(time.Now().UnixNano())
	return a.w.Write(p)
}

// closeWriter is a connection that can end its write side alone, as
// *net.TCPConn can.
type closeWriter interface {
	CloseWrite() error
}

func closeWrite(conn net.Conn) {
	if half, ok := conn.(closeWriter); ok {
		_ = half.CloseWrite()
	}
}

// Serve accepts connections on the listener until it closes. It holds at most
// the bound of Options.MaxConnections at once.
func (p *Proxy) Serve(l net.Listener) error {
	limited := &limitListener{Listener: l, slots: make(chan struct{}, p.maxConnections), log: p.log}
	// No WriteTimeout: it would cut a long stream of a model answer.
	server := &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: p.keepAliveIdle}
	err := server.Serve(limited)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// limitListener closes each connection that it accepts while all slots are
// taken. It closes rather than waits, so a closed listener always ends Accept.
type limitListener struct {
	net.Listener
	slots chan struct{}
	log   func(format string, args ...any)
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &slotConn{Conn: conn, release: sync.OnceFunc(func() { <-l.slots })}, nil
		default:
			l.log("egress proxy: refuse a connection from %s: %d connections are open", conn.RemoteAddr(), cap(l.slots))
			_ = conn.Close()
		}
	}
}

// slotConn frees its slot when it closes. A hijacked tunnel keeps the slot
// until pipe closes it.
type slotConn struct {
	net.Conn
	release func()
}

func (c *slotConn) Close() error {
	err := c.Conn.Close()
	c.release()
	return err
}

// CloseWrite keeps the half close of the connection, so a tunnel still passes
// the end of one direction.
func (c *slotConn) CloseWrite() error {
	if half, ok := c.Conn.(closeWriter); ok {
		return half.CloseWrite()
	}
	return nil
}
