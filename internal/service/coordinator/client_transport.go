// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

const (
	// h2cWindowLength is how long clients keep h2c for a coordinator address
	// after built-in TLS failed there, before they try built-in TLS again.
	h2cWindowLength = 10 * time.Minute
	// replanInterval is how often a connection on h2c without a TLS source
	// checks whether a new connection would use built-in TLS. It matches the
	// registry refresh.
	replanInterval = 10 * time.Second
	// retireGrace is how long a replaced connection stays open after its
	// last call, for a caller that took it before the replacement.
	retireGrace = time.Minute
	// tlsAlertRecord is the record type of a TLS alert.
	tlsAlertRecord = 0x15
	// connectionProtocol is the protocol connectionCredentials report. It is
	// not "tls", so gRPC sends the :scheme http that a plaintext proxy on an
	// h2c path expects. Coordinators ignore :scheme.
	connectionProtocol = "dagu-peer"
)

// Transports and their sources, as the start line and the per-coordinator
// lines name them.
const (
	transportH2C        = "h2c"
	transportTLS        = "TLS"
	transportBuiltinTLS = "built-in TLS"

	sourceInsecure    = "peer.insecure: true"
	sourceTLSSettings = "peer.insecure: false"
	sourceWorkerCert  = "worker certificate"
	sourceInvite      = "worker.invite"
	sourceCACertFile  = "peer.ca_cert_file"
	sourceDataDir     = "the coordinator's data directory"
	sourceNoTLS       = "no TLS source"
	sourceNetworkPath = "tls failed on the network path"
)

// h2cReasonMetadataKey carries, on each call over h2c, why the client uses
// h2c. A call over h2c without it comes from a Dagu that predates the key.
const h2cReasonMetadataKey = "dagu-peer-h2c-reason"

// The h2c reasons a client sends, and the one the coordinator reports for a
// call that carries none.
const (
	h2cReasonInsecure     = "peer.insecure"
	h2cReasonNoTLS        = sourceNoTLS
	h2cReasonNetworkPath  = sourceNetworkPath
	h2cReasonOlderVersion = "older version"
)

// requireTLSClientPrefix starts what a client says when
// coordinator.require_tls refuses its call over h2c.
const requireTLSClientPrefix = "The coordinator requires TLS. "

// requireTLSClientMessage returns what a client says when
// coordinator.require_tls refuses its call over h2c from source: the
// refusal and the change that puts the client on TLS.
func requireTLSClientMessage(source string) string {
	switch source {
	case sourceInsecure:
		return requireTLSClientPrefix + "Remove peer.insecure: true."
	case sourceNetworkPath:
		return requireTLSClientPrefix + "Let the network path to it pass TLS."
	default:
		return requireTLSClientPrefix + "Set peer.ca_cert_file to the coordinator's CA certificate."
	}
}

// otherDaguFormat is what a worker with a worker certificate says about a
// coordinator on built-in TLS whose CA did not sign the certificate.
const otherDaguFormat = "%s belongs to a different Dagu than the one this worker joined. Get a new command from \"Add worker\" to join it."

// ownCertificatesFormat is what a worker with a worker certificate says
// about a coordinator that serves TLS with its own certificates.
const ownCertificatesFormat = "Coordinator at %s serves TLS with its own certificates, so this worker cannot use its worker certificate there. Remove peer.cert_file and peer.key_file from the coordinator's config, so it serves built-in TLS."

// transportChoice is a transport and the source that selected it.
type transportChoice struct {
	transport string
	source    string
}

func (c transportChoice) String() string {
	return c.transport + " (from " + c.source + ")"
}

// StartTransport returns the transport the client starts with and what
// selected it, as "<transport> (from <source>)". It fails with
// ErrMissingTLSConfig when peer.insecure is false and nothing selects TLS.
func (c *Config) StartTransport() (string, error) {
	choice, err := c.startTransport()
	if err != nil {
		return "", err
	}
	return choice.String(), nil
}

