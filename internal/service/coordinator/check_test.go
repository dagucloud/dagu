// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"bufio"
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
)

var loopback = []net.IP{net.IPv4(127, 0, 0, 1)}

func TestCheckH2C(t *testing.T) {
	t.Parallel()

	addr := startCheckServer(t)
	config := coordinator.DefaultConfig()
	config.Token = "client-token"

	result := coordinator.Check(t.Context(), config, checkMember(t, addr))

	assert.True(t, result.Passed(), "%+v", result)
	assert.Nil(t, result.TLS)
	assert.Empty(t, result.Proxy)
	assert.Equal(t, coordinator.PeerTokenConfigured, result.Credential.PeerToken)
}

func TestCheckUnreachable(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	result := coordinator.Check(t.Context(), coordinator.DefaultConfig(), checkMember(t, addr))

	assert.False(t, result.Passed())
	assert.Error(t, result.DialErr)
	assert.Equal(t, addr, result.Address)
}

func TestCheckTokenRefused(t *testing.T) {
	t.Parallel()

	auth := coordinator.NewPeerTokenAuth("coordinator-token", true, true)
	addr := startCheckServer(t, grpc.ChainUnaryInterceptor(auth.UnaryInterceptor()))
	config := coordinator.DefaultConfig()
	config.Token = "other-token"

	result := coordinator.Check(t.Context(), config, checkMember(t, addr))

	assert.False(t, result.Passed())
	assert.ErrorContains(t, result.AnswerErr, "serving, but refused the call, the peer token does not match")
}

// An h2c client on a port that serves only TLS learns that, instead of a
// transport error. A TLS 1.2 server that requires a client certificate
// answers the probe with an alert, which still shows TLS.
func TestCheckH2CToTLSOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certFile, keyFile := peercert.Write(t, dir, peercert.Options{Name: "server"})
	clientCA, _ := peercert.Write(t, dir, peercert.Options{Name: "client"})
	for _, tc := range []struct {
		name   string
		server grpc.ServerOption
	}{
		{name: "TLS", server: tlsCreds(t, certFile, keyFile)},
		{name: "TLS12ClientCertRequired", server: mutualTLSCreds(t, certFile, keyFile, clientCA, tls.RequireAndVerifyClientCert, tls.VersionTLS12)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := startCheckServer(t, tc.server)

			result := coordinator.Check(t.Context(), coordinator.DefaultConfig(), checkMember(t, addr))

			assert.False(t, result.Passed())
			assert.ErrorContains(t, result.AnswerErr, "serves only TLS on this port")
		})
	}
}

