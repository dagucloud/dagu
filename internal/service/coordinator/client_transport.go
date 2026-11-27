// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

const (
	// h2cWindow is how long a client keeps h2c for a coordinator address
	// after built-in TLS failed there, before it tries built-in TLS again.
	h2cWindow = 10 * time.Minute
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
	sourceCACertFile  = "peer.ca_cert_file"
	sourceDataDir     = "the coordinator's data directory"
	sourceNoTLS       = "no TLS source"
	sourceNetworkPath = "tls failed on the network path"
)

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
	switch {
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

func (c *Config) hasTLSSettings() bool {
	return c.CertFile != "" || c.KeyFile != "" || c.CAFile != "" || c.SkipTLSVerify
}

// picksTransportPerConnection reports whether the client picks built-in TLS
// or h2c for each connection, rows 3 to 5 of Spec 108 §Client Transport.
func (c *Config) picksTransportPerConnection() bool {
	return c.InsecureUnset || (!c.Insecure && !c.hasTLSSettings())
}

func (c *Config) canReadPublishedCA() bool {
	if c.PublishedCAFile == "" {
		return false
	}
	_, err := readCertPool(c.PublishedCAFile)
	return err == nil
}

// clientTransport picks the transport of each connection to a coordinator
// for a client that picks it per connection, and keeps, per address, the h2c
// that a failed built-in TLS handshake selects. Its interceptors log what it
// has to report with the context of the next call.
type clientTransport struct {
	config *Config
	// start is the client's start transport. The transport to an address
	// is logged when it differs from start, and again each time it changes.
	start transportChoice
	now   func() time.Time

	mu        sync.Mutex
	fallbacks map[string]h2cFallback
	// moves counts, per address, the fallbacks to h2c, so a call can tell
	// that its connection moved to h2c.
	moves   map[string]uint64
	current map[string]transportChoice
	refused map[string]bool
	pending []func(context.Context)
}

// h2cFallback is the h2c that a failed built-in TLS handshake selects for an
// address until a time.
type h2cFallback struct {
	until  time.Time
	source string
	err    error
}

func newClientTransport(config *Config) *clientTransport {
	start, _ := config.startTransport()
	return &clientTransport{
		config:    config,
		start:     start,
		now:       time.Now,
		fallbacks: make(map[string]h2cFallback),
		moves:     make(map[string]uint64),
		current:   make(map[string]transportChoice),
		refused:   make(map[string]bool),
	}
}

// dialOptions returns the options that add the transport's interceptors to
// a connection to address and, for a client that picks the transport per
// connection, its credentials.
func (t *clientTransport) dialOptions(address string) []grpc.DialOption {
	opts := []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(t.unaryInterceptor(address)),
		grpc.WithChainStreamInterceptor(t.streamInterceptor(address)),
	}
	if t.config.picksTransportPerConnection() {
		opts = append(opts, grpc.WithTransportCredentials(&connectionCredentials{transport: t, address: address}))
	}
	return opts
}

// connectionPlan is the transport of one connection: built-in TLS that
// verifies against roots, or h2c when roots is nil.
type connectionPlan struct {
	choice transportChoice
	roots  *x509.CertPool
	// unlisted reports an address that no live registry entry lists, with
	// the published CA certificate as the only TLS source.
	unlisted bool
}