// startTransport picks the transport by the rows of Spec 108 §Client
// Transport. The published CA certificate counts when this process can read
// it; whether a coordinator serves built-in TLS is known per connection.
func (c *Config) startTransport() (transportChoice, error) {
	// A worker's settings that contradict its invite stop it at start, so
	// an invite or a worker certificate decides alone.
	switch {
	case c.workerCert != nil:
		return transportChoice{transportBuiltinTLS, sourceWorkerCert}, nil
	case c.invite != nil:
		return transportChoice{transportBuiltinTLS, sourceInvite}, nil
	case c.Insecure && !c.InsecureUnset:
		return transportChoice{transportH2C, sourceInsecure}, nil
	case !c.Insecure && c.hasTLSSettings():
		return transportChoice{transportTLS, sourceTLSSettings}, nil
	case c.BuiltinCAFile != "":
		return transportChoice{transportBuiltinTLS, sourceCACertFile}, nil
	case c.canReadPublishedCA():
		return transportChoice{transportBuiltinTLS, sourceDataDir}, nil
	case !c.Insecure:
		return transportChoice{}, ErrMissingTLSConfig
	}
	return transportChoice{transportH2C, sourceNoTLS}, nil
}

// inviteWorker reports an invite worker, which has no credential other than
// its invite or worker certificate and so never falls back to h2c.
func (c *Config) inviteWorker() bool {
	return c.invite != nil || c.workerCert != nil
}

func (c *Config) hasTLSSettings() bool {
	return c.CertFile != "" || c.KeyFile != "" || c.CAFile != "" || c.SkipTLSVerify
}

// picksTransportPerConnection reports whether the client picks the transport
// for each connection, rows 3 to 5 of Spec 108 §Client Transport.
func (c *Config) picksTransportPerConnection() bool {
	return c.inviteWorker() || c.InsecureUnset || (!c.Insecure && !c.hasTLSSettings())
}

func (c *Config) canReadPublishedCA() bool {
	if c.PublishedCAFile == "" {
		return false
	}
	_, err := readCertPool(c.PublishedCAFile)
	return err == nil
}

// clientTransport picks the transport of each connection of a client to a
// coordinator, and logs what it has to report with the context of the next
// call.
type clientTransport struct {
	config *Config
	// start is the client's start transport. The transport to an address
	// is logged when it differs from start, and again each time it changes.
	start   transportChoice
	now     func() time.Time
	windows *h2cWindows

	mu      sync.Mutex
	current map[string]transportChoice
	refused map[string]bool
	pending []func(context.Context)
	// retired holds replaced connections that are not closed yet.
	retired []*connLink
}

func newClientTransport(config *Config) *clientTransport {
	start, _ := config.startTransport()
	return &clientTransport{
		config:  config,
		start:   start,
		now:     time.Now,
		windows: sharedH2CWindows(config.FallbackFile),
		current: make(map[string]transportChoice),
		refused: make(map[string]bool),
	}
}

// newLink returns the state of a new connection to address, and the options
// that dial it.
func (t *clientTransport) newLink(address string) (*connLink, []grpc.DialOption) {
	link := &connLink{transport: t, address: address}
	opts := []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(link.unaryInterceptor),
		grpc.WithChainStreamInterceptor(link.streamInterceptor),
	}
	if t.config.picksTransportPerConnection() {
		opts = append(opts, grpc.WithTransportCredentials(&connectionCredentials{link: link}))
	}
	return link, opts
}

// connectionPlan is the transport of one connection: built-in TLS that
// verifies against roots, or h2c when roots is nil.
type connectionPlan struct {
	choice transportChoice
	roots  *x509.CertPool
	// cert is the worker certificate the connection presents, or nil.
	cert *tls.Certificate
	// pin is the pin of an invite, which verifies the coordinator in place
	// of roots during enrollment.
	pin string
	// unlisted reports an address that no live registry entry lists, with
	// the published CA certificate as the only TLS source.
	unlisted bool
	// windowed reports h2c that an open h2c window selects.
	windowed bool
	// tlsError is the handshake error that opened that window.
	tlsError string
}

