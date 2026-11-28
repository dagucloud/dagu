// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// Each row of Spec 108 §Client Transport, in its order of precedence.
func TestStartTransport(t *testing.T) {
	published := publishedCA(t, newTestCA(t))
	garbage := filepath.Join(t.TempDir(), "peer-ca.crt")
	require.NoError(t, os.WriteFile(garbage, []byte("not a certificate"), 0o600))

	for _, tc := range []struct {
		name    string
		config  Config
		want    string
		wantErr error
	}{
		{name: "InsecureTrue", config: Config{Insecure: true, BuiltinCAFile: "ca.crt", PublishedCAFile: published}, want: "h2c (from peer.insecure: true)"},
		{name: "TLSFiles", config: Config{CertFile: "client.crt", KeyFile: "client.key", PublishedCAFile: published}, want: "TLS (from peer.insecure: false)"},
		{name: "SkipTLSVerify", config: Config{SkipTLSVerify: true}, want: "TLS (from peer.insecure: false)"},
		// TLS files without peer.insecure: false select no TLS.
		{name: "UnsetIgnoresTLSFiles", config: unset(Config{CAFile: "ca.crt"}), want: "h2c (from no TLS source)"},
		{name: "CACertFile", config: unset(Config{BuiltinCAFile: "ca.crt", PublishedCAFile: published}), want: "built-in TLS (from peer.ca_cert_file)"},
		{name: "PublishedCA", config: unset(Config{PublishedCAFile: published}), want: "built-in TLS (from the coordinator's data directory)"},
		{name: "InsecureFalsePublishedCA", config: Config{PublishedCAFile: published}, want: "built-in TLS (from the coordinator's data directory)"},
		{name: "UnreadablePublishedCA", config: unset(Config{PublishedCAFile: garbage}), want: "h2c (from no TLS source)"},
		{name: "NoSource", config: unset(Config{PublishedCAFile: filepath.Join(t.TempDir(), "missing")}), want: "h2c (from no TLS source)"},
		{name: "InsecureFalseWithoutTLS", config: Config{PublishedCAFile: garbage}, wantErr: ErrMissingTLSConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.config.StartTransport()
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, tc.want, got)
			if tc.wantErr != nil {
				assert.ErrorIs(t, tc.config.Validate(), tc.wantErr)
			}
		})
	}
}

// A client that shares the data directory uses built-in TLS for each
// registry entry that reports it, and h2c for the others (Spec 108 §Built-in
// TLS Sources).
func TestClientTransportFromRegistry(t *testing.T) {
	ca := newTestCA(t)
	coord := serveTestCoordinator(t, builtinCredentials(t, ca))

	for _, tc := range []struct {
		name      string
		published bool
		tls       string
		wantTLS   bool
		wantLines []string
	}{
		{name: "BuiltinEntry", published: true, tls: serviceregistry.TLSBuiltin, wantTLS: true},
		{name: "EntryWithoutBuiltin", published: true, wantLines: []string{"Peer transport to " + coord.addr + ": h2c (from no TLS source)"}},
		{name: "NoPublishedCA", tls: serviceregistry.TLSBuiltin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			member := coord.member(tc.tls)
			config := unset(*DefaultConfig())
			if tc.published {
				config.PublishedCAFile = publishedCA(t, ca)
			}
			cli := newTestClient(t, &config, member)

			lines := healthCheck(t, cli, member)
			assert.Equal(t, tc.wantTLS, coord.lastTLS.Load())
			assert.Equal(t, tc.wantLines, transportLines(lines))
		})
	}
}

// A coordinator certificate that does not verify never falls back on an
// address that a registry entry lists, or with peer.ca_cert_file (Spec 108
// §Unlisted Addresses).
func TestClientTransportNoFallbackOnVerification(t *testing.T) {
	coord := serveTestCoordinator(t, builtinCredentials(t, newTestCA(t)))
	other := publishedCA(t, newTestCA(t))

	for name, config := range map[string]Config{
		"ListedAddress": unset(Config{PublishedCAFile: other}),
		"CACertFile":    unset(Config{BuiltinCAFile: other}),
	} {
		t.Run(name, func(t *testing.T) {
			member := coord.member(serviceregistry.TLSBuiltin)
			config.DialTimeout = time.Second
			cli := newTestClient(t, &config, member)
			client, err := cli.getOrCreateDiscoveredClient(member)
			require.NoError(t, err)
			err = cli.isHealthy(t.Context(), client)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "certificate signed by unknown authority")
			assert.Zero(t, coord.h2cCalls.Load())
		})
	}
}

