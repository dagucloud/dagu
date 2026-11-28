// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// A call that reached the coordinator is never sent again. Here its TLS
// connection drains while the coordinator restarts behind a proxy, and a
// health check meanwhile opens a connection that the proxy accepts and
// closes, which falls back to h2c (Spec 108 §Fallback to h2c).
func TestClientTransportNoResendAfterTransport(t *testing.T) {
	ca := newTestCA(t)
	var applied atomic.Int32
	entered := make(chan struct{}, 1)
	tlsConns, h2cConns := make(chan net.Conn, 4), make(chan net.Conn, 4)
	tlsServer := serveCountingCoordinator(t, builtinCredentials(t, ca), tlsConns, &applied, entered)
	serveCountingCoordinator(t, insecure.NewCredentials(), h2cConns, &applied, nil)
	addr := routeConnections(t, func(n int32, conn net.Conn) {
		switch n {
		case 1:
			tlsConns <- conn
		case 2:
			_ = conn.Close()
		default:
			h2cConns <- conn
		}
	})
	member := hostInfo(addr, serviceregistry.TLSBuiltin)
	config := unset(*DefaultConfig())
	config.PublishedCAFile = publishedCA(t, ca)
	cli := newTestClient(t, &config, member)
	client, err := cli.getOrCreateDiscoveredClient(member)
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, err := client.client.GetWorkers(context.Background(), &coordinatorv1.GetWorkersRequest{})
		result <- err
	}()
	<-entered
	go tlsServer.GracefulStop()
	require.Eventually(t, func() bool { return client.conn.GetState() != connectivity.Ready }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, cli.isHealthy(t.Context(), client))
	tlsServer.Stop()

	assert.Error(t, <-result)
	assert.Equal(t, int32(1), applied.Load(), "the call ran on the coordinator once")
}

// Under coordinator.require_tls, a client that fell back to h2c after one
// reset tries built-in TLS again after the first refusal, before its window
// ends (Spec 108 §Require TLS).
func TestClientTransportRefusalRedials(t *testing.T) {
	ca := newTestCA(t)
	conns := make(chan net.Conn, 4)
	var applied atomic.Int32
	serveCountingCoordinator(t, builtinCredentials(t, ca), conns, &applied, nil,
		grpc.ChainUnaryInterceptor(RequireTLSUnaryInterceptor()),
		grpc.ChainStreamInterceptor(RequireTLSStreamInterceptor()),
	)
	addr := routeConnections(t, func(n int32, conn net.Conn) {
		if n == 1 {
			_ = conn.Close()
			return
		}
		conns <- conn
	})
	member := hostInfo(addr, serviceregistry.TLSBuiltin)
	config := unset(*DefaultConfig())
	config.PublishedCAFile = publishedCA(t, ca)
	cli := newTestClient(t, &config, member)

	healthCheck(t, cli, member)
	assert.Error(t, getWorkers(t.Context(), t, cli, member), "refused over h2c")
	assert.NoError(t, getWorkers(t.Context(), t, cli, member), "built-in TLS on the next connection")
	assert.Equal(t, int32(1), applied.Load())
}

// A connection on h2c without a TLS source, such as one a worker opens
// before its coordinator registers, moves to built-in TLS once the registry
// reports it (Spec 108 §Built-in TLS Sources).
func TestClientTransportRegistryRedials(t *testing.T) {
	ca := newTestCA(t)
	coord := serveTestCoordinator(t, builtinCredentials(t, ca))
	member := coord.member(serviceregistry.TLSBuiltin)
	config := unset(*DefaultConfig())
	config.PublishedCAFile = publishedCA(t, ca)
	cli := newTestClient(t, &config)
	clock := fakeClock(cli)

	healthCheck(t, cli, member)
	assert.False(t, coord.lastTLS.Load(), "no coordinator registered")

	cli.config.LocalRegistry.(*mutableRegistry).set(member)
	healthCheck(t, cli, member)
	assert.False(t, coord.lastTLS.Load(), "checked at most every replanInterval")

	clock.Add(int64(replanInterval))
	lines := healthCheck(t, cli, member)
	assert.True(t, coord.lastTLS.Load())
	assert.Equal(t, []string{"Peer transport to " + coord.addr + ": built-in TLS (from the coordinator's data directory)"}, transportLines(lines))
}