// h2c reports a plan for h2c.
func (p connectionPlan) h2c() bool {
	return p.roots == nil && p.pin == ""
}

// plan picks the transport of a new connection to address. A client with
// peer.insecure: false never gets h2c.
func (t *clientTransport) plan(ctx context.Context, address string) (connectionPlan, error) {
	switch {
	case t.config.workerCert != nil:
		roots := x509.NewCertPool()
		roots.AddCert(t.config.workerCA)
		return connectionPlan{choice: transportChoice{transportBuiltinTLS, sourceWorkerCert}, roots: roots, cert: t.config.workerCert}, nil
	case t.config.invite != nil:
		return connectionPlan{choice: transportChoice{transportBuiltinTLS, sourceInvite}, pin: t.config.invite.pin}, nil
	}
	if t.config.InsecureUnset {
		if window, ok := t.windows.active(address, t.now()); ok {
			return connectionPlan{choice: transportChoice{transportH2C, window.Source}, windowed: true, tlsError: window.Error}, nil
		}
	}
	if file := t.config.BuiltinCAFile; file != "" {
		roots, err := readCertPool(file)
		if err != nil {
			return connectionPlan{}, fmt.Errorf("peer.ca_cert_file: %w", err)
		}
		return connectionPlan{choice: transportChoice{transportBuiltinTLS, sourceCACertFile}, roots: roots}, nil
	}
	if t.config.PublishedCAFile != "" {
		if roots, err := readCertPool(t.config.PublishedCAFile); err == nil {
			if listed, builtin := t.builtinTLSAt(ctx, address); builtin {
				return connectionPlan{choice: transportChoice{transportBuiltinTLS, sourceDataDir}, roots: roots, unlisted: !listed}, nil
			}
		}
	}
	if !t.config.InsecureUnset {
		return connectionPlan{}, ErrMissingTLSConfig
	}
	return connectionPlan{choice: transportChoice{transportH2C, sourceNoTLS}}, nil
}

// builtinTLSAt reports whether a live entry of the local registry lists
// address, and whether address serves built-in TLS by the registry: its
// entry reports built-in TLS, or, for an address no entry lists, any entry
// does.
func (t *clientTransport) builtinTLSAt(ctx context.Context, address string) (listed, builtin bool) {
	if t.config.LocalRegistry == nil {
		return false, false
	}
	members, err := t.config.LocalRegistry.GetServiceMembers(ctx, serviceregistry.ServiceNameCoordinator)
	if err != nil {
		return false, false
	}
	for _, member := range members {
		serves := member.TLS == serviceregistry.TLSBuiltin
		if strings.EqualFold(coordinatorAddress(member), address) {
			return true, serves
		}
		builtin = builtin || serves
	}
	return false, builtin
}

// use records that a connection to address uses choice.
func (t *clientTransport) use(address string, choice transportChoice) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous, ok := t.current[address]
	if !ok {
		previous = t.start
	}
	t.current[address] = choice
	if choice != previous {
		line := fmt.Sprintf("Peer transport to %s: %s", address, choice)
		t.pending = append(t.pending, func(ctx context.Context) { logger.Info(ctx, line) })
	}
}

