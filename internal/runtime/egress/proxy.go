// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm/anthropicbeta"
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
	// Upstream is the route of a request in origin form. It can be nil: the proxy
	// then refuses such a request. The host and the port of the upstream MUST
	// match a rule of Rules or of CredentialRules.
	Upstream *Upstream
	// CredentialRules are the destinations that need the proxy credential. A
	// client that sends it in the header Proxy-Authorization (Basic, the password
	// part) reaches them. Other clients get the status 407. A rule of this list
	// MUST NOT overlap a rule of Rules. It can be nil.
	CredentialRules *Rules
	// ProxyCredential is the value that CredentialRules need. It is set exactly
	// when CredentialRules has a rule.
	ProxyCredential string
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
// status 403 and no connection leaves the proxy. With an upstream, it also sends
// a request in origin form to the upstream, with the key of the upstream.
type Proxy struct {
	rules    *Rules
	resolver Resolver
	dial     DialFunc
	log      func(format string, args ...any)
	upstream *Upstream

	credentialRules *Rules
	credential      string

	// tunnelIdle ends a tunnel in which no byte moved for this time.
	tunnelIdle time.Duration
	// keepAliveIdle ends a kept connection with no request for this time.
	keepAliveIdle time.Duration
	// maxConnections bounds the client connections that Serve holds at once.
	maxConnections int

	transport *http.Transport
	reverse   *httputil.ReverseProxy
	origin    *httputil.ReverseProxy
}

// ErrUpstreamNotAllowed is the error of an upstream that no rule lists.
var ErrUpstreamNotAllowed = errors.New("the upstream is not in the allow list")

// connectTimeout limits the time to open one upstream connection.
const connectTimeout = 15 * time.Second

// minProxyCredentialLen is the shortest proxy credential that New accepts.
const minProxyCredentialLen = 16

// proxyRealm is the realm of the header Proxy-Authenticate of a 407 answer.
const proxyRealm = "dagu egress proxy"

// DefaultMaxConnections is the bound of client connections when
// Options.MaxConnections is not set.
const DefaultMaxConnections = 512

// New makes a proxy. The upstream can match a rule of Rules or of
// CredentialRules. With a rule of CredentialRules, a client reaches the host of
// the upstream directly only with the proxy credential.
func New(opts Options) (*Proxy, error) {
	if opts.Rules == nil {
		opts.Rules = &Rules{}
	}
	if opts.CredentialRules == nil {
		opts.CredentialRules = &Rules{}
	}
	if opts.Rules.Len() == 0 && opts.CredentialRules.Len() == 0 {
		return nil, errors.New("egress proxy: the allow list is empty; a policy with no destination uses no proxy")
	}
	if err := checkCredentialRules(opts.Rules, opts.CredentialRules, opts.ProxyCredential); err != nil {
		return nil, err
	}
	if up := opts.Upstream; up != nil && !opts.Rules.Allows(up.host, up.port) && !opts.CredentialRules.Allows(up.host, up.port) {
		return nil, fmt.Errorf("%w: %s:%d", ErrUpstreamNotAllowed, up.host, up.port)
	}
	p := &Proxy{
		rules: opts.Rules, resolver: opts.Resolver, dial: opts.Dial, log: opts.Log, upstream: opts.Upstream,
		credentialRules: opts.CredentialRules, credential: opts.ProxyCredential,
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
	p.transport = &http.Transport{
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
		Transport:      p.transport,
		ModifyResponse: refuseSwitch,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.log("egress proxy: %s %s failed: %v", r.Method, r.URL.Host, err)
			if denied, ok := errors.AsType[*DeniedError](err); ok {
				http.Error(w, denied.Error(), http.StatusForbidden)
				return
			}
			http.Error(w, "egress proxy: the connection to the destination failed", http.StatusBadGateway)
		},
	}
	if p.upstream != nil {
		p.origin = &httputil.ReverseProxy{
			Rewrite:        p.rewriteOrigin,
			Transport:      p.transport,
			ModifyResponse: refuseSwitch,
			// A model API streams its answer: send each chunk at once.
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				p.log("egress proxy: %s %s:%d failed: %v", r.Method, p.upstream.host, p.upstream.port, err)
				http.Error(w, "egress proxy: the connection to the destination failed", http.StatusBadGateway)
			},
		}
	}
	return p, nil
}

// ErrCredentialRules is the error of a bad pair of CredentialRules and
// ProxyCredential.
var ErrCredentialRules = errors.New("the destinations with a proxy credential are not valid")