// A caller that took a connection just before its replacement still
// finishes its call on it. The replaced connection closes once no call has
// used it for retireGrace.
func TestClientTransportRetiredConnectionGrace(t *testing.T) {
	ca := newTestCA(t)
	coord := serveTestCoordinator(t, builtinCredentials(t, ca))
	member := coord.member(serviceregistry.TLSBuiltin)
	config := unset(*DefaultConfig())
	config.PublishedCAFile = publishedCA(t, ca)
	cli := newTestClient(t, &config)
	clock := fakeClock(cli)
	healthCheck(t, cli, member)

	taken, err := cli.getOrCreateDiscoveredClient(member)
	require.NoError(t, err)
	cli.config.LocalRegistry.(*mutableRegistry).set(member)
	clock.Add(int64(replanInterval))
	replacement, err := cli.getOrCreateDiscoveredClient(member)
	require.NoError(t, err)
	require.NotSame(t, taken, replacement)

	require.NoError(t, cli.isHealthy(t.Context(), taken), "the taken connection still serves")
	clock.Add(int64(retireGrace))
	require.NoError(t, cli.isHealthy(t.Context(), replacement))
	assert.Equal(t, connectivity.Shutdown, taken.conn.GetState())
}

// A stream in flight on a replaced connection finishes on it, and Cleanup
// closes that connection with the current ones, also while a stream is
// still in flight on it.
func TestClientTransportCleanupClosesRetired(t *testing.T) {
	ca := newTestCA(t)
	coord := serveTestCoordinator(t, builtinCredentials(t, ca))
	member := coord.member(serviceregistry.TLSBuiltin)
	config := unset(*DefaultConfig())
	config.PublishedCAFile = publishedCA(t, ca)
	cli := newTestClient(t, &config)
	clock := fakeClock(cli)

	taken, err := cli.getOrCreateDiscoveredClient(member)
	require.NoError(t, err)
	stream, err := taken.client.StreamLogs(t.Context())
	require.NoError(t, err)
	cli.config.LocalRegistry.(*mutableRegistry).set(member)
	clock.Add(int64(replanInterval))
	replacement, err := cli.getOrCreateDiscoveredClient(member)
	require.NoError(t, err)
	require.NotSame(t, taken, replacement)

	_, err = stream.CloseAndRecv()
	assert.Equal(t, codes.Unimplemented, status.Code(err), "the stream finished on its connection")
	assert.NotEqual(t, connectivity.Shutdown, taken.conn.GetState())

	// A second stream stays in flight on the replaced connection: the
	// client has not received its end.
	inFlight, err := taken.client.StreamLogs(t.Context())
	require.NoError(t, err)
	require.NoError(t, cli.Cleanup(t.Context()))
	assert.Equal(t, connectivity.Shutdown, taken.conn.GetState())
	_, _ = inFlight.CloseAndRecv()
}

// peer.insecure: false never uses h2c: a connection without a TLS source
// fails, and a transport-level failure does not fall back (Spec 108 §Client
// Transport).
func TestClientTransportInsecureFalse(t *testing.T) {
	ca := newTestCA(t)
	for _, tc := range []struct {
		name  string
		creds credentials.TransportCredentials
		tls   string
	}{
		{name: "NoTLSSource", creds: builtinCredentials(t, ca)},
		{name: "TransportFailure", creds: insecure.NewCredentials(), tls: serviceregistry.TLSBuiltin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coord := serveTestCoordinator(t, tc.creds)
			member := coord.member(tc.tls)
			config := *DefaultConfig()
			config.Insecure = false
			config.PublishedCAFile = publishedCA(t, ca)
			cli := newTestClient(t, &config, member)

			client, err := cli.getOrCreateDiscoveredClient(member)
			require.NoError(t, err)
			assert.Error(t, cli.isHealthy(t.Context(), client))
			assert.Zero(t, coord.h2cCalls.Load())
			_, open := cli.transport.windows.active(coord.addr, time.Now())
			assert.False(t, open)
		})
	}
}