// failed handles a built-in TLS handshake to address that failed with err.
// For a client with peer.insecure unset, a transport-level failure, or a
// certificate that does not chain to the published CA certificate on an
// unlisted address, opens an h2c window for the address. The client that
// opens a window logs its warning.
func (t *clientTransport) failed(address string, plan connectionPlan, conn *handshakeConn, err error) {
	if format := foreignCoordinatorFormat(err); plan.cert != nil && format != "" {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.pending = append(t.pending, func(ctx context.Context) {
			logger.Error(ctx, fmt.Sprintf(format, address), tag.Error(err))
		})
		return
	}
	source := t.config.fallbackSource(plan, conn, err)
	if source == "" {
		return
	}
	now := t.now()
	window := h2cWindow{Until: now.Add(h2cWindowLength), Source: source, Error: err.Error()}
	if !t.windows.open(address, window, now) || source != sourceNetworkPath {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = append(t.pending, func(ctx context.Context) {
		logger.Warn(ctx, "Built-in TLS failed on the network path; using h2c for 10 minutes",
			tag.Addr(address), tag.Error(err))
	})
}

// fallbackSource returns the source of the h2c a client falls back to after
// a built-in TLS handshake on plan ended with err, or "" when it does not
// fall back: only a client with peer.insecure unset falls back, after a
// transport-level failure, or after a certificate on an unlisted address
// that does not chain to the published CA certificate.
func (c *Config) fallbackSource(plan connectionPlan, conn *handshakeConn, err error) string {
	switch {
	case !c.InsecureUnset, c.inviteWorker():
		return ""
	case conn.transportLevelFailure(err):
		return sourceNetworkPath
	case plan.unlisted && isUnknownAuthority(err):
		return sourceNoTLS
	}
	return ""
}

// awaitReady waits, at most DialTimeout, until cc connects again.
func (t *clientTransport) awaitReady(ctx context.Context, cc *grpc.ClientConn) {
	ctx, cancel := context.WithTimeout(ctx, t.config.DialTimeout)
	defer cancel()
	for state := cc.GetState(); state != connectivity.Ready; state = cc.GetState() {
		cc.Connect()
		if !cc.WaitForStateChange(ctx, state) {
			return
		}
	}
}

// report logs, with ctx, what the transport has to report after a call of
// method on link that ended with err, and the refusal of
// coordinator.require_tls once until a call other than a health check
// succeeds. A refusal of a connection that an h2c window put on h2c ends the
// window, and the client opens a new connection.
func (t *clientTransport) report(ctx context.Context, link *connLink, method string, err error, p *peer.Peer) {
	refusal := isRequireTLSRefusal(err)
	if refusal && link.refuse() {
		t.windows.end(link.address, t.now())
	}
	t.mu.Lock()
	pending := t.pending
	t.pending = nil
	firstRefusal := refusal && !t.refused[link.address]
	switch {
	case refusal:
		t.refused[link.address] = true
	case err == nil && !strings.HasPrefix(method, grpcHealthMethodPrefix):
		delete(t.refused, link.address)
	}
	t.mu.Unlock()

	for _, log := range pending {
		log(ctx)
	}
	if !firstRefusal {
		return
	}
	info := h2cInfoOf(p)
	attrs := []slog.Attr{tag.Addr(link.address)}
	if info.tlsError != "" {
		attrs = append(attrs, slog.String("tls-error", info.tlsError))
	}
	logger.Error(ctx, requireTLSClientMessage(info.source), attrs...)
}

// refusalError returns the error a call over the connection of p ends with:
// err, or for a coordinator.require_tls refusal, the refusal with the
// change for the h2c reason of that connection, so callers that log it
// agree with the refusal line. It runs after report, which matches the
// coordinator's message.
func refusalError(err error, p *peer.Peer) error {
	if !isRequireTLSRefusal(err) {
		return err
	}
	return status.Error(codes.Unauthenticated, requireTLSClientMessage(h2cInfoOf(p).source))
}

// h2cInfoOf returns the h2c details of the connection of p, or none when
// it is not on h2c.
func h2cInfoOf(p *peer.Peer) h2cInfo {
	info, _ := p.AuthInfo.(h2cInfo)
	return info
}

func isRequireTLSRefusal(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Unauthenticated && st.Message() == requireTLSMessage
}

// connLink is the state of one gRPC connection of a client to a coordinator
// address: what its current transport connection uses, its calls in flight,
// and whether it closes once they end.
type connLink struct {
	transport *clientTransport
	address   string
	conn      *grpc.ClientConn

	mu sync.Mutex
	// h2c reports a current transport connection on h2c that the client
	// picked, and windowed one that an h2c window selected.
	h2c      bool
	windowed bool
	// redial reports that a new connection would pick another transport.
	redial   bool
	checked  time.Time
	inflight int
	retired  bool
	// handshakeErr is why the last TLS handshake failed, which gRPC
	// reports only as Unavailable.
	handshakeErr error
	// lastUsed is when the connection was retired, or when a call on it
	// ended since.
	lastUsed time.Time
}

// connected records the plan of a new transport connection.
func (l *connLink) connected(plan connectionPlan) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.h2c = plan.h2c()
	l.windowed = plan.windowed
	l.redial = false
	l.checked = l.transport.now()
}