// checkCredentialRules checks the destinations that need the proxy credential.
// The credential is set exactly when a rule needs it. Two lists that can both
// match one destination contradict each other: a client would reach it with no
// credential. A plain wildcard voids the credential too: a client that controls
// the DNS of a name below it points the name at a guarded host and tunnels there
// with no credential. The error never holds the credential.
func checkCredentialRules(plain, guarded *Rules, credential string) error {
	hasRules := guarded.Len() > 0
	switch {
	case hasRules && credential == "":
		return fmt.Errorf("%w: the destinations %v need a proxy credential, got none", ErrCredentialRules, guarded.List())
	case !hasRules && credential != "":
		return fmt.Errorf("%w: a proxy credential needs at least one destination that uses it, got none", ErrCredentialRules)
	case !hasRules:
		return nil
	}
	if len(credential) < minProxyCredentialLen {
		return fmt.Errorf("%w: the proxy credential has %d characters, expected at least %d", ErrCredentialRules, len(credential), minProxyCredentialLen)
	}
	for i := 0; i < len(credential); i++ {
		if credential[i] < 0x20 || credential[i] == 0x7f {
			return fmt.Errorf("%w: the proxy credential has a control character at byte %d", ErrCredentialRules, i)
		}
	}
	for _, rule := range guarded.rules {
		for _, other := range plain.rules {
			if rule.overlaps(other) {
				return fmt.Errorf("%w: the destination %q that needs the proxy credential overlaps the destination %q that needs none",
					ErrCredentialRules, rule.Text, other.Text)
			}
		}
	}
	for _, other := range plain.rules {
		if other.Wildcard {
			return fmt.Errorf("%w: the destination %q that needs no proxy credential is a wildcard; a name below it can point at a destination that needs the credential, so list each name",
				ErrCredentialRules, other.Text)
		}
	}
	return nil
}

// refuseSwitch fails a 101 answer: no forwarded request asks for an upgrade.
// Refusing it here also closes the connection that the answer hands over, which
// the reverse proxy leaves open on its own refusal.
func refuseSwitch(res *http.Response) error {
	if res.StatusCode == http.StatusSwitchingProtocols {
		return errors.New("the destination switched protocols without an upgrade request")
	}
	return nil
}

// rewriteOrigin points a request in origin form at the upstream. It joins the
// paths, keeps the query, sets the host of the upstream, drops the credentials of
// the client and sets the key.
func (p *Proxy) rewriteOrigin(r *httputil.ProxyRequest) {
	r.SetURL(p.upstream.target)
	header := r.Out.Header
	for _, name := range []string{"X-Api-Key", "Authorization", "Proxy-Authorization", "Proxy-Connection"} {
		header.Del(name)
	}
	declineUpgrade(header)
	switch p.upstream.auth {
	case AuthXAPIKey:
		header.Set("X-Api-Key", p.upstream.key)
	case AuthBearer:
		header.Set("Authorization", "Bearer "+p.upstream.key)
	case AuthAnthropicOAuth:
		header.Set("Authorization", "Bearer "+p.upstream.key)
		header.Set("Anthropic-Beta", anthropicbeta.Join(header.Values("Anthropic-Beta"), anthropicbeta.OAuth))
	}
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

// clientCredential describes the credential that a client sent to the route of
// the upstream: the header and the digest of its value, never the value.
func clientCredential(header http.Header) string {
	if value := header.Get("X-Api-Key"); value != "" {
		return "x-api-key sha256:" + digest(value)
	}
	if value := header.Get("Authorization"); value != "" {
		// Only a known scheme is named: another first word can be the credential.
		scheme, token, ok := strings.Cut(value, " ")
		if ok && (strings.EqualFold(scheme, "Bearer") || strings.EqualFold(scheme, "Basic")) {
			return "authorization " + scheme + " sha256:" + digest(token)
		}
		return "authorization sha256:" + digest(value)
	}
	return "none"
}

// hasCredential reports whether the request carries the proxy credential in its
// only header Proxy-Authorization, in the form Basic with any user name. The
// check compares digests, so its time tells neither the bytes nor the length
// of the credential.
func (p *Proxy) hasCredential(r *http.Request) bool {
	values := r.Header.Values("Proxy-Authorization")
	if p.credential == "" || len(values) != 1 {
		return false
	}
	scheme, encoded, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return false
	}
	_, password, ok := strings.Cut(string(decoded), ":")
	got, want := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(p.credential))
	return ok && subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// admit decides for a destination of a request in absolute form or of CONNECT.
