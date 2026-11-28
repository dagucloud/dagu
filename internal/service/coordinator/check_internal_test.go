// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

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

// createCertificate returns a certificate named name for key, signed by
// parent and parentKey, or by itself when parent is nil.
func createCertificate(t *testing.T, name pkix.Name, key *ecdsa.PrivateKey, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               name,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
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
// connections make, and changes no h2c window (Spec 108 §Client Transport,
// §dagu worker --check).
func TestCheckTransport(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t)
	builtin := serveCheckCoordinator(t, builtinCredentials(t, ca))
	plaintext := serveCheckCoordinator(t, insecure.NewCredentials())
	strict := serveCheckCoordinator(t, builtinCredentials(t, ca),
		grpc.ChainUnaryInterceptor(RequireTLSUnaryInterceptor()))

	for _, tc := range []struct {
		name string
		addr string
		// config sets up the client on top of peer.insecure unset with the
		// published CA certificate and a registry that reports built-in TLS.
		config        func(config *Config)
		window        bool
		wantTransport string
		wantTLS       bool
		wantFallback  string
		wantAnswer    string
	}{
		{name: "DataDir", addr: builtin,
			wantTransport: "built-in TLS (from the coordinator's data directory)", wantTLS: true},
		{name: "CACertFile", addr: builtin,
			config:        func(config *Config) { config.BuiltinCAFile, config.PublishedCAFile = config.PublishedCAFile, "" },
			wantTransport: "built-in TLS (from peer.ca_cert_file)", wantTLS: true},
		{name: "NoTLSSource", addr: builtin,
			config:        func(config *Config) { config.PublishedCAFile = "" },
			wantTransport: "h2c (from no TLS source)"},
		{name: "OpenWindow", addr: builtin, window: true,
			wantTransport: "h2c (from tls failed on the network path)"},
		{name: "FallbackOnNetworkPath", addr: plaintext,
			wantTransport: "built-in TLS (from the coordinator's data directory)", wantTLS: true,
			wantFallback: sourceNetworkPath},
		{name: "RequireTLS", addr: strict,
			config:        func(config *Config) { *config = *DefaultConfig() },
			wantTransport: "h2c (from peer.insecure: true)",
			wantAnswer:    requireTLSClientMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			member := checkCoordinatorMember(t, tc.addr)
			config := unset(*DefaultConfig())
			config.PublishedCAFile = publishedCA(t, ca)
			config.LocalRegistry = &mutableRegistry{members: []serviceregistry.HostInfo{member}}
			if tc.config != nil {
				tc.config(&config)
			}
			config.FallbackFile = filepath.Join(t.TempDir(), "peer-h2c.json")
			if tc.window {
				now := time.Now()
				sharedH2CWindows(config.FallbackFile).open(tc.addr,
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
			if tc.wantAnswer != "" {
				assert.EqualError(t, result.AnswerErr, tc.wantAnswer)
				return
			}
			assert.True(t, result.Passed(), "%+v", result)
			after, _ := os.ReadFile(config.FallbackFile)
			assert.Equal(t, before, after, "the check opens or ends no h2c window")
		})
	}
}

// serveCheckCoordinator serves the health service and the worker list with
// creds and returns the address.
func serveCheckCoordinator(t *testing.T, creds credentials.TransportCredentials, opts ...grpc.ServerOption) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(append([]grpc.ServerOption{grpc.Creds(creds)}, opts...)...)
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())
	coordinatorv1.RegisterCoordinatorServiceServer(server, NewHandler(HandlerConfig{}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
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
