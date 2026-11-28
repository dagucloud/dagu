// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"

	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// A certificate that a CA of the same name signed does not sign itself,
// though its issuer and subject match.
func TestSignsItself(t *testing.T) {
	t.Parallel()

	name := pkix.Name{CommonName: "Dagu"}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ca := createCertificate(t, name, caKey, nil, nil)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := createCertificate(t, name, leafKey, ca, caKey)

	assert.True(t, signsItself(ca))
	assert.False(t, signsItself(leaf))
}

// createCertificate returns a certificate named name for key, valid for an
// hour around now, signed by parent and parentKey, or by itself when parent
// is nil.
func createCertificate(t *testing.T, name pkix.Name, key *ecdsa.PrivateKey, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	return createCertificateAt(t, name, key, parent, parentKey, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
}

// newKey returns a new P-256 key.
func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return key
}

// createCertificateAt is createCertificate, valid from notBefore to
// notAfter.
func createCertificateAt(t *testing.T, name pkix.Name, key *ecdsa.PrivateKey, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               name,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  parent == nil,
		BasicConstraintsValid: true,
	}
	if parent == nil {
		parent, parentKey = template, key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

// The check picks the transport through the decision the client's
// connections make, sends the h2c reason as they do, and changes no h2c
// window (Spec 108 §Client Transport, §h2c Reasons, §dagu worker --check).
func TestCheckTransport(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	builtin := func(t *testing.T) *checkCoordinator { return serveCheckCoordinator(t, builtinCredentials(t, ca)) }
	plaintext := func(t *testing.T) *checkCoordinator { return serveCheckCoordinator(t, insecure.NewCredentials()) }
	strict := func(t *testing.T) *checkCoordinator {
		return serveCheckCoordinator(t, builtinCredentials(t, ca), grpc.ChainUnaryInterceptor(RequireTLSUnaryInterceptor()))
	}

	for _, tc := range []struct {
		name  string
		serve func(t *testing.T) *checkCoordinator
		// config sets up the client on top of peer.insecure unset with the
		// published CA certificate and a registry that reports built-in TLS.
		config        func(config *Config)
		window        bool
		wantTransport string
		wantTLS       bool
		wantFallback  string
		wantReason    string
		wantAnswer    error
	}{
		{name: "DataDir", serve: builtin,
			wantTransport: "built-in TLS (from the coordinator's data directory)", wantTLS: true},
		{name: "CACertFile", serve: builtin,
			config:        func(config *Config) { config.BuiltinCAFile, config.PublishedCAFile = config.PublishedCAFile, "" },
			wantTransport: "built-in TLS (from peer.ca_cert_file)", wantTLS: true},
		{name: "NoTLSSource", serve: builtin,
			config:        func(config *Config) { config.PublishedCAFile = "" },
			wantTransport: "h2c (from no TLS source)", wantReason: h2cReasonNoTLS},
		{name: "OpenWindow", serve: builtin, window: true,
			wantTransport: "h2c (from tls failed on the network path)", wantReason: h2cReasonNetworkPath},
		{name: "FallbackOnNetworkPath", serve: plaintext,
			wantTransport: "built-in TLS (from the coordinator's data directory)", wantTLS: true,
			wantFallback: sourceNetworkPath, wantReason: h2cReasonNetworkPath},
		{name: "RequireTLS", serve: strict,
			config:        func(config *Config) { *config = *DefaultConfig() },
			wantTransport: "h2c (from peer.insecure: true)",
			wantAnswer:    ErrRequireTLS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			coord := tc.serve(t)
			member := checkCoordinatorMember(t, coord.addr)
			config := unset(*DefaultConfig())
			config.PublishedCAFile = publishedCA(t, ca)
			config.LocalRegistry = &mutableRegistry{members: []serviceregistry.HostInfo{member}}
			if tc.config != nil {
				tc.config(&config)
			}
			config.FallbackFile = filepath.Join(t.TempDir(), "peer-h2c.json")
			if tc.window {
				now := time.Now()
				sharedH2CWindows(config.FallbackFile).open(coord.addr,
					h2cWindow{Until: now.Add(h2cWindowLength), Source: sourceNetworkPath, Error: "EOF"}, now)
			}
			before, _ := os.ReadFile(config.FallbackFile)

			result := Check(t.Context(), &config, member)

			assert.Equal(t, tc.wantTransport, result.Transport)
			assert.Equal(t, tc.wantTLS, result.TLS != nil)
			if tc.wantFallback != "" {
				require.NotNil(t, result.TLS)
				assert.Error(t, result.TLS.Err)
				assert.Equal(t, tc.wantFallback, result.TLS.Fallback)
			} else if result.TLS != nil {
				assert.NoError(t, result.TLS.Err)
				assert.Contains(t, result.TLS.Certificate.DNSNames, coordinatorCertName)
			}
			if tc.window {
				require.NotNil(t, result.Fallback)
				assert.Equal(t, "EOF", result.Fallback.Err)
			} else {
				assert.Nil(t, result.Fallback)
			}
			if tc.wantAnswer != nil {
				assert.ErrorIs(t, result.AnswerErr, tc.wantAnswer)
				return
			}
			assert.True(t, result.Passed(), "%+v", result)
			assert.Equal(t, tc.wantReason, coord.reason())
			after, _ := os.ReadFile(config.FallbackFile)
			assert.Equal(t, before, after, "the check opens or ends no h2c window")
		})
	}
}