// plan picks the transport of a new connection to address.
func (t *clientTransport) plan(ctx context.Context, address string) (connectionPlan, error) {
	if fallback, ok := t.activeFallback(address); ok {
		return connectionPlan{choice: transportChoice{transportH2C, fallback.source}}, nil
	}
	if file := t.config.BuiltinCAFile; file != "" {
		roots, err := readCertPool(file)
		if err != nil {
			return connectionPlan{}, fmt.Errorf("peer.ca_cert_file: %w", err)
		}
		return connectionPlan{choice: transportChoice{transportBuiltinTLS, sourceCACertFile}, roots: roots}, nil
	}
	noSource := connectionPlan{choice: transportChoice{transportH2C, sourceNoTLS}}
	if t.config.PublishedCAFile == "" {
		return noSource, nil
	}
	roots, err := readCertPool(t.config.PublishedCAFile)
	if err != nil {
		return noSource, nil
	}
	listed, builtin := t.builtinTLSAt(ctx, address)
	if !builtin {
		return noSource, nil
	}
	return connectionPlan{
		choice:   transportChoice{transportBuiltinTLS, sourceDataDir},
		roots:    roots,
		unlisted: !listed,
	}, nil
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

func (t *clientTransport) activeFallback(address string) (h2cFallback, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fallback, ok := t.fallbacks[address]
	if ok && !t.now().Before(fallback.until) {
		delete(t.fallbacks, address)
		return h2cFallback{}, false
	}
	return fallback, ok
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
// A transport-level failure, or a certificate that does not chain to the
// published CA certificate on an unlisted address, selects h2c for the
// address for h2cWindow.
func (t *clientTransport) failed(address string, plan connectionPlan, conn *handshakeConn, err error) {
	var source string
	switch {
	case conn.transportLevelFailure(err):
		source = sourceNetworkPath
	case plan.unlisted && isUnknownAuthority(err):
		source = sourceNoTLS
	default:
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fallbacks[address] = h2cFallback{until: t.now().Add(h2cWindow), source: source, err: err}
	t.moves[address]++
	if source == sourceNetworkPath {
		t.pending = append(t.pending, func(ctx context.Context) {
			logger.Warn(ctx, "Built-in TLS failed on the network path; using h2c for 10 minutes",
				tag.Addr(address), tag.Error(err))
		})
	}
}

func (t *clientTransport) moveCount(address string) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.moves[address]
}

// movedToH2C reports whether a call to address failed because its
// connection moved to h2c after the call read moves.
func (t *clientTransport) movedToH2C(address string, moves uint64, err error) bool {
	return status.Code(err) == codes.Unavailable && t.moveCount(address) != moves
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

// unaryInterceptor retries, once, a call whose connection moved to h2c, and
// then reports what the transport logged.
func (t *clientTransport) unaryInterceptor(address string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		moves := t.moveCount(address)
		err := invoker(ctx, method, req, reply, cc, opts...)
		if t.movedToH2C(address, moves, err) {
			t.awaitReady(ctx, cc)
			err = invoker(ctx, method, req, reply, cc, opts...)
		}
		t.report(ctx, address, method, err)
		return err
	}
}

// streamInterceptor retries, once, opening a stream whose connection moved
// to h2c, and then reports what the transport logged.
func (t *clientTransport) streamInterceptor(address string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		moves := t.moveCount(address)
		stream, err := streamer(ctx, desc, cc, method, opts...)
		if t.movedToH2C(address, moves, err) {
			t.awaitReady(ctx, cc)
			stream, err = streamer(ctx, desc, cc, method, opts...)
		}
		t.report(ctx, address, method, err)
		return stream, err
	}
}

// report logs, with ctx, what the transport has to report after a call of
// method to address that ended with err, and the refusal of
// coordinator.require_tls once until a call other than a health check
// succeeds.
func (t *clientTransport) report(ctx context.Context, address, method string, err error) {
	refusal := isRequireTLSRefusal(err)
	t.mu.Lock()
	pending := t.pending
	t.pending = nil
	firstRefusal := refusal && !t.refused[address]
	switch {
	case refusal:
		t.refused[address] = true
	case err == nil && !strings.HasPrefix(method, grpcHealthMethodPrefix):
		delete(t.refused, address)
	}
	fallback, fellBack := t.fallbacks[address]
	t.mu.Unlock()

	for _, log := range pending {
		log(ctx)
	}
	if !firstRefusal {
		return
	}
	attrs := []slog.Attr{tag.Addr(address)}
	if fellBack && fallback.source == sourceNetworkPath {
		attrs = append(attrs, slog.String("tls-error", fallback.err.Error()))
	}
	logger.Error(ctx, "The coordinator requires TLS. Upgrade this process, or remove peer.insecure: true.", attrs...)
}

func isRequireTLSRefusal(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Unauthenticated && st.Message() == requireTLSMessage
}

// connectionCredentials pick built-in TLS or h2c for each new connection to
// address.
type connectionCredentials struct {
	transport *clientTransport
	address   string
}

func (c *connectionCredentials) ClientHandshake(ctx context.Context, _ string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	plan, err := c.transport.plan(ctx, c.address)
	if err != nil {
		return nil, nil, err
	}
	if plan.roots == nil {
		c.transport.use(c.address, plan.choice)
		return insecure.NewCredentials().ClientHandshake(ctx, c.address, rawConn)
	}
	conn := &handshakeConn{Conn: rawConn}
	creds := credentials.NewTLS(&tls.Config{RootCAs: plan.roots, MinVersion: tls.VersionTLS13})
	// The authority sets the name to verify: built-in TLS checks the name
	// every coordinator certificate carries, not the dialed address.
	tlsConn, info, err := creds.ClientHandshake(ctx, coordinatorCertName, conn)
	if err != nil {
		c.transport.failed(c.address, plan, conn, err)
		return nil, nil, err
	}
	c.transport.use(c.address, plan.choice)
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
	first := byte(c.first.Load())
	return first != tlsHandshakeRecord && first != tlsAlertRecord
}

func isUnknownAuthority(err error) bool {
	var unknown x509.UnknownAuthorityError
	return errors.As(err, &unknown)
}
