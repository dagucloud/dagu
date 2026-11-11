// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress

import (
	"context"
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
		tunnelIdle: tunnelIdleTimeout,
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
		},
		Transport: p.transport,
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
			Rewrite:   p.rewriteOrigin,
			Transport: p.transport,
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
// credential. The error never holds the credential.
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
	switch p.upstream.auth {
	case AuthXAPIKey:
		header.Set("X-Api-Key", p.upstream.key)
	case AuthBearer:
		header.Set("Authorization", "Bearer "+p.upstream.key)
	case AuthAnthropicOAuth:
		header.Set("Authorization", "Bearer "+p.upstream.key)
		header.Set("Anthropic-Beta", withBeta(header.Values("Anthropic-Beta"), AnthropicOAuthBeta))
	}
}

// WithBeta is withBeta for the gateway of Spec 094, which sets the same header.
func WithBeta(values []string, beta string) string { return withBeta(values, beta) }

// withBeta returns the values of anthropic-beta as one list, with the beta added
// when no value names it. The values of the client stay in their order.
func withBeta(values []string, beta string) string {
	var list []string
	found := false
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			found = found || item == beta
			list = append(list, item)
		}
	}
	if !found {
		list = append(list, beta)
	}
	return strings.Join(list, ",")
}

// clientCredential describes the credential that a client sent to the route of
// the upstream: the header and the digest of its value, never the value.
func clientCredential(header http.Header) string {
	if value := header.Get("X-Api-Key"); value != "" {
		return "x-api-key sha256:" + Digest(value)
	}
	if value := header.Get("Authorization"); value != "" {
		scheme, token, ok := strings.Cut(value, " ")
		if !ok {
			return "authorization sha256:" + Digest(value)
		}
		return "authorization " + scheme + " sha256:" + Digest(token)
	}
	return "none"
}

// hasCredential reports whether the request carries the proxy credential in its
// only header Proxy-Authorization, in the form Basic with any user name. The
// check takes the same time for each wrong value of the same length.
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
	return ok && subtle.ConstantTimeCompare([]byte(password), []byte(p.credential)) == 1
}

// admit decides for a destination of a request in absolute form or of CONNECT.
// It writes the answer of a refused request and returns false. A destination of
// CredentialRules needs the proxy credential.
func (p *Proxy) admit(w http.ResponseWriter, r *http.Request, host string, port int) bool {
	if p.rules.Allows(host, port) {
		return true
	}
	if !p.credentialRules.Allows(host, port) {
		p.deny(w, host, port)
		return false
	}
	if !p.hasCredential(r) {
		p.log("egress proxy: deny %s %s:%d: no valid proxy credential", r.Method, host, port)
		w.Header().Set("Proxy-Authenticate", `Basic realm="`+proxyRealm+`"`)
		http.Error(w, fmt.Sprintf("egress proxy: the destination %s:%d needs the proxy credential", host, port), http.StatusProxyAuthRequired)
		return false
	}
	p.log("egress proxy: allow %s %s:%d (proxy credential)", r.Method, host, port)
	return true
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
		addrs, err = p.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
		// Anybody can make a name below a wildcard point to an address of the
		// network of the proxy. A name that only a wildcard allows gets public
		// addresses only. A rule that names the host can reach any address.
		if !p.rules.AllowsExactly(host, port) && !p.credentialRules.AllowsExactly(host, port) {
			addrs = slices.DeleteFunc(addrs, func(addr netip.Addr) bool { return !publicAddr(addr) })
			if len(addrs) == 0 {
				return nil, &DeniedError{Host: host, Port: port}
			}
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

// sharedAddressSpace is the range of carrier-grade NAT (RFC 6598), which is not
// a public address either.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// publicAddr reports whether addr is an address of the internet: not loopback,
// private, link-local, multicast, unspecified or shared.
func publicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !sharedAddressSpace.Contains(addr)
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
		if err != nil {
			http.Error(w, "egress proxy: the port is not a number", http.StatusBadRequest)
			return
		}
		port = parsed
	}
	if !p.admit(w, r, host, port) {
		return
	}
	if p.rules.Allows(host, port) {
		p.log("egress proxy: allow %s %s:%d", r.Method, host, port)
	}
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
	if !p.admit(w, r, host, port) {
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
	if p.rules.Allows(host, port) {
		p.log("egress proxy: allow CONNECT %s:%d", host, port)
	}
	_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
	pipe(client, buffered, upstream, p.tunnelIdle)
}

// tunnelIdleTimeout is the time after which a tunnel in which no byte moved in
// either direction ends. A client that opens tunnels and sends nothing cannot
// hold the descriptors of the proxy for ever.
const tunnelIdleTimeout = 10 * time.Minute

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

func closeWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

// Serve accepts connections on the listener until it closes.
func (p *Proxy) Serve(l net.Listener) error {
	server := &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	err := server.Serve(l)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
