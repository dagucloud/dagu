// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
)

const (
	// handshakeTimeout bounds opening a tunnel, from the browser's CONNECT
	// through reaching the server to the TLS handshakes with both.
	handshakeTimeout = 30 * time.Second
	// readHeaderTimeout bounds reading a request to the proxy.
	readHeaderTimeout = 10 * time.Second
	// leafLifetime and leafBackdate set the validity of the certificates
	// the browser is shown, which it accepts by their key alone.
	leafLifetime = 365 * 24 * time.Hour
	leafBackdate = time.Hour
	// leafSerialBits sizes the random serial numbers of those
	// certificates.
	leafSerialBits = 128
	// maxCommonNameLength is the longest common name a certificate holds.
	maxCommonNameLength = 64
	// relayBufferBytes sizes each read relayed from a server.
	relayBufferBytes = 32 << 10
	// drainTimeout bounds how long the proxy reads from a server once the
	// browser's side of a tunnel ended.
	drainTimeout = 5 * time.Second
	// defaultTLSPort is the port of a CONNECT authority that names none.
	defaultTLSPort = "443"
	// tlsScheme and plainScheme are the URL schemes the device's proxy
	// settings are asked about for a tunnel that carries TLS, and for one
	// that does not, which only a ws: URL opens.
	tlsScheme   = "https"
	plainScheme = "ws"
	// defaultPlainPort is the port of a ws: URL that names none.
	defaultPlainPort = "80"
	// maxURLBytes is the length of the longest URL Chrome sends.
	maxURLBytes = 2 << 20
	// remoteErrorOp marks the error of an alert a TLS peer sent.
	remoteErrorOp = "remote error"
	// alertPrefix starts the text of a TLS alert.
	alertPrefix        = "tls: "
	connectEstablished = "HTTP/1.1 200 Connection Established\r\n\r\n"
)

// Control paths, under the token.
const (
	eventsPath   = "events"
	pendingPath  = "pending"
	verifiedPath = "verified"
	pacPath      = "proxy.pac"
	afterParam   = "after"
	hostParam    = "host"
)

// rejectionAlerts are the TLS alerts by which a server refuses a client
// certificate: handshake failure, bad, unsupported, revoked, expired, and
// unknown certificate, unknown certificate authority, access denied, and
// certificate required.
var rejectionAlerts = []tls.AlertError{40, 42, 43, 44, 45, 46, 48, 49, 116}

// server is a running proxy.
type server struct {
	// ctx ends when the proxy stops.
	ctx        context.Context
	token      string
	entries    []*upstreamEntry
	trustedCAs []scopedAuthority
	pac        *pacEvaluator
	issuer     *leafIssuer
	events     *eventLog
	verified   *verifiedHosts
	pacScript  string // served when too large for a data: URL
	pending    *pendingLog
	// guardEntry presents no certificate, to the hosts the guard carries.
	guardEntry *upstreamEntry
	// publicRoots are the authorities whose sites the guard may pass the
	// browser's own TLS to; verdicts remembers which sites it may.
	publicRoots *x509.CertPool
	verdicts    *passVerdicts

	mu      sync.Mutex
	closed  bool
	tunnels map[net.Conn]struct{}
	// guard accepts the browser's other https: connections; nil without a
	// guard, or once it stopped.
	guard net.Listener
	// carryAll is set when the browser sends every request here, so a
	// destination Chrome skips in a PAC script can still arrive. Hosts no
	// entry names keep the device's route.
	carryAll bool
	// forward relays the plain HTTP requests the browser sends while
	// carryAll is set.
	forward *forwarder
}

// scopedAuthority is a TrustedCA ready to verify servers.
type scopedAuthority struct {
	hosts []string
	certs []*x509.Certificate
}

// upstreamEntry is an Entry ready to present.
type upstreamEntry struct {
	label string
	hosts []string
	// cert is empty for the guard, which presents no certificate.
	cert     tls.Certificate
	roots    *x509.CertPool
	sessions tls.ClientSessionCache
}