func (l *connLink) handshakeFailed(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.handshakeErr = err
}

func (l *connLink) lastHandshakeError() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handshakeErr
}

// refuse records a require_tls refusal and reports whether an h2c window
// put the current connection on h2c, so a new connection tries built-in TLS
// again. A connection on h2c without a TLS source finds one by the check of
// stale.
func (l *connLink) refuse() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.h2c && l.windowed {
		l.redial = true
	}
	return l.redial
}

// stale reports whether the client should replace the connection, since a
// new one would pick another transport: a call on h2c was refused for TLS,
// the h2c window ended, or a check, at most every replanInterval, finds a
// TLS source.
func (l *connLink) stale() bool {
	now := l.transport.now()
	l.mu.Lock()
	switch {
	case l.retired || !l.h2c:
		l.mu.Unlock()
		return false
	case l.redial:
		l.mu.Unlock()
		return true
	case !l.windowed && now.Sub(l.checked) < replanInterval:
		l.mu.Unlock()
		return false
	}
	windowed := l.windowed
	l.checked = now
	l.mu.Unlock()

	var redial bool
	if windowed {
		_, open := l.transport.windows.active(l.address, now)
		redial = !open
	} else {
		plan, err := l.transport.plan(context.Background(), l.address)
		redial = err == nil && plan.roots != nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.redial = l.redial || redial
	return l.redial
}

// retire hands the connection to the transport, which closes it once no
// call has used it for retireGrace. A caller that took the connection just
// before its replacement can still finish its call on it.
func (l *connLink) retire() {
	now := l.transport.now()
	l.mu.Lock()
	l.retired = true
	l.lastUsed = now
	l.mu.Unlock()
	l.transport.mu.Lock()
	l.transport.retired = append(l.transport.retired, l)
	l.transport.mu.Unlock()
	l.transport.closeRetired(now)
}

// idleSince reports whether no call has used the connection since before
// cutoff.
func (l *connLink) idleSince(cutoff time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight == 0 && !l.lastUsed.After(cutoff)
}

func (l *connLink) begin() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inflight++
}

func (l *connLink) end() {
	now := l.transport.now()
	l.mu.Lock()
	l.inflight--
	l.lastUsed = now
	l.mu.Unlock()
	l.transport.closeRetired(now)
}

// closeRetired closes the retired connections that no call has used for
// retireGrace at now.
func (t *clientTransport) closeRetired(now time.Time) {
	t.mu.Lock()
	var idle []*connLink
	t.retired = slices.DeleteFunc(t.retired, func(l *connLink) bool {
		if l.idleSince(now.Add(-retireGrace)) {
			idle = append(idle, l)
			return true
		}
		return false
	})
	t.mu.Unlock()
	for _, l := range idle {
		_ = l.conn.Close()
	}
}

// closeAllRetired closes every retired connection.
func (t *clientTransport) closeAllRetired() {
	t.mu.Lock()
	retired := t.retired
	t.retired = nil
	t.mu.Unlock()
	for _, l := range retired {
		_ = l.conn.Close()
	}
}

// resend reports whether a call that failed with err, whose attempt got the
// peer p, should be sent once more: it never got a transport, so no
// coordinator saw it, and the address has an open h2c window, so the next
// connection uses h2c.
func (l *connLink) resend(err error, p *peer.Peer) bool {
	if status.Code(err) != codes.Unavailable || p.Addr != nil || !l.transport.config.InsecureUnset || l.transport.config.inviteWorker() {
		return false
	}
	_, open := l.transport.windows.active(l.address, l.transport.now())
	return open
}