// A coordinator that asks for a client certificate and waits for the HTTP/2
// preface makes the read after the handshake wait. The calls still get
// their own time (Spec 108 §dagu worker --check).
func TestCheckReadLeavesCallsTheirTime(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	creds, err := NewPeerTransportCredentials(t.Context(), ca)
	require.NoError(t, err)
	backend := serveCheckCoordinator(t, insecure.NewCredentials())
	front := startPrefaceFirstFront(t, creds, backend.addr)
	member := checkCoordinatorMember(t, front)
	config := unset(*DefaultConfig())
	config.BuiltinCAFile = publishedCA(t, ca)
	config.FallbackFile = filepath.Join(t.TempDir(), "peer-h2c.json")
	// Shorter than the read after the handshake, which waits a second.
	config.DialTimeout = clientCertRefusalWait / 2

	result := Check(t.Context(), &config, member)

	assert.True(t, result.Passed(), "%+v", result)
}

// startPrefaceFirstFront serves TLS with the coordinator's credentials and,
// once the client sends its first bytes, relays the connection to backend
// over h2c, as a TLS front end that waits for the HTTP/2 preface does. It
// returns its address.
func startPrefaceFirstFront(t *testing.T, creds credentials.TransportCredentials, backend string) string {
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
			go relayAfterFirstBytes(creds, conn, backend)
		}
	}()
	return listener.Addr().String()
}

func relayAfterFirstBytes(creds credentials.TransportCredentials, conn net.Conn, backend string) {
	defer func() { _ = conn.Close() }()
	tlsConn, _, err := creds.ServerHandshake(conn)
	if err != nil {
		return
	}
	first := make([]byte, 1)
	if _, err := io.ReadFull(tlsConn, first); err != nil {
		return
	}
	target, err := net.Dial("tcp", backend)
	if err != nil {
		return
	}
	defer func() { _ = target.Close() }()
	if _, err := target.Write(first); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, tlsConn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(tlsConn, target); done <- struct{}{} }()
	<-done
}

// An intermediate that expires before the leaf still lets the check see an
// untrusted root.
func TestVerificationProblemExpiredIntermediate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	rootKey := newKey(t)
	root := createCertificateAt(t, pkix.Name{CommonName: "root"}, rootKey, nil, nil, now.Add(-4*time.Hour), now.Add(time.Hour))
	interKey := newKey(t)
	inter := createCertificateAt(t, pkix.Name{CommonName: "intermediate"}, interKey, root, rootKey, now.Add(-4*time.Hour), now.Add(-3*time.Hour))
	leaf := createCertificateAt(t, pkix.Name{CommonName: "leaf"}, newKey(t), inter, interKey, now.Add(-4*time.Hour), now.Add(-time.Hour))
	otherRoots := x509.NewCertPool()
	otherRoots.AddCert(createCertificate(t, pkix.Name{CommonName: "other"}, newKey(t), nil, nil))

	err := verificationProblem(&tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{leaf, inter}}, otherRoots, "leaf")

	assert.EqualError(t, err, "the certificate does not cover leaf; the certificate has expired or is not valid yet; the certificate does not chain to a CA this client trusts")
}

// checkCoordinator serves the health service and the worker list.
type checkCoordinator struct {
	addr string

	mu sync.Mutex
	// lastReason is the h2c reason of the last worker list call.
	lastReason string
}

func (c *checkCoordinator) reason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastReason
}

// serveCheckCoordinator serves a coordinator with creds and records the h2c
// reason of its worker list calls.
func serveCheckCoordinator(t *testing.T, creds credentials.TransportCredentials, opts ...grpc.ServerOption) *checkCoordinator {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	coord := &checkCoordinator{addr: listener.Addr().String()}
	record := grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if strings.HasSuffix(info.FullMethod, "/GetWorkers") {
			md, _ := metadata.FromIncomingContext(ctx)
			coord.mu.Lock()
			coord.lastReason = strings.Join(md.Get(h2cReasonMetadataKey), ",")
			coord.mu.Unlock()
		}
		return handler(ctx, req)
	})
	server := grpc.NewServer(append([]grpc.ServerOption{grpc.Creds(creds), record}, opts...)...)
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())
	coordinatorv1.RegisterCoordinatorServiceServer(server, NewHandler(HandlerConfig{}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return coord
}

// checkCoordinatorMember returns the registry entry of the coordinator at
// addr, reporting built-in TLS.
func checkCoordinatorMember(t *testing.T, addr string) serviceregistry.HostInfo {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return serviceregistry.HostInfo{ID: addr, Host: host, Port: port, TLS: serviceregistry.TLSBuiltin}
}