// newGuardEntry returns the entry of the hosts the guard carries, which
// presents no certificate. It trusts the system's roots and the CA
// certificates the entries name, which a private CA's other sites need.
func newGuardEntry(entries []Entry) *upstreamEntry {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	for _, entry := range entries {
		roots.AppendCertsFromPEM([]byte(entry.CAPEM))
	}
	return &upstreamEntry{roots: roots, sessions: tls.NewLRUClientSessionCache(0)}
}

// presents reports whether the entry has a certificate to present.
func (e *upstreamEntry) presents() bool {
	return len(e.cert.Certificate) > 0
}

func newUpstreamEntry(entry Entry) (*upstreamEntry, error) {
	cert, err := tls.X509KeyPair([]byte(entry.ChainPEM), []byte(entry.KeyPEM))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", entry.Label, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if entry.CAPEM != "" && !roots.AppendCertsFromPEM([]byte(entry.CAPEM)) {
		return nil, fmt.Errorf("%s: ca_pem holds no certificate", entry.Label)
	}
	return &upstreamEntry{
		label:    entry.Label,
		hosts:    entry.Hosts,
		cert:     cert,
		roots:    roots,
		sessions: tls.NewLRUClientSessionCache(0),
	}, nil
}

// upstreamConn is a TLS connection to a server.
type upstreamConn struct {
	*tls.Conn
	via route
	// requested is set once the server asked for a client certificate,
	// which it can do again in a renegotiation after the handshake.
	requested *atomic.Bool
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r, false)
		return
	}
	if s.carryAll && r.URL.IsAbs() && r.URL.Scheme == "http" {
		s.forward.ServeHTTP(w, r)
		return
	}
	// The control paths answer only requests made to the proxy itself, not
	// requests for another site that a client sends through it.
	if r.Method != http.MethodGet || r.URL.IsAbs() {
		http.NotFound(w, r)
		return
	}
	switch r.URL.Path {
	case s.controlPath(eventsPath):
		s.serveEvents(w, r)
	case s.controlPath(pendingPath):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.pending.list())
	case s.controlPath(verifiedPath):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.verified.has(hostpattern.Canonical(r.URL.Query().Get(hostParam))))
	case s.controlPath(pacPath):
		if s.pacScript == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", pacContentType)
		_, _ = io.WriteString(w, s.pacScript)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) controlPath(name string) string {
	return "/" + s.token + "/" + name
}

func (s *server) serveEvents(w http.ResponseWriter, r *http.Request) {
	var after int64
	if text := r.URL.Query().Get(afterParam); text != "" {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			http.Error(w, "after must be a number", http.StatusBadRequest)
			return
		}
		after = n
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.events.after(after))
}

// guardHandler serves the guard, which tunnels to any host.
type guardHandler struct{ s *server }

func (g guardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.NotFound(w, r)
		return
	}
	g.s.handleConnect(w, r, true)
}

// handleConnect opens a tunnel to the host a CONNECT request names. The
// certificate port ends TLS for the hosts the entries name. The guard, and
// the certificate port while it carries every request, does the same for
// ordinary hosts and presents no certificate, or passes the browser's TLS
// to a site that never asks for one. A loopback host the settings do not
// name, and a tunnel that carries no TLS, are spliced through unread.
func (s *server) handleConnect(w http.ResponseWriter, r *http.Request, guard bool) {
	host, port := splitAuthority(r.Host)
	if host == "" {
		http.Error(w, "the CONNECT request names no host", http.StatusBadRequest)
		return
	}
	entry := s.scopedEntry(host)
	if entry == nil {
		switch {
		case guard:
			entry = s.guardEntry
		case s.carryAll && s.guardListening() && !implicitBypass(host):
			// The guard still carries ordinary hosts. A loopback host the
			// settings do not name stays on the browser's own TLS, which is
			// where Chrome sent it before the manual proxy.
			entry = s.guardEntry
		case s.carryAll:
			s.relayOpaque(w, host, port)
			return
		default:
			s.events.add(EventRefused, host, "", fmt.Sprintf("the certificate proxy refused a connection to %s, which no certificate setting names", host))
			http.Error(w, "no certificate setting names this host", http.StatusForbidden)
			return
		}
	}
	deadline := time.Now().Add(handshakeTimeout)
	pending := s.pending.start(host)
	opened, ok := s.openTunnel(w, host, port, entry, guard || s.carriedByGuard(entry), deadline)
	if !ok {
		s.pending.end(pending)
		return
	}
	if opened.plain {
		// A ws: URL tunnels plain HTTP while the browser sends every scheme
		// here; there is no TLS to end.
		s.pending.end(pending)
		s.splice(opened.client, opened.raw)
		return
	}
	if s.carriedByGuard(entry) && !s.intercepted(host, port) {
		s.passOrTunnel(opened.client, opened.raw, opened.via, host, port, deadline, pending)
		return
	}
	s.tunnel(opened.client, opened.raw, opened.via, host, entry, deadline, pending)
}