// A failed handshake names what the certificate covers and the CA it
// chains to, whichever check fails.
func TestCheckTLS(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	covering, coveringKey := peercert.Write(t, dir, peercert.Options{Name: "covering", IPAddresses: loopback})
	named, namedKey := peercert.Write(t, dir, peercert.Options{Name: "named", DNSNames: []string{"coordinator.test"}})
	ipAsDNS, ipAsDNSKey := peercert.Write(t, dir, peercert.Options{Name: "ip-as-dns", DNSNames: []string{"127.0.0.1"}})
	clientOnly, clientOnlyKey := peercert.Write(t, dir, peercert.Options{
		Name: "client-only", IPAddresses: loopback, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	other, _ := peercert.Write(t, dir, peercert.Options{Name: "other", IPAddresses: loopback})

	for _, tc := range []struct {
		name         string
		certFile     string
		keyFile      string
		caFile       string
		skipVerify   bool
		wantErr      string
		wantVerified bool
		wantDNS      []string
		wantIPs      []string
		wantCA       string
	}{
		{name: "Passes", certFile: covering, keyFile: coveringKey, caFile: covering,
			wantVerified: true, wantIPs: []string{"127.0.0.1"}, wantCA: "covering"},
		{name: "SkipVerify", certFile: covering, keyFile: coveringKey, skipVerify: true,
			wantIPs: []string{"127.0.0.1"}, wantCA: "covering"},
		{name: "NameNotCovered", certFile: named, keyFile: namedKey, caFile: named,
			wantErr: "the certificate does not cover 127.0.0.1", wantDNS: []string{"coordinator.test"}, wantCA: "named"},
		{name: "IPAsDNSName", certFile: ipAsDNS, keyFile: ipAsDNSKey, caFile: ipAsDNS,
			wantErr: "the certificate lists 127.0.0.1 as a DNS name, not as an IP address", wantDNS: []string{"127.0.0.1"}, wantCA: "ip-as-dns"},
		{name: "OtherCA", certFile: covering, keyFile: coveringKey, caFile: other,
			wantErr: "the certificate does not chain to a CA this client trusts", wantIPs: []string{"127.0.0.1"}, wantCA: "covering"},
		{name: "NoServerAuth", certFile: clientOnly, keyFile: clientOnlyKey, caFile: clientOnly,
			wantErr: "the certificate does not allow server authentication", wantIPs: []string{"127.0.0.1"}, wantCA: "client-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := startCheckServer(t, tlsCreds(t, tc.certFile, tc.keyFile))
			config := coordinator.DefaultConfig()
			config.Insecure = false
			config.CAFile = tc.caFile
			config.SkipTLSVerify = tc.skipVerify

			result := coordinator.Check(t.Context(), config, checkMember(t, addr))

			require.NotNil(t, result.TLS)
			if tc.wantErr == "" {
				assert.True(t, result.Passed(), "%+v", result)
			} else {
				assert.False(t, result.Passed())
				assert.EqualError(t, result.TLS.Err, tc.wantErr)
			}
			assert.Equal(t, tc.wantVerified, result.TLS.Verified)
			require.NotNil(t, result.TLS.Certificate)
			assert.Equal(t, tc.wantDNS, result.TLS.Certificate.DNSNames)
			assert.Equal(t, emptyIfNil(tc.wantIPs), result.TLS.Certificate.IPAddresses)
			assert.Equal(t, tc.wantCA, result.TLS.Certificate.CA)
			assert.True(t, result.TLS.Certificate.SelfSigned)
		})
	}
}

// A coordinator that requires a client certificate refuses one it did not
// sign, or none. TLS 1.3 refuses after the client side of the handshake
// ends, and TLS 1.2 during it; both read as a failed handshake. One that
// only asks for a certificate from its CA gets none from a worker whose
// certificate another CA signed, as the worker sends none.
func TestCheckClientCertificate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	serverCert, serverKey := peercert.Write(t, dir, peercert.Options{Name: "server", IPAddresses: loopback})
	clientCert, clientKey := peercert.Write(t, dir, peercert.Options{Name: "client"})
	otherCert, otherKey := peercert.Write(t, dir, peercert.Options{Name: "other-client"})

	for _, tc := range []struct {
		name       string
		clientAuth tls.ClientAuthType
		maxVersion uint16
		certFile   string
		keyFile    string
		wantErr    string
	}{
		{name: "Accepted", certFile: clientCert, keyFile: clientKey},
		{name: "None",
			wantErr: "the coordinator requires a client certificate, and the worker sends none"},
		{name: "OtherCA", certFile: otherCert, keyFile: otherKey,
			wantErr: "the coordinator refused the client certificate " + otherCert},
		{name: "NoneTLS12", maxVersion: tls.VersionTLS12,
			wantErr: "the coordinator requires a client certificate, and the worker sends none"},
		{name: "OptionalOtherCA", clientAuth: tls.VerifyClientCertIfGiven, certFile: otherCert, keyFile: otherKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clientAuth := cmp.Or(tc.clientAuth, tls.RequireAndVerifyClientCert)
			maxVersion := cmp.Or(tc.maxVersion, tls.VersionTLS13)
			addr := startCheckServer(t, mutualTLSCreds(t, serverCert, serverKey, clientCert, clientAuth, maxVersion))
			config := coordinator.DefaultConfig()
			config.Insecure = false
			config.CAFile = serverCert
			config.CertFile = tc.certFile
			config.KeyFile = tc.keyFile

			result := coordinator.Check(t.Context(), config, checkMember(t, addr))

			require.NotNil(t, result.TLS)
			if tc.wantErr == "" {
				assert.True(t, result.Passed(), "%+v", result)
				return
			}
			assert.False(t, result.Passed())
			assert.ErrorContains(t, result.TLS.Err, tc.wantErr)
			require.NotNil(t, result.TLS.Certificate)
			assert.Equal(t, "server", result.TLS.Certificate.CA)
		})
	}
}