// An address in worker.coordinators that no registry entry lists, whose
// certificate does not chain to the published CA certificate, gets h2c for
// 10 minutes, and built-in TLS again on the next connection after that
// (Spec 108 §Unlisted Addresses).
func TestClientTransportUnlistedAddress(t *testing.T) {
	coord := serveTestCoordinator(t, builtinCredentials(t, newTestCA(t)))
	local := serviceregistry.HostInfo{Host: "127.0.0.1", Port: 1, TLS: serviceregistry.TLSBuiltin}
	config := unset(*DefaultConfig())
	config.PublishedCAFile = publishedCA(t, newTestCA(t))
	cli := newTestClient(t, &config, local)
	clock := fakeClock(cli)
	member := coord.member("")

	lines := healthCheck(t, cli, member)
	assert.Equal(t, []string{"Peer transport to " + coord.addr + ": h2c (from no TLS source)"}, transportLines(lines))
	assert.False(t, coord.lastTLS.Load())
	assert.Equal(t, int32(1), coord.tlsHellos.Load())

	reconnect(t, cli)
	healthCheck(t, cli, member)
	assert.Equal(t, int32(1), coord.tlsHellos.Load(), "h2c within 10 minutes")

	clock.Add(int64(h2cWindowLength))
	reconnect(t, cli)
	healthCheck(t, cli, member)
	assert.Equal(t, int32(2), coord.tlsHellos.Load(), "built-in TLS again after 10 minutes")
	assert.False(t, coord.lastTLS.Load())
}

// A new client keeps working with a coordinator that serves h2c only, such
// as one that a downgrade left behind its published CA certificate: the
// handshake fails at the transport level, and the same call succeeds over
// h2c. When the window ends, the client opens a new connection, which tries
// built-in TLS again (Spec 108 §Fallback to h2c).
func TestClientTransportFallback(t *testing.T) {
	coord := serveTestCoordinator(t, insecure.NewCredentials())
	config := unset(*DefaultConfig())
	config.PublishedCAFile = publishedCA(t, newTestCA(t))
	member := coord.member(serviceregistry.TLSBuiltin)
	cli := newTestClient(t, &config, member)
	clock := fakeClock(cli)

	lines := healthCheck(t, cli, member)
	assert.False(t, coord.lastTLS.Load())
	require.Len(t, lines, 2, lines)
	assert.Equal(t, "WARN", lines[0].Level)
	assert.Equal(t, "Built-in TLS failed on the network path; using h2c for 10 minutes", lines[0].Msg)
	assert.Equal(t, coord.addr, lines[0].Addr)
	assert.NotEmpty(t, lines[0].Error)
	assert.Equal(t, []string{"Peer transport to " + coord.addr + ": h2c (from tls failed on the network path)"}, transportLines(lines))

	clock.Add(int64(h2cWindowLength - time.Second))
	reconnect(t, cli)
	healthCheck(t, cli, member)
	assert.Equal(t, int32(1), coord.tlsHellos.Load(), "h2c within 10 minutes")

	clock.Add(int64(time.Second))
	healthCheck(t, cli, member)
	assert.Equal(t, int32(2), coord.tlsHellos.Load(), "built-in TLS again after 10 minutes")
}

// A client on h2c, as every client before Spec 108 is, still calls a
// coordinator that serves built-in TLS (Spec 108 §One Port).
func TestClientTransportInsecureClient(t *testing.T) {
	coord := serveTestCoordinator(t, builtinCredentials(t, newTestCA(t)))
	config := DefaultConfig()
	member := coord.member(serviceregistry.TLSBuiltin)
	cli := newTestClient(t, config, member)

	assert.Empty(t, transportLines(healthCheck(t, cli, member)))
	assert.False(t, coord.lastTLS.Load())
}