// openedTunnel is a tunnel the browser opened with a CONNECT request.
type openedTunnel struct {
	// client is the browser's side, and raw the site's.
	client, raw net.Conn
	via         route
	// plain is set for a tunnel that carries no TLS, which the browser opens
	// for a ws: URL while it sends every request here.
	plain bool
}

// openTunnel reaches host and port and answers the browser's CONNECT
// request with a tunnel there, or with why there is none. guarded is set
// when the guard carries host, which a proxy asking for credentials stops.
// While the browser sends every request here, the route depends on what the
// tunnel carries, which shows only once it is open: the URL Chrome hands a
// PAC script is an https: one for TLS and the ws: URL of the request
// otherwise. A site that cannot be reached then ends the open tunnel.
func (s *server) openTunnel(w http.ResponseWriter, host, port string, entry *upstreamEntry, guarded bool, deadline time.Time) (openedTunnel, bool) {
	ctx, cancel := context.WithDeadline(s.ctx, deadline)
	defer cancel()
	var opened openedTunnel
	if !s.carryAll {
		var err error
		if opened.raw, opened.via, err = s.reach(ctx, pacRequestURL(tlsScheme, host, port, "/"), host, port, entry); err != nil {
			if s.unreachable(host, entry, guarded, err) {
				// A reset connection makes the browser try the route after the
				// guard; a closed guard keeps it on that route.
				resetConnection(w)
				return openedTunnel{}, false
			}
			http.Error(w, "the certificate proxy could not reach the site", http.StatusBadGateway)
			return openedTunnel{}, false
		}
	}
	conn, reader, ok := establish(w)
	if !ok {
		if opened.raw != nil {
			_ = opened.raw.Close()
		}
		return openedTunnel{}, false
	}
	// sent is what the proxy read of the browser's request, which the site
	// still gets.
	var sent []byte
	if s.carryAll {
		rawURL := pacRequestURL(tlsScheme, host, port, "/")
		// A loopback destination's route never depends on its URL.
		if opened.plain = !carriesTLS(conn, reader, deadline); opened.plain && !implicitBypass(host) {
			var target string
			target, sent = readRequestTarget(conn, reader, deadline)
			rawURL = pacRequestURL(plainScheme, host, port, target)
		}
		var err error
		if opened.raw, opened.via, err = s.reach(ctx, rawURL, host, port, entry); err != nil {
			s.unreachable(host, entry, guarded, err)
			_ = conn.Close()
			return openedTunnel{}, false
		}
	}
	opened.client = conn
	if reader.Buffered() > 0 || len(sent) > 0 {
		opened.client = &bufferedConn{Conn: conn, reader: reader}
	}
	if len(sent) > 0 {
		opened.client = replayed(opened.client, sent)
	}
	return opened, true
}

// establish takes over the connection of the CONNECT request w answers and
// tells the browser its tunnel is open. It returns the connection and what
// was buffered from it.
func establish(w http.ResponseWriter) (net.Conn, *bufio.Reader, bool) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "the connection cannot be tunneled", http.StatusInternalServerError)
		return nil, nil, false
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, false
	}
	if _, err := io.WriteString(conn, connectEstablished); err != nil {
		_ = conn.Close()
		return nil, nil, false
	}
	return conn, buffered.Reader, true
}

