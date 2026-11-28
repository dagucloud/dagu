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
	"os"
	"slices"
	"strings"
	"time"
)

// errNoServerAuth is the problem of a certificate that a server cannot
// present.
const errNoServerAuth = "the certificate does not allow server authentication"

// checkHandshake runs on conn the TLS handshake that the client's
// connection runs: built-in TLS when plan has roots, and the TLS settings
// otherwise. It then reads once, since TLS 1.3 refuses a client certificate
// only after the client side of the handshake is done. It also reports
// whether the client sent its certificate.
func checkHandshake(ctx context.Context, config *Config, plan connectionPlan, conn net.Conn, host string) (*TLSHandshake, bool) {
	tlsConfig, verifyHost, err := handshakeConfig(config, plan, host)
	if err != nil {
		return &TLSHandshake{Err: err}, false
	}
	// An alert after the server asked for a client certificate refuses it.
	certRequested, certSent := false, false
	clientCert := &tls.Certificate{}
	if len(tlsConfig.Certificates) > 0 {
		clientCert = &tlsConfig.Certificates[0]
	}
	tlsConfig.GetClientCertificate = func(request *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		certRequested = true
		// Send the certificate only when the server accepts its kind and
		// CA, as crypto/tls does without this hook.
		if request.SupportsCertificate(clientCert) != nil {
			return &tls.Certificate{}, nil
		}
		certSent = len(clientCert.Certificate) > 0
		return clientCert, nil
	}
	recorded := &handshakeConn{Conn: conn}
	tlsConn := tls.Client(recorded, tlsConfig)
	err = tlsConn.HandshakeContext(ctx)
	if verifyErr, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		problem := verificationProblem(verifyErr, tlsConfig.RootCAs, verifyHost)
		if plan.cert != nil && foreignCoordinatorFormat(err) == otherDaguFormat {
			problem = ErrOtherDagu
		}
		return &TLSHandshake{
			Err:         problem,
			Fallback:    fallbackFrom(config, plan, recorded, err),
			Certificate: describeServerCertificate(verifyErr.UnverifiedCertificates, nil),
		}, certSent
	}
	if err == nil && certRequested {
		// A refusal arrives at once. A server that waits for the client's
		// HTTP/2 preface sends nothing, so the read is bounded.
		_ = tlsConn.SetReadDeadline(time.Now().Add(clientCertRefusalWait))
		_, err = tlsConn.Read(make([]byte, 1))
		if !isRemoteAlert(err) {
			err = nil
		}
	}
	state := tlsConn.ConnectionState()
	handshake := &TLSHandshake{
		// A pinned handshake verifies the chain itself, and fails without
		// a match.
		Verified:    len(state.VerifiedChains) > 0 || plan.pin != "",
		Certificate: describeServerCertificate(state.PeerCertificates, state.VerifiedChains),
	}
	if err == nil {
		return handshake, certSent
	}
	handshake.Fallback = fallbackFrom(config, plan, recorded, err)
	_, notTLS := errors.AsType[tls.RecordHeaderError](err)
	switch {
	case certRequested && isRemoteAlert(err):
		handshake.Err = clientCertProblem(config, certSent, err)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
		handshake.Err = noAnswer(config)
	case notTLS:
		handshake.Err = errors.New("the coordinator answered without TLS on this port")
	case errors.Is(err, ErrInviteCAMismatch):
		handshake.Err = ErrInviteCAMismatch
	case errors.Is(err, errNotBuiltinCertificate):
		handshake.Err = errors.New("the coordinator serves TLS with its own certificates, so this worker cannot join it with an invite")
	default:
		handshake.Err = err
	}
	return handshake, certSent
}

// handshakeConfig returns the TLS configuration of a connection on plan to
// host, and the name the server certificate must carry.
func handshakeConfig(config *Config, plan connectionPlan, host string) (*tls.Config, string, error) {
	if !plan.h2c() {
		tlsConfig := builtinTLSConfig(plan)
		tlsConfig.NextProtos = []string{"h2"}
		return tlsConfig, coordinatorCertName, nil
	}
	tlsConfig, err := clientTLSConfig(config)
	if err != nil {
		return nil, "", err
	}
	// gRPC verifies the dialed host and offers HTTP/2.
	tlsConfig.ServerName = host
	tlsConfig.NextProtos = []string{"h2"}
	return tlsConfig, host, nil
}

// builtinTLSConfig returns the TLS configuration of a built-in TLS
// connection on plan. It checks the name every coordinator certificate
// carries, not the dialed address, and presents the worker certificate of
// plan, if any.
func builtinTLSConfig(plan connectionPlan) *tls.Config {
	tlsConfig := &tls.Config{RootCAs: plan.roots, MinVersion: tls.VersionTLS13, ServerName: coordinatorCertName}
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
	return tlsConfig
}

// fallbackFrom returns the source of the h2c the client falls back to after
// a built-in TLS handshake on plan failed with err, or "" when it does not.
func fallbackFrom(config *Config, plan connectionPlan, conn *handshakeConn, err error) string {
	if plan.roots == nil {
		return ""
	}
	return config.fallbackSource(plan, conn, err)
}