// The clients of a process, and later processes on the host, share an h2c
// window: only the first pays the failed handshake and logs the warning.
func TestClientTransportSharedWindow(t *testing.T) {
	coord := serveTestCoordinator(t, insecure.NewCredentials())
	member := coord.member(serviceregistry.TLSBuiltin)
	record := filepath.Join(t.TempDir(), "peer-h2c.json")
	newClient := func() *clientImpl {
		config := unset(*DefaultConfig())
		config.PublishedCAFile = publishedCA(t, newTestCA(t))
		config.FallbackFile = record
		return newTestClient(t, &config, member)
	}

	first := healthCheck(t, newClient(), member)
	assert.Equal(t, "WARN", first[0].Level)
	second := healthCheck(t, newClient(), member)
	assert.Equal(t, []string{"Peer transport to " + coord.addr + ": h2c (from tls failed on the network path)"}, transportLines(second))
	assert.Len(t, second, 1, "no warning")
	assert.Equal(t, int32(1), coord.tlsHellos.Load())

	host, _ := os.Hostname()
	later := newH2CWindows(record, host)
	window, open := later.active(coord.addr, time.Now())
	assert.True(t, open, "a later process reads the window")
	assert.Equal(t, sourceNetworkPath, window.Source)
}

// serveCountingCoordinator serves the connections sent on conns, counting in
// applied the GetWorkers calls it runs. With entered, each call signals it
// and blocks until its connection ends.
func serveCountingCoordinator(t *testing.T, creds credentials.TransportCredentials, conns chan net.Conn, applied *atomic.Int32, entered chan struct{}, opts ...grpc.ServerOption) *grpc.Server {
	t.Helper()
	server := grpc.NewServer(append([]grpc.ServerOption{grpc.Creds(creds)}, opts...)...)
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())
	coordinatorv1.RegisterCoordinatorServiceServer(server, &countingCoordinator{applied: applied, entered: entered})
	listener := &chanListener{conns: conns, done: make(chan struct{})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return server
}

// routeConnections accepts connections on a loopback address, which it
// returns, and hands the nth one to route.
func routeConnections(t *testing.T, route func(n int32, conn net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	var n atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			route(n.Add(1), conn)
		}
	}()
	return listener.Addr().String()
}

func hostInfo(addr, tls string) serviceregistry.HostInfo {
	host, port, _ := net.SplitHostPort(addr)
	member := serviceregistry.HostInfo{ID: "coord-" + port, Host: host, TLS: tls}
	member.Port, _ = net.LookupPort("tcp", port)
	return member
}

type countingCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	applied *atomic.Int32
	entered chan struct{}
}

func (c *countingCoordinator) GetWorkers(ctx context.Context, _ *coordinatorv1.GetWorkersRequest) (*coordinatorv1.GetWorkersResponse, error) {
	c.applied.Add(1)
	if c.entered == nil {
		return &coordinatorv1.GetWorkersResponse{}, nil
	}
	c.entered <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

// chanListener accepts the connections sent on conns.
type chanListener struct {
	conns chan net.Conn
	once  sync.Once
	done  chan struct{}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

// mutableRegistry lists the members set last.
type mutableRegistry struct {
	mu      sync.Mutex
	members []serviceregistry.HostInfo
}

func (r *mutableRegistry) set(members ...serviceregistry.HostInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.members = members
}

func (r *mutableRegistry) Register(context.Context, serviceregistry.ServiceName, serviceregistry.HostInfo) error {
	return nil
}

func (r *mutableRegistry) Unregister(context.Context) {}

func (r *mutableRegistry) GetServiceMembers(context.Context, serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]serviceregistry.HostInfo(nil), r.members...), nil
}

func (r *mutableRegistry) UpdateStatus(context.Context, serviceregistry.ServiceName, serviceregistry.ServiceStatus) error {
	return nil
}