// unreachable records why host could not be reached, and reports whether
// the guard stopped instead, which a proxy asking for credentials does to a
// guarded host.
func (s *server) unreachable(host string, entry *upstreamEntry, guarded bool, err error) bool {
	if guarded && errors.Is(err, errProxyAuth) {
		s.stopGuard(host)
		return true
	}
	s.reportUnreachable(host, entry, err)
	return false
}

// reportUnreachable records why the proxy could not reach host.
func (s *server) reportUnreachable(host string, entry *upstreamEntry, err error) {
	kind := EventUpstream
	if errors.Is(err, errProxyAuth) {
		kind = EventProxyAuth
	}
	s.events.add(kind, host, entry.label, fmt.Sprintf("the certificate proxy could not reach %s: %s", host, oneLine(err)))
}

// stopGuard closes the guard, so the browser sends the requests it carried
// on the route after it, and records why.
func (s *server) stopGuard(host string) {
	s.mu.Lock()
	guard := s.guard
	s.guard = nil
	s.mu.Unlock()
	if guard == nil {
		return
	}
	_ = guard.Close()
	s.verified.dropGuarded()
	detail := "sites without a client certificate now go through the browser's own route; one of them that asks for a client certificate can make the system prompt for it"
	if s.carryAll {
		detail = "requests stay on the certificate proxy, which is also carrying a loopback destination, so those sites cannot fall through to the browser"
	}
	s.events.add(EventGuardOff, host, "", fmt.Sprintf(
		"the device's proxy asks %s for credentials the certificate proxy cannot give, so %s", host, detail))
}

// resetConnection resets the connection of the request w answers, which
// browsers take as a proxy that cannot be reached.
func resetConnection(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

// splitAuthority returns the host, canonical, and port a CONNECT authority
// names.
func splitAuthority(authority string) (string, string) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		host, port = authority, defaultTLSPort
	}
	return hostpattern.Canonical(host), port
}

// entryFor returns the first entry whose hosts match host, or nil.
func (s *server) entryFor(host string) *upstreamEntry {
	for _, entry := range s.entries {
		if hostpattern.MatchAny(host, entry.hosts) {
			return entry
		}
	}
	return nil
}

// scopedEntry combines a host's client identity with every authority that
// names that host. Authorities never change which identity is presented,
// and a host without an identity keeps the guard's trust.
func (s *server) scopedEntry(host string) *upstreamEntry {
	entry := s.entryFor(host)
	var selected *upstreamEntry
	for _, authority := range s.trustedCAs {
		if !hostpattern.MatchAny(host, authority.hosts) {
			continue
		}
		if selected == nil {
			if entry == nil {
				entry = s.guardEntry
			}
			// A named host gets a copy, so it never counts as carried by
			// the guard: it keeps the certificate port when the guard stops.
			scoped := *entry
			scoped.roots = entry.roots.Clone()
			selected = &scoped
		}
		for _, cert := range authority.certs {
			selected.roots.AddCert(cert)
		}
	}
	if selected != nil {
		return selected
	}
	return entry
}

// guardListening reports whether the guard still accepts connections.
func (s *server) guardListening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.guard != nil
}

// relayOpaque tunnels host without ending its TLS, so the browser verifies
// the server itself and a configured authority is not applied to it.
func (s *server) relayOpaque(w http.ResponseWriter, host, port string) {
	pending := s.pending.start(host)
	opened, ok := s.openTunnel(w, host, port, s.guardEntry, false, time.Now().Add(handshakeTimeout))
	// Past reaching the site, the proxy waits on nothing of its own.
	s.pending.end(pending)
	if ok {
		s.splice(opened.client, opened.raw)
	}
}