// clientCertProblem says why a coordinator refused the client with the alert
// in err. certSent reports whether the client sent its certificate.
func clientCertProblem(config *Config, certSent bool, err error) error {
	switch {
	case config.CertFile == "" || config.KeyFile == "":
		return errors.New("the coordinator requires a client certificate, and the worker sends none: " +
			"set peer.cert_file and peer.key_file to a certificate that the coordinator's peer.client_ca_file signed")
	case !certSent:
		return fmt.Errorf("the coordinator requires a client certificate that its peer.client_ca_file signed, "+
			"and %s is not one, so the worker sends none", config.CertFile)
	default:
		reason := err.Error()
		if opErr, ok := errors.AsType[*net.OpError](err); ok {
			reason = opErr.Err.Error()
		}
		return fmt.Errorf("the coordinator refused the client certificate %s (%s): "+
			"use a valid certificate that its peer.client_ca_file signed", config.CertFile, reason)
	}
}

// verificationProblem says why a server certificate did not verify against
// roots for host. The TLS check stops at the first problem, so each one is
// checked here.
func verificationProblem(err *tls.CertificateVerificationError, roots *x509.CertPool, host string) error {
	certs := err.UnverifiedCertificates
	if len(certs) == 0 {
		return err
	}
	leaf := certs[0]
	var problems []string
	if leaf.VerifyHostname(host) != nil {
		if net.ParseIP(host) != nil && slices.Contains(leaf.DNSNames, host) {
			problems = append(problems, fmt.Sprintf("the certificate lists %s as a DNS name, not as an IP address", host))
		} else {
			problems = append(problems, "the certificate does not cover "+host)
		}
	}
	// The chain check stops at an untrusted CA before it checks usage.
	usageReported := !allowsServerAuth(leaf)
	if usageReported {
		problems = append(problems, errNoServerAuth)
	}
	options := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool()}
	for _, cert := range certs[1:] {
		options.Intermediates.AddCert(cert)
	}
	_, chainErr := leaf.Verify(options)
	invalid, isInvalid := errors.AsType[x509.CertificateInvalidError](chainErr)
	if isInvalid && invalid.Reason == x509.Expired {
		problems = append(problems, "the certificate has expired or is not valid yet")
		// The validity check runs before the chain is built, so the chain is
		// checked again at a time every certificate the server sent is valid.
		options.CurrentTime = validTime(certs)
		_, chainErr = leaf.Verify(options)
		invalid, isInvalid = errors.AsType[x509.CertificateInvalidError](chainErr)
	}
	_, unknownCA := errors.AsType[x509.UnknownAuthorityError](chainErr)
	switch {
	case chainErr == nil:
	case unknownCA:
		problems = append(problems, "the certificate does not chain to a CA this client trusts")
	case isInvalid && invalid.Reason == x509.Expired:
		// A CA in the chain is not valid at that time either; the expiry is
		// already reported.
	case isInvalid && invalid.Reason == x509.IncompatibleUsage:
		if !usageReported {
			problems = append(problems, errNoServerAuth)
		}
	default:
		problems = append(problems, chainErr.Error())
	}
	if len(problems) == 0 {
		return err
	}
	return errors.New(strings.Join(problems, "; "))
}

// validTime returns a time at which every certificate of chain is valid,
// the one nearest to now, or a time the leaf is valid when their validity
// does not overlap.
func validTime(chain []*x509.Certificate) time.Time {
	notBefore, notAfter := chain[0].NotBefore, chain[0].NotAfter
	for _, cert := range chain[1:] {
		if cert.NotBefore.After(notBefore) {
			notBefore = cert.NotBefore
		}
		if cert.NotAfter.Before(notAfter) {
			notAfter = cert.NotAfter
		}
	}
	if notAfter.Before(notBefore) {
		notBefore, notAfter = chain[0].NotBefore, chain[0].NotAfter
	}
	if time.Now().After(notAfter) {
		return notAfter.Add(-time.Second)
	}
	return notBefore.Add(time.Second)
}

// allowsServerAuth reports whether cert's extended key usage, if it names
// any, allows server authentication.
func allowsServerAuth(cert *x509.Certificate) bool {
	if len(cert.ExtKeyUsage) == 0 && len(cert.UnknownExtKeyUsage) == 0 {
		return true
	}
	return slices.ContainsFunc(cert.ExtKeyUsage, func(usage x509.ExtKeyUsage) bool {
		return usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny
	})
}

// describeServerCertificate describes the leaf of chain, which the server
// sent leaf first. verified holds the chains that verified, if any.
func describeServerCertificate(chain []*x509.Certificate, verified [][]*x509.Certificate) *ServerCertificate {
	if len(chain) == 0 {
		return nil
	}
	leaf := chain[0]
	ips := make([]string, 0, len(leaf.IPAddresses))
	for _, ip := range leaf.IPAddresses {
		ips = append(ips, ip.String())
	}
	ca := chain[len(chain)-1].Issuer
	if len(verified) > 0 && len(verified[0]) > 0 {
		ca = verified[0][len(verified[0])-1].Subject
	}
	return &ServerCertificate{
		DNSNames:    leaf.DNSNames,
		IPAddresses: ips,
		CA:          distinguishedName(ca),
		SelfSigned:  len(chain) == 1 && signsItself(leaf),
	}
}

// signsItself reports whether cert names itself as its issuer and its own
// key signed it.
func signsItself(cert *x509.Certificate) bool {
	return bytes.Equal(cert.RawIssuer, cert.RawSubject) &&
		cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}

// distinguishedName returns the common name of name, or the whole name when
// it has none.
func distinguishedName(name pkix.Name) string {
	if name.CommonName != "" {
		return name.CommonName
	}
	return name.String()
}