// A coordinator that accepts the connection and never answers gets a plain
// timeout on both transports.
func TestCheckNoAnswer(t *testing.T) {
	t.Parallel()

	addr := startSilentListener(t)
	for _, tc := range []struct {
		name     string
		insecure bool
	}{
		{name: "H2C", insecure: true},
		{name: "TLS", insecure: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			config := coordinator.DefaultConfig()
			config.Insecure = tc.insecure
			config.SkipTLSVerify = !tc.insecure
			config.DialTimeout = 300 * time.Millisecond

			result := coordinator.Check(t.Context(), config, checkMember(t, addr))

			assert.False(t, result.Passed())
			want := "the coordinator did not answer within 300ms"
			if tc.insecure {
				assert.EqualError(t, result.AnswerErr, want)
			} else {
				require.NotNil(t, result.TLS)
				assert.EqualError(t, result.TLS.Err, want)
			}
		})
	}
}

// With an HTTPS proxy in the environment, the check reaches the coordinator
// through it, as gRPC does. Not parallel: it replaces the proxy selection.
func TestCheckThroughProxy(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := peercert.Write(t, dir, peercert.Options{Name: "server", IPAddresses: loopback})
	addr := startCheckServer(t, tlsCreds(t, certFile, keyFile))
	proxy := startConnectProxy(t)
	coordinator.SetCheckProxyForTest(t, &url.URL{Scheme: "http", Host: proxy.addr})
	config := coordinator.DefaultConfig()
	config.Insecure = false
	config.CAFile = certFile

	result := coordinator.Check(t.Context(), config, checkMember(t, addr))

	assert.True(t, result.Passed(), "%+v", result)
	assert.Equal(t, proxy.addr, result.Proxy)
	assert.Contains(t, proxy.targets(), addr)
}

// A proxy URL without a port is dialed on 443, as gRPC dials it. Not
// parallel: it replaces the proxy selection.
func TestCheckProxyDefaultPort(t *testing.T) {
	coordinator.SetCheckProxyForTest(t, &url.URL{Scheme: "http", Host: "127.0.0.1"})
	config := coordinator.DefaultConfig()
	config.DialTimeout = time.Second

	result := coordinator.Check(t.Context(), config, checkMember(t, "127.0.0.1:1"))

	assert.Equal(t, "127.0.0.1:443", result.Proxy)
}

// startCheckServer starts a coordinator that serves the health service and
// the worker list, and returns its address.
func startCheckServer(t *testing.T, opts ...grpc.ServerOption) string {
	t.Helper()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	server, addr := startMockServerWithHealth(t, coordinator.NewHandler(coordinator.HandlerConfig{}), healthServer, opts...)
	t.Cleanup(server.Stop)
	return addr
}

func tlsCreds(t *testing.T, certFile, keyFile string) grpc.ServerOption {
	t.Helper()
	creds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
	require.NoError(t, err)
	return grpc.Creds(creds)
}

// mutualTLSCreds serves certFile and asks for a client certificate that
// clientCAFile signed, as clientAuth says, over TLS up to maxVersion.
func mutualTLSCreds(t *testing.T, certFile, keyFile, clientCAFile string, clientAuth tls.ClientAuthType, maxVersion uint16) grpc.ServerOption {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)
	caPEM, err := os.ReadFile(clientCAFile)
	require.NoError(t, err)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(caPEM))
	return grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   clientAuth,
		ClientCAs:    clientCAs,
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   maxVersion,
	}))
}

// startSilentListener accepts connections and never writes to them.
func startSilentListener(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return listener.Addr().String()
}

// connectProxy is an HTTP CONNECT proxy that records the addresses it
// tunnels to.
type connectProxy struct {
	addr string

	mu   sync.Mutex
	seen []string
}

func (p *connectProxy) targets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func startConnectProxy(t *testing.T) *connectProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	proxy := &connectProxy{addr: listener.Addr().String()}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go proxy.tunnel(conn)
		}
	}()
	return proxy
}

func (p *connectProxy) tunnel(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	p.mu.Lock()
	p.seen = append(p.seen, req.Host)
	p.mu.Unlock()
	target, err := net.Dial("tcp", req.Host)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
	// Either side closing ends the tunnel, as a real proxy does.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, target); done <- struct{}{} }()
	<-done
	_ = target.Close()
}

func checkMember(t *testing.T, addr string) serviceregistry.HostInfo {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return serviceregistry.HostInfo{ID: addr, Host: host, Port: port}
}

func emptyIfNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