// splice relays the bytes between client and raw, without reading them,
// until both directions end.
func (s *server) splice(client, raw net.Conn) {
	if !s.track(client) {
		_ = client.Close()
		_ = raw.Close()
		return
	}
	defer s.untrack(client)
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(raw, client)
		_ = raw.Close()
		close(done)
	}()
	_, _ = io.Copy(client, raw)
	_ = client.Close()
	<-done
}

// readRequestTarget reads the request line a plain tunnel starts with and
// returns its target, as /socket in "GET /socket HTTP/1.1", with the bytes
// it read. A line that has not ended by deadline, or within the longest URL
// Chrome sends, gives /.
func readRequestTarget(conn net.Conn, reader *bufio.Reader, deadline time.Time) (string, []byte) {
	_ = conn.SetReadDeadline(deadline)
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	var line []byte
	for len(line) <= maxURLBytes {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if err == nil {
			if fields := strings.Fields(string(line)); len(fields) == 3 && strings.HasPrefix(fields[1], "/") {
				return fields[1], line
			}
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			break
		}
	}
	return "/", line
}

// carriesTLS reports whether what the client sends over conn, read through
// reader, starts with a TLS handshake record. A client still silent at
// deadline counts as TLS, which fails the same way.
func carriesTLS(conn net.Conn, reader *bufio.Reader, deadline time.Time) bool {
	_ = conn.SetReadDeadline(deadline)
	first, err := reader.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	return err != nil || first[0] == recordTypeHandshake
}

// carriedByGuard reports whether entry is the guard's own, which only the
// hosts no entry or authority names use.
func (s *server) carriedByGuard(entry *upstreamEntry) bool {
	return entry == s.guardEntry
}

// tunnel relays the browser's connection to the server reached over raw.
// The browser's handshake waits for the one with the server, so it learns
// the server's application protocol, and fails when that one does or by
// deadline.
func (s *server) tunnel(client, raw net.Conn, via route, host string, entry *upstreamEntry, deadline time.Time, pending int64) {
	defer s.pending.end(pending)
	if !s.track(client) {
		_ = client.Close()
		_ = raw.Close()
		return
	}
	defer s.untrack(client)

	var upstream *upstreamConn
	downstream := tls.Server(client, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			s.pending.set(pending, PhaseHandshake)
			conn, err := s.handshake(hello.Context(), raw, via, host, entry, hello.SupportedProtos)
			if err != nil {
				return nil, err
			}
			upstream = conn
			leaf, err := s.issuer.leaf(host)
			if err != nil {
				return nil, err
			}
			config := &tls.Config{Certificates: []tls.Certificate{*leaf}, MinVersion: tls.VersionTLS12}
			if protocol := conn.ConnectionState().NegotiatedProtocol; protocol != "" {
				config.NextProtos = []string{protocol}
			}
			return config, nil
		},
	})
	ctx, cancel := context.WithDeadline(s.ctx, deadline)
	err := downstream.HandshakeContext(ctx)
	cancel()
	if err != nil {
		_ = client.Close()
		_ = raw.Close()
		return
	}
	s.pending.set(pending, PhaseAnswer)
	s.reportConnected(host, entry, upstream)
	s.relay(downstream, upstream, host, entry, pending)
}

// reach connects to host and port on the route the device's proxy settings
// give rawURL.
func (s *server) reach(ctx context.Context, rawURL, host, port string, entry *upstreamEntry) (net.Conn, route, error) {
	routes := s.routesTo(ctx, rawURL, host, entry.label)
	return dialRoutes(ctx, routes, net.JoinHostPort(host, port), s.proxyTLS)
}

// routesTo returns the routes to rawURL, whose host is host. Chrome never
// sends a destination it implicitly bypasses to a proxy, and never asks
// the device's proxy settings about it, so neither does the proxy.
func (s *server) routesTo(ctx context.Context, rawURL, host, label string) []route {
	if implicitBypass(host) {
		return []route{{kind: routeDirect}}
	}
	found := s.pac.routes(ctx, rawURL, host)
	for _, skipped := range found.skipped {
		s.events.add(EventUpstream, host, label, fmt.Sprintf(
			"the device's proxy settings route %s through %s, which the certificate proxy does not support; it tries the next route", host, skipped))
	}
	return found.routes
}