// It writes the answer of a refused request and returns false. A destination of
// CredentialRules needs the proxy credential. The suffix is the one of the allow
// log line: it names the proxy credential for such a destination.
func (p *Proxy) admit(w http.ResponseWriter, r *http.Request, host string, port int) (allowed bool, suffix string) {
	if p.rules.Allows(host, port) {
		return true, ""
	}
	if !p.credentialRules.Allows(host, port) {
		p.deny(w, host, port)
		return false, ""
	}
	if !p.hasCredential(r) {
		p.log("egress proxy: deny %s %s:%d: no valid proxy credential", r.Method, host, port)
		w.Header().Set("Proxy-Authenticate", `Basic realm="`+proxyRealm+`"`)
		http.Error(w, fmt.Sprintf("egress proxy: the destination %s:%d needs the proxy credential", host, port), http.StatusProxyAuthRequired)
		return false, ""
	}
	return true, " (proxy credential)"
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
	// A destination of CredentialRules gets here only after admit checked the
	// credential.
	if !p.rules.Allows(host, port) && !p.credentialRules.Allows(host, port) {
		return nil, &DeniedError{Host: host, Port: port}
	}
	var addrs []netip.Addr
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		addrs = []netip.Addr{ip}
	} else {
		// The pure Go resolver takes the IDNA ASCII form only, and a CONNECT can
		// name the host in Unicode.
		var ascii string
		if ascii, err = asciiHost(host); err == nil {
			addrs, err = p.resolver.LookupNetIP(ctx, "ip", ascii)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
	}
	// Anybody can make a name below a wildcard point to an address of the network
	// of the proxy, and a wildcard such as *.0.0.1 matches address literals. A
	// destination that only a wildcard allows gets public addresses only. A rule
	// that names the host can reach any address. The answer belongs to the
	// resolver, so the filter works on a copy.
	if !p.rules.AllowsExactly(host, port) && !p.credentialRules.AllowsExactly(host, port) {
		addrs = slices.DeleteFunc(slices.Clone(addrs), func(addr netip.Addr) bool { return !publicAddr(addr) })
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
// checks. The zone is checked before Unmap, which drops it.
func publicAddr(addr netip.Addr) bool {
	if addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() {
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
	if p.origin != nil && r.URL.Scheme == "" && r.URL.Host == "" && strings.HasPrefix(r.URL.Path, "/") {
		// TRACE asks the upstream to echo the request, with the key, back to the
		// client. TRACK is the same method on IIS.
		if strings.EqualFold(r.Method, http.MethodTrace) || strings.EqualFold(r.Method, "TRACK") {
			p.log("egress proxy: deny %s %s:%d (upstream): the answer would echo the key", r.Method, p.upstream.host, p.upstream.port)
			http.Error(w, "egress proxy: the upstream route does not forward TRACE or TRACK", http.StatusMethodNotAllowed)
			return
		}
		// The log has no path, which can hold a secret, and only digests of the
		// credentials.
		p.log("egress proxy: allow %s %s:%d (upstream): client credential %s replaced by the key sha256:%s (%s)",
			r.Method, p.upstream.host, p.upstream.port, clientCredential(r.Header), p.upstream.keyDigest, p.upstream.auth)
		p.origin.ServeHTTP(w, r)
		return
	}
	if !r.URL.IsAbs() || r.URL.Host == "" {
		message := "egress proxy: send a request with an absolute URL, or use the method CONNECT"
		if p.origin != nil {
			message = "egress proxy: send a request with a path that starts with /, an absolute URL, or use the method CONNECT"
		}
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	if r.URL.Scheme != "http" {
		http.Error(w, "egress proxy: a plain request needs the scheme http; use CONNECT for TLS", http.StatusBadRequest)
		return
	}
	host, port := r.URL.Hostname(), 80
	if text := r.URL.Port(); text != "" {
		parsed, err := strconv.Atoi(text)
		if err != nil || parsed < 1 || parsed > 65535 {
			http.Error(w, "egress proxy: the port is not valid", http.StatusBadRequest)
			return
		}
		port = parsed
	}
	allowed, suffix := p.admit(w, r, host, port)
	if !allowed {
		return
	}
	p.log("egress proxy: allow %s %s:%d%s", r.Method, host, port, suffix)
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
	allowed, suffix := p.admit(w, r, host, port)
	if !allowed {
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
	p.log("egress proxy: allow CONNECT %s:%d%s", host, port, suffix)
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
	// last holds the time of the last write as a duration since start, which
	// keeps the monotonic clock: a step of the wall clock ends no tunnel.
	start := time.Now()
	var last atomic.Int64
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(idle / 4)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if time.Since(start)-time.Duration(last.Load()) >= idle {
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
		_, _ = io.Copy(activityWriter{upstream, start, &last}, buffered)
		closeWrite(upstream)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(activityWriter{client, start, &last}, upstream)
		closeWrite(client)
	}()
	wg.Wait()
	close(done)
	_ = client.Close()
	_ = upstream.Close()
}

// activityWriter notes, since start, the time at which each write of a tunnel
// that moved bytes ended. A peer that reads slowly holds a write open.
type activityWriter struct {
	w     io.Writer
	start time.Time
	last  *atomic.Int64
}

func (a activityWriter) Write(p []byte) (int, error) {
	n, err := a.w.Write(p)
	if n > 0 {
		a.last.Store(int64(time.Since(a.start)))
	}
	return n, err
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