// withPeer returns opts and an option that records the peer of the call's
// attempt in p, without changing the caller's slice.
func withPeer(opts []grpc.CallOption, p *peer.Peer) []grpc.CallOption {
	return append(opts[:len(opts):len(opts)], grpc.Peer(p))
}

func (l *connLink) unaryInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	l.begin()
	defer l.end()
	var p peer.Peer
	err := invoker(ctx, method, req, reply, cc, withPeer(opts, &p)...)
	if l.resend(err, &p) {
		l.transport.awaitReady(ctx, cc)
		err = invoker(ctx, method, req, reply, cc, withPeer(opts, &p)...)
	}
	l.transport.report(ctx, l, method, err, &p)
	return refusalError(err, &p)
}

func (l *connLink) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	l.begin()
	var p peer.Peer
	stream, err := streamer(ctx, desc, cc, method, withPeer(opts, &p)...)
	if l.resend(err, &p) {
		l.transport.awaitReady(ctx, cc)
		stream, err = streamer(ctx, desc, cc, method, withPeer(opts, &p)...)
	}
	if err != nil {
		l.end()
		l.transport.report(ctx, l, method, err, &p)
		return nil, refusalError(err, &p)
	}
	s := &reportingStream{ClientStream: stream, link: l, ctx: ctx, method: method, serverStreams: desc.ServerStreams, peer: &p}
	s.stop = context.AfterFunc(ctx, func() { s.finish(nil, false) })
	return s, nil
}

// reportingStream reports the final status of a stream, and ends it on its
// link when it finishes or its context ends.
type reportingStream struct {
	grpc.ClientStream
	link          *connLink
	ctx           context.Context
	method        string
	serverStreams bool
	// peer is the peer of the stream, which gRPC records when it ends.
	peer *peer.Peer
	stop func() bool
	once sync.Once
}

func (s *reportingStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	// A stream with one response ends with it.
	if err != nil || !s.serverStreams {
		s.stop()
		final := err
		if errors.Is(err, io.EOF) {
			final = nil
		}
		s.finish(final, true)
	}
	return refusalError(err, s.peer)
}

func (s *reportingStream) finish(err error, report bool) {
	s.once.Do(func() {
		s.link.end()
		if report {
			s.link.transport.report(s.ctx, s.link, s.method, err, s.peer)
		}
	})
}

// connectionCredentials pick built-in TLS or h2c for each new connection of
// link.
type connectionCredentials struct {
	link *connLink
}

func (c *connectionCredentials) ClientHandshake(ctx context.Context, _ string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	t, address := c.link.transport, c.link.address
	plan, err := t.plan(ctx, address)
	if err != nil {
		return nil, nil, err
	}
	if plan.h2c() {
		c.link.connected(plan)
		t.use(address, plan.choice)
		return h2cCredentials{source: plan.choice.source, tlsError: plan.tlsError}.ClientHandshake(ctx, address, rawConn)
	}
	conn := &handshakeConn{Conn: rawConn}
	tlsConfig := &tls.Config{RootCAs: plan.roots, MinVersion: tls.VersionTLS13}
	if plan.cert != nil {
		tlsConfig.Certificates = []tls.Certificate{*plan.cert}
	}
	if plan.pin != "" {
		// verifyPinnedChain checks the chain against the pin instead.
		tlsConfig.InsecureSkipVerify = true //nolint:gosec
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			return verifyPinnedChain(state.PeerCertificates, plan.pin)
		}
	}
	creds := credentials.NewTLS(tlsConfig)
	// The authority sets the name to verify: built-in TLS checks the name
	// every coordinator certificate carries, not the dialed address.
	tlsConn, info, err := creds.ClientHandshake(ctx, coordinatorCertName, conn)
	if err != nil {
		c.link.handshakeFailed(err)
		t.failed(address, plan, conn, err)
		return nil, nil, err
	}
	c.link.handshakeFailed(nil)
	c.link.connected(plan)
	t.use(address, plan.choice)
	return tlsConn, info, nil
}