// proxyTLS returns the TLS configuration for an HTTPS proxy at host. An
// entry whose hosts match the proxy's presents its certificate to it, and
// its ca_file is trusted for it.
func (s *server) proxyTLS(host string) *tls.Config {
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if entry := s.scopedEntry(hostpattern.Canonical(host)); entry != nil {
		config.RootCAs = entry.roots
		config.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &entry.cert, nil
		}
	}
	return config
}

// handshake opens TLS to the server at host over raw, presenting entry's
// certificate when the server asks for one.
func (s *server) handshake(ctx context.Context, raw net.Conn, via route, host string, entry *upstreamEntry, protocols []string) (*upstreamConn, error) {
	requested := &atomic.Bool{}
	conn := tls.Client(raw, &tls.Config{
		ServerName: host,
		NextProtos: protocols,
		RootCAs:    entry.roots,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			requested.Store(true)
			// The guard's entry is empty, which sends no certificate.
			return &entry.cert, nil
		},
		Renegotiation:      tls.RenegotiateFreelyAsClient,
		MinVersion:         tls.VersionTLS12,
		ClientSessionCache: entry.sessions,
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		s.reportHandshakeFailure(host, entry, requested.Load(), err)
		return nil, err
	}
	// Recorded as the server is verified, as a failure is, so overlapping
	// connections to a host leave the latest verification's outcome.
	s.verified.add(host, s.carriedByGuard(entry))
	return &upstreamConn{Conn: conn, via: via, requested: requested}, nil
}

// reportConnected records the first connection to a host an entry or an
// authority names, or, for the guard, a server that asked for a
// certificate it does not have.
func (s *server) reportConnected(host string, entry *upstreamEntry, upstream *upstreamConn) {
	switch {
	case !entry.presents():
		if upstream.requested.Load() {
			s.reportMissing(host)
		}
		if !s.carriedByGuard(entry) {
			s.events.connect(host, entry.label, false, fmt.Sprintf("verified the server certificate of %s through %s", host, upstream.via))
		}
	case upstream.requested.Load():
		s.events.connect(host, entry.label, true, fmt.Sprintf("presented the client certificate %s to %s through %s", entry.label, host, upstream.via))
	default:
		s.events.connect(host, entry.label, false, fmt.Sprintf("connected to %s through %s; it did not ask for a client certificate", host, upstream.via))
	}
}

// reportHandshakeFailure records why the handshake with a server failed.
func (s *server) reportHandshakeFailure(host string, entry *upstreamEntry, requested bool, err error) {
	if alert, ok := rejectionAlert(err); ok && requested {
		s.reportRefusal(host, entry, alert)
		return
	}
	if verifyErr, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		s.verified.remove(host)
		message := fmt.Sprintf("%s presented a server certificate that is not trusted (%v)", host, verifyErr.Err)
		if entry.presents() {
			message += fmt.Sprintf("; if a private CA issued it, name that CA in ca_file of %s", entry.label)
		}
		s.events.add(EventServerCertificate, host, entry.label, message)
		return
	}
	s.events.add(EventUpstream, host, entry.label, fmt.Sprintf("the TLS handshake with %s failed: %v", host, err))
}

// reportRefusal records a server that refused the browser for its client
// certificate: the one entry presented, or none at all.
func (s *server) reportRefusal(host string, entry *upstreamEntry, alert string) {
	if !entry.presents() {
		s.reportMissing(host)
		return
	}
	s.events.add(EventClientCertificateRejected, host, entry.label, fmt.Sprintf("%s rejected the client certificate %s: %s", host, entry.label, alert))
}

func (s *server) reportMissing(host string) {
	s.events.add(EventClientCertificateMissing, host, "", fmt.Sprintf("%s asks for a TLS client certificate, and none is configured for it", host))
}

