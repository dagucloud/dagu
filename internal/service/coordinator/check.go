// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// CheckResult is what a check of one coordinator address found.
type CheckResult struct {
	// Address is the address dialed, as host:port.
	Address string
	// DialErr is why the address did not accept a connection; nil when it
	// did.
	DialErr error
	// TLS is the TLS handshake; nil over h2c and when the address did not
	// accept a connection.
	TLS *TLSHandshake
	// Credential is what the client presents to the coordinator.
	Credential Credential
	// AnswerErr is why the coordinator did not serve the client's call; nil
	// when it did. It is nil as well when the check stopped earlier.
	AnswerErr error
}

// TLSHandshake is what a TLS handshake with a coordinator showed.
type TLSHandshake struct {
	// Err says why the handshake failed; nil when it succeeded.
	Err error
	// Certificate describes the server certificate; nil when the server sent
	// none.
	Certificate *ServerCertificate
}

// ServerCertificate describes a server certificate.
type ServerCertificate struct {
	// Names are the DNS names and IP addresses the certificate covers.
	Names []string
	// CA names the CA the certificate chains to.
	CA string
	// SelfSigned reports whether the certificate names itself as its CA.
	SelfSigned bool
}

// Credential is what a client presents to a coordinator.
type Credential struct {
	// CertFile is the client certificate sent over TLS; empty when none.
	CertFile string
	// PeerToken is where the peer token comes from.
	PeerToken PeerTokenSource
}

// PeerTokenSource is where a client's peer token comes from.
type PeerTokenSource int

const (
	// PeerTokenNone means the client sends no peer token.
	PeerTokenNone PeerTokenSource = iota
	// PeerTokenConfigured means the token comes from peer.token.
	PeerTokenConfigured
	// PeerTokenDataDir means the token comes from the coordinator's data
	// directory.
	PeerTokenDataDir
)

// Passed reports whether the coordinator accepted a connection, completed
// the TLS handshake when TLS is used, and served the call.
func (r CheckResult) Passed() bool {
	return r.DialErr == nil && (r.TLS == nil || r.TLS.Err == nil) && r.AnswerErr == nil
}

// Check dials the coordinator that member names with config, as a worker
// does, and reports what it found. It registers no worker, takes no task,
// and changes no state on the coordinator.
func Check(ctx context.Context, config *Config, member serviceregistry.HostInfo) CheckResult {
	result := CheckResult{
		Address:    coordinatorAddress(member),
		Credential: clientCredential(config),
	}
	ctx, cancel := context.WithTimeout(ctx, config.DialTimeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", result.Address)
	if err != nil {
		result.DialErr = err
		return result
	}
	if !config.Insecure {
		result.TLS = checkHandshake(ctx, config, conn, member.Host)
	}
	_ = conn.Close()
	if result.TLS != nil && result.TLS.Err != nil {
		return result
	}
	result.AnswerErr = checkAnswer(ctx, config, member)
	return result
}

// checkHandshake runs the TLS handshake that gRPC runs on conn.
func checkHandshake(ctx context.Context, config *Config, conn net.Conn, host string) *TLSHandshake {
	tlsConfig, err := clientTLSConfig(config)
	if err != nil {
		return &TLSHandshake{Err: err}
	}
	// gRPC verifies the dialed host and offers HTTP/2.
	tlsConfig.ServerName = host
	tlsConfig.NextProtos = []string{"h2"}
	tlsConn := tls.Client(conn, tlsConfig)
	err = tlsConn.HandshakeContext(ctx)
	if err == nil {
		state := tlsConn.ConnectionState()
		return &TLSHandshake{Certificate: describeServerCertificate(state.PeerCertificates, state.VerifiedChains)}
	}
	if verifyErr, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return &TLSHandshake{
			Err:         verificationProblem(verifyErr, tlsConfig.RootCAs, host),
			Certificate: describeServerCertificate(verifyErr.UnverifiedCertificates, nil),
		}
	}
	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return &TLSHandshake{Err: errors.New("the coordinator answered without TLS on this port")}
	}
	return &TLSHandshake{Err: err}
}