func (c *connectionCredentials) ServerHandshake(net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("connection credentials only dial")
}

func (c *connectionCredentials) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: connectionProtocol}
}

func (c *connectionCredentials) Clone() credentials.TransportCredentials {
	clone := *c
	return &clone
}

func (c *connectionCredentials) OverrideServerName(string) error {
	return nil
}

// handshakeConn records the first byte the server sends during a TLS
// handshake.
type handshakeConn struct {
	net.Conn
	received atomic.Bool
	first    atomic.Uint32
}

func (c *handshakeConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && !c.received.Load() {
		c.first.Store(uint32(p[0]))
		c.received.Store(true)
	}
	return n, err
}

// transportLevelFailure reports whether a handshake that failed with err
// ended at the transport level: the connection was reset or closed before
// the server sent anything, or the server's first byte starts neither a TLS
// handshake record nor an alert.
func (c *handshakeConn) transportLevelFailure(err error) bool {
	var netErr net.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) {
		return false
	}
	if !c.received.Load() {
		return true
	}
	first := c.first.Load()
	return first != tlsHandshakeRecord && first != tlsAlertRecord
}

func isUnknownAuthority(err error) bool {
	var unknown x509.UnknownAuthorityError
	return errors.As(err, &unknown)
}

// foreignCoordinatorFormat returns the message for a server certificate that
// failed to verify, judged by its chain: one of another Dagu for a
// coordinator certificate of built-in TLS, and own certificates for any
// other, whatever name it covers. It returns "" when no certificate failed.
func foreignCoordinatorFormat(err error) string {
	verification, ok := errors.AsType[*tls.CertificateVerificationError](err)
	switch {
	case !ok:
		return ""
	case isUnknownAuthority(err) && isBuiltinChain(verification.UnverifiedCertificates):
		return otherDaguFormat
	case isBuiltinChain(verification.UnverifiedCertificates):
		// A coordinator certificate of this Dagu that fails for another
		// reason, such as its expiry, gets the error alone.
		return ""
	}
	return ownCertificatesFormat
}

// h2cCredentials dial h2c, and give each connection an h2cInfo with the
// source that chose h2c.
type h2cCredentials struct {
	source string
	// tlsError is the handshake error of the h2c window that chose h2c.
	tlsError string
}

func (c h2cCredentials) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, _, err := insecure.NewCredentials().ClientHandshake(ctx, authority, rawConn)
	if err != nil {
		return nil, nil, err
	}
	return conn, h2cInfo{CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.NoSecurity}, source: c.source, tlsError: c.tlsError}, nil
}

func (h2cCredentials) ServerHandshake(net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("h2c credentials only dial")
}

func (h2cCredentials) Info() credentials.ProtocolInfo {
	return insecure.NewCredentials().Info()
}

func (c h2cCredentials) Clone() credentials.TransportCredentials {
	return c
}

func (h2cCredentials) OverrideServerName(string) error {
	return nil
}

// h2cInfo is the AuthInfo of a connection on h2c.
type h2cInfo struct {
	credentials.CommonAuthInfo
	source   string
	tlsError string
}

func (h2cInfo) AuthType() string {
	return "insecure"
}

// h2cReasonCredentials send the h2c reason on each call over a connection
// that h2cCredentials dialed, and nothing over TLS.
type h2cReasonCredentials struct{}

func (h2cReasonCredentials) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	request, _ := credentials.RequestInfoFromContext(ctx)
	info, ok := request.AuthInfo.(h2cInfo)
	if !ok {
		return nil, nil
	}
	reason := info.source
	if reason == sourceInsecure {
		reason = h2cReasonInsecure
	}
	return map[string]string{h2cReasonMetadataKey: reason}, nil
}

func (h2cReasonCredentials) RequireTransportSecurity() bool {
	return false
}