// rejectionAlert returns the alert of err when it is one by which a server
// refuses a client certificate.
func rejectionAlert(err error) (string, bool) {
	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != remoteErrorOp || opErr.Err == nil {
		return "", false
	}
	for _, alert := range rejectionAlerts {
		if opErr.Err.Error() == alert.Error() {
			return strings.TrimPrefix(alert.Error(), alertPrefix), true
		}
	}
	return "", false
}

// relay copies between the browser and the server until either side ends.
// Once the browser's side ends, what the server already sent, such as an
// alert refusing the client certificate, is still read for up to
// drainTimeout before both close.
func (s *server) relay(client *tls.Conn, upstream *upstreamConn, host string, entry *upstreamEntry, pending int64) {
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.SetReadDeadline(time.Now().Add(drainTimeout))
	}()
	s.copyFromServer(client, upstream, host, entry, pending)
	_ = client.Close()
	_ = upstream.Close()
}

// copyFromServer copies what the server sends to the browser. Under TLS
// 1.3, and in a TLS 1.2 renegotiation, a server refuses a client
// certificate after the handshake, so an alert that ends the connection
// before it sent anything is reported.
func (s *server) copyFromServer(client net.Conn, upstream *upstreamConn, host string, entry *upstreamEntry, pending int64) {
	buf := make([]byte, relayBufferBytes)
	received := false
	for {
		n, err := upstream.Read(buf)
		if n > 0 {
			if !received {
				received = true
				s.pending.end(pending)
			}
			if _, err := client.Write(buf[:n]); err != nil {
				return
			}
		}
		if err == nil {
			continue
		}
		if alert, ok := rejectionAlert(err); ok && !received && upstream.requested.Load() {
			s.reportRefusal(host, entry, alert)
		}
		return
	}
}

func (s *server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.tunnels[conn] = struct{}{}
	return true
}

func (s *server) untrack(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tunnels, conn)
}

// closeTunnels ends every tunnel, and refuses new ones.
func (s *server) closeTunnels() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for conn := range s.tunnels {
		_ = conn.Close()
	}
}

// pacRequestURL returns the URL a browser hands a PAC script for a scheme
// request for target at host and port, scheme being tlsScheme or
// plainScheme. A browser keeps the target of a ws: URL only.
func pacRequestURL(scheme, host, port, target string) string {
	if scheme != plainScheme {
		target = "/"
	}
	defaultPort := defaultTLSPort
	if scheme == plainScheme {
		defaultPort = defaultPlainPort
	}
	if port == defaultPort {
		if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
			return scheme + "://[" + host + "]" + target
		}
		return scheme + "://" + host + target
	}
	return scheme + "://" + net.JoinHostPort(host, port) + target
}

// oneLine joins the lines of err, which errors.Join separates.
func oneLine(err error) string {
	return strings.ReplaceAll(err.Error(), "\n", "; ")
}

// bufferedConn reads what was buffered from a connection before the rest.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

// leafIssuer mints the certificates the browser is shown, one per host, all
// self-signed with one key, so that a single key hash covers them.
type leafIssuer struct {
	key *ecdsa.PrivateKey
	// spki is the base64 SHA-256 hash of the key's SubjectPublicKeyInfo.
	spki   string
	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

func newLeafIssuer() (*leafIssuer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	return &leafIssuer{key: key, spki: base64.StdEncoding.EncodeToString(sum[:]), leaves: map[string]*tls.Certificate{}}, nil
}

// leaf returns the certificate for host.
func (l *leafIssuer) leaf(host string) (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if leaf, ok := l.leaves[host]; ok {
		return leaf, nil
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), leafSerialBits))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		NotBefore:             now.Add(-leafBackdate),
		NotAfter:              now.Add(leafLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if len(host) <= maxCommonNameLength {
		template.Subject = pkix.Name{CommonName: host}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		template.IPAddresses = []net.IP{addr.AsSlice()}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &l.key.PublicKey, l.key)
	if err != nil {
		return nil, fmt.Errorf("mint a certificate for %s: %w", host, err)
	}
	leaf := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: l.key}
	l.leaves[host] = leaf
	return leaf, nil
}