// verificationProblem says why a server certificate did not verify against
// roots for host. The TLS check stops at the first problem, so each one is
// checked here.
func verificationProblem(err *tls.CertificateVerificationError, roots *x509.CertPool, host string) error {
	certs := err.UnverifiedCertificates
	if len(certs) == 0 {
		return err
	}
	var problems []string
	if certs[0].VerifyHostname(host) != nil {
		problems = append(problems, "the certificate does not cover "+host)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	_, chainErr := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates})
	_, unknownCA := errors.AsType[x509.UnknownAuthorityError](chainErr)
	invalid, isInvalid := errors.AsType[x509.CertificateInvalidError](chainErr)
	switch {
	case chainErr == nil:
	case unknownCA:
		problems = append(problems, "the certificate does not chain to a CA this client trusts")
	case isInvalid && invalid.Reason == x509.Expired:
		problems = append(problems, "the certificate has expired or is not valid yet")
	default:
		problems = append(problems, chainErr.Error())
	}
	if len(problems) == 0 {
		return err
	}
	return errors.New(strings.Join(problems, "; "))
}

// describeServerCertificate describes the leaf of chain, which the server
// sent leaf first. verified holds the chains that verified, if any.
func describeServerCertificate(chain []*x509.Certificate, verified [][]*x509.Certificate) *ServerCertificate {
	if len(chain) == 0 {
		return nil
	}
	leaf := chain[0]
	names := append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	ca := chain[len(chain)-1].Issuer
	if len(verified) > 0 && len(verified[0]) > 0 {
		ca = verified[0][len(verified[0])-1].Subject
	}
	return &ServerCertificate{
		Names:      names,
		CA:         distinguishedName(ca),
		SelfSigned: len(chain) == 1 && bytes.Equal(leaf.RawIssuer, leaf.RawSubject),
	}
}

// distinguishedName returns the common name of name, or the whole name when
// it has none.
func distinguishedName(name pkix.Name) string {
	if name.CommonName != "" {
		return name.CommonName
	}
	return name.String()
}

// checkAnswer asks the coordinator for its health and its worker list with
// the client's credential.
func checkAnswer(ctx context.Context, config *Config, member serviceregistry.HostInfo) error {
	cli := &clientImpl{config: config}
	c, err := cli.createClient(member)
	if err != nil {
		return err
	}
	defer func() { _ = c.conn.Close() }()

	health, err := c.healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		if config.Insecure && status.Code(err) == codes.Unavailable && servesTLS(ctx, c.address) {
			return errServesTLS
		}
		return callError(err)
	}
	if health.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("the coordinator is not serving: %s", strings.ToLower(health.Status.String()))
	}
	// The health service takes no credential; the worker list does.
	if _, err := c.client.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{}); err != nil {
		return callError(err)
	}
	return nil
}

// errServesTLS is the answer of a coordinator that serves only TLS to a
// client on h2c.
var errServesTLS = errors.New("it serves only TLS on this port: set peer.insecure: false with a TLS setting")

// servesTLS reports whether address answers a TLS handshake.
func servesTLS(ctx context.Context, address string) bool {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	probe := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Only the protocol matters here, not who the server is.
		MinVersion:         tls.VersionTLS12,
	}
	err = tls.Client(conn, probe).HandshakeContext(ctx)
	_, alert := errors.AsType[tls.AlertError](err)
	return err == nil || alert
}

// callError returns the message of a gRPC call error without its code.
func callError(err error) error {
	s, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch s.Code() {
	case codes.Unauthenticated, codes.PermissionDenied:
		return fmt.Errorf("refused the call, %s", s.Message())
	default:
		return errors.New(s.Message())
	}
}

// clientCredential returns what a client with config presents.
func clientCredential(config *Config) Credential {
	var credential Credential
	if !config.Insecure && config.CertFile != "" && config.KeyFile != "" {
		credential.CertFile = config.CertFile
	}
	switch {
	case config.Token != "":
		credential.PeerToken = PeerTokenConfigured
	case config.TokenFile != "":
		if _, err := readPeerToken(config.TokenFile); err == nil {
			credential.PeerToken = PeerTokenDataDir
		}
	}
	return credential
}