// coordinator.require_tls refuses h2c calls but health checks, and the
// client logs the refusal once (Spec 108 §Require TLS).
func TestClientTransportRequireTLS(t *testing.T) {
	ca := newTestCA(t)
	coord := serveTestCoordinator(t, builtinCredentials(t, ca),
		grpc.ChainUnaryInterceptor(RequireTLSUnaryInterceptor()),
		grpc.ChainStreamInterceptor(RequireTLSStreamInterceptor()),
	)
	member := coord.member(serviceregistry.TLSBuiltin)

	t.Run("H2C", func(t *testing.T) {
		cli := newTestClient(t, DefaultConfig(), member)
		lines := healthCheck(t, cli, member)
		// A stream reports its refusal when it ends.
		lines = append(lines, captureClientLogs(t, func(ctx context.Context) {
			client, err := cli.getOrCreateDiscoveredClient(member)
			require.NoError(t, err)
			stream, err := client.client.StreamLogs(ctx)
			require.NoError(t, err)
			_, err = stream.CloseAndRecv()
			assert.Equal(t, codes.Unauthenticated, status.Code(err))
		})...)
		for range 2 {
			lines = append(lines, captureClientLogs(t, func(ctx context.Context) {
				err := getWorkers(ctx, t, cli, member)
				assert.Equal(t, codes.Unauthenticated, status.Code(err))
				assert.Equal(t, requireTLSMessage, status.Convert(err).Message())
			})...)
		}
		require.Len(t, lines, 1)
		assert.Equal(t, "ERROR", lines[0].Level)
		assert.Equal(t, "The coordinator requires TLS. Upgrade this process, or remove peer.insecure: true.", lines[0].Msg)
	})
	t.Run("BuiltinTLS", func(t *testing.T) {
		config := unset(*DefaultConfig())
		config.PublishedCAFile = publishedCA(t, ca)
		cli := newTestClient(t, &config, member)
		err := getWorkers(t.Context(), t, cli, member)
		assert.Equal(t, codes.Unimplemented, status.Code(err), "the call passes coordinator.require_tls")
	})
}

// A client sends why it uses h2c on each call over h2c, from the same
// choice that picked h2c, and nothing over TLS (Spec 108 §h2c Reasons).
func TestClientTransportH2CReason(t *testing.T) {
	ca := newTestCA(t)
	builtin := serveTestCoordinator(t, builtinCredentials(t, ca))
	plain := serveTestCoordinator(t, insecure.NewCredentials())

	for _, tc := range []struct {
		name      string
		coord     *testCoordinator
		insecure  bool
		published bool
		tls       string
		want      string
	}{
		{name: "InsecureTrue", coord: builtin, insecure: true, published: true, tls: serviceregistry.TLSBuiltin, want: h2cReasonInsecure},
		{name: "NoTLSSource", coord: builtin, tls: serviceregistry.TLSBuiltin, want: h2cReasonNoTLS},
		{name: "EntryWithoutBuiltin", coord: builtin, published: true, want: h2cReasonNoTLS},
		{name: "Fallback", coord: plain, published: true, tls: serviceregistry.TLSBuiltin, want: h2cReasonNetworkPath},
		{name: "BuiltinTLS", coord: builtin, published: true, tls: serviceregistry.TLSBuiltin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := *DefaultConfig()
			if !tc.insecure {
				config = unset(config)
			}
			if tc.published {
				config.PublishedCAFile = publishedCA(t, ca)
			}
			member := tc.coord.member(tc.tls)
			cli := newTestClient(t, &config, member)

			healthCheck(t, cli, member)
			assert.Equal(t, tc.want, tc.coord.lastReason.Load())
		})
	}
}

// testCoordinator serves the health service and an unimplemented coordinator
// service, and records how clients reach it.
type testCoordinator struct {
	addr string
	port int
	// lastTLS reports whether the last call came over TLS.
	lastTLS  atomic.Bool
	h2cCalls atomic.Int32
	// tlsHellos counts the connections that started with a TLS record.
	tlsHellos atomic.Int32
	// lastReason is the h2c reason the last call carried.
	lastReason atomic.Value
}

func serveTestCoordinator(t *testing.T, creds credentials.TransportCredentials, opts ...grpc.ServerOption) *testCoordinator {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	coord := &testCoordinator{addr: listener.Addr().String(), port: listener.Addr().(*net.TCPAddr).Port}
	record := func(ctx context.Context) {
		p, _ := peer.FromContext(ctx)
		_, isTLS := p.AuthInfo.(credentials.TLSInfo)
		coord.lastTLS.Store(isTLS)
		md, _ := metadata.FromIncomingContext(ctx)
		coord.lastReason.Store(strings.Join(md.Get(h2cReasonMetadataKey), ","))
		if !isTLS {
			coord.h2cCalls.Add(1)
		}
	}
	server := grpc.NewServer(append([]grpc.ServerOption{
		grpc.Creds(creds),
		grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			record(ctx)
			return handler(ctx, req)
		}),
	}, opts...)...)
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())
	coordinatorv1.RegisterCoordinatorServiceServer(server, coordinatorv1.UnimplementedCoordinatorServiceServer{})
	go func() { _ = server.Serve(&helloCountingListener{Listener: listener, hellos: &coord.tlsHellos}) }()
	t.Cleanup(server.Stop)
	return coord
}

// member returns the registry entry of the coordinator with tls.
func (c *testCoordinator) member(tls string) serviceregistry.HostInfo {
	return serviceregistry.HostInfo{ID: "coord-" + strconv.Itoa(c.port), Host: "127.0.0.1", Port: c.port, TLS: tls}
}

// helloCountingListener counts the connections that start with a TLS
// record.
type helloCountingListener struct {
	net.Listener
	hellos *atomic.Int32
}

func (l *helloCountingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &helloCountingConn{Conn: conn, hellos: l.hellos}, nil
}

type helloCountingConn struct {
	net.Conn
	hellos *atomic.Int32
	read   bool
}

func (c *helloCountingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && !c.read {
		c.read = true
		if p[0] == tlsHandshakeRecord {
			c.hellos.Add(1)
		}
	}
	return n, err
}

func newTestCA(t *testing.T) *PeerCA {
	t.Helper()
	ca, err := LoadOrCreatePeerCA(filepath.Join(t.TempDir(), "peer-ca.pem"))
	require.NoError(t, err)
	return ca
}

func publishedCA(t *testing.T, ca *PeerCA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peer-ca.crt")
	publish(t, ca, path)
	return path
}

func builtinCredentials(t *testing.T, ca *PeerCA) credentials.TransportCredentials {
	t.Helper()
	creds, err := NewPeerTransportCredentials(t.Context(), ca)
	require.NoError(t, err)
	return creds
}

// unset returns config with peer.insecure unset.
func unset(config Config) Config {
	config.Insecure = true
	config.InsecureUnset = true
	return config
}

// newTestClient returns a client whose local registry lists members. The
// client keeps its h2c windows in a record of its own unless config names
// one.
func newTestClient(t *testing.T, config *Config, members ...serviceregistry.HostInfo) *clientImpl {
	t.Helper()
	registry := &mutableRegistry{members: members}
	config.LocalRegistry = registry
	if config.FallbackFile == "" {
		config.FallbackFile = filepath.Join(t.TempDir(), "peer-h2c.json")
	}
	require.NoError(t, config.Validate())
	cli := New(registry, config).(*clientImpl)
	t.Cleanup(func() { _ = cli.Cleanup(context.Background()) })
	return cli
}

// fakeClock makes cli read the time from the returned nanoseconds.
func fakeClock(cli *clientImpl) *atomic.Int64 {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	cli.transport.now = func() time.Time { return time.Unix(0, clock.Load()) }
	return &clock
}

// reconnect closes the client's connections, so the next call opens one.
func reconnect(t *testing.T, cli *clientImpl) {
	t.Helper()
	require.NoError(t, cli.Cleanup(t.Context()))
}

// healthCheck checks member's health through cli and returns what cli logs.
func healthCheck(t *testing.T, cli *clientImpl, member serviceregistry.HostInfo) []clientLogLine {
	t.Helper()
	return captureClientLogs(t, func(ctx context.Context) {
		client, err := cli.getOrCreateDiscoveredClient(member)
		require.NoError(t, err)
		require.NoError(t, cli.isHealthy(ctx, client))
	})
}

func getWorkers(ctx context.Context, t *testing.T, cli *clientImpl, member serviceregistry.HostInfo) error {
	t.Helper()
	client, err := cli.getOrCreateDiscoveredClient(member)
	require.NoError(t, err)
	_, err = client.client.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
	return err
}

// clientLogLine is one line the client logs.
type clientLogLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Addr  string `json:"addr"`
	Error string `json:"err"`
}

// captureClientLogs returns what fn logs at level info and above.
func captureClientLogs(t *testing.T, fn func(context.Context)) []clientLogLine {
	t.Helper()
	var out bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(logger.WithQuiet(), logger.WithFormat("json"), logger.WithWriter(&out)))
	fn(ctx)
	var lines []clientLogLine
	for line := range strings.Lines(out.String()) {
		var record clientLogLine
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		lines = append(lines, record)
	}
	return lines
}

// transportLines returns the per-coordinator lines among lines.
func transportLines(lines []clientLogLine) []string {
	var transport []string
	for _, line := range lines {
		if strings.HasPrefix(line.Msg, "Peer transport to ") {
			transport = append(transport, line.Msg)
		}
	}
	return transport
}
