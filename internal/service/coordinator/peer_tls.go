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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"google.golang.org/grpc/credentials"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

const (
	// coordinatorCertName is the name every coordinator certificate carries.
	// Clients on built-in TLS check it instead of the dialed address.
	coordinatorCertName = "coordinator.dagu.invalid"
	// tlsHandshakeRecord is the first byte of a TLS connection: the record
	// type of the ClientHello.
	tlsHandshakeRecord = 0x16
)

// NewPeerTransportCredentials returns the server credentials of a
// coordinator with the peer CA ca. They serve built-in TLS and h2c on one
// port: a connection that starts with a TLS handshake record gets TLS, and
// any other connection is served as h2c. The TLS server certificate is
// issued from ca and names coordinator.dagu.invalid and each non-empty host
// in hosts that a certificate can carry. TLS needs version 1.3, asks for a
// client certificate, and accepts a connection without one; a client
// certificate that does not chain to ca fails the handshake.
func NewPeerTransportCredentials(ctx context.Context, ca *PeerCA, hosts ...string) (credentials.TransportCredentials, error) {
	cert, err := ca.issueCoordinatorCertificate(ctx, hosts)
	if err != nil {
		return nil, err
	}
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(ca.cert)
	return &peerTransportCredentials{
		tls: credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.VerifyClientCertIfGiven,
			ClientCAs:    clientCAs,
			MinVersion:   tls.VersionTLS13,
		}),
	}, nil
}

// peerTransportCredentials picks TLS or h2c from the first byte of each
// connection. gRPC runs ServerHandshake on the connection's own goroutine
// under its connection timeout, so a client that sends nothing holds only
// its own connection, and only until that timeout closes it.
type peerTransportCredentials struct {
	tls credentials.TransportCredentials
}

func (c *peerTransportCredentials) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	first := make([]byte, 1)
	// A connection closed before its first byte returns io.EOF as is, which
	// gRPC drops without a log line.
	if _, err := io.ReadFull(conn, first); err != nil {
		return nil, nil, err
	}
	conn = &peekedConn{Conn: conn, first: first}
	if first[0] != tlsHandshakeRecord {
		// h2c carries no AuthInfo, as on a server without credentials.
		return conn, nil, nil
	}
	return c.tls.ServerHandshake(conn)
}

func (c *peerTransportCredentials) ClientHandshake(context.Context, string, net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("peer transport credentials serve only")
}

func (c *peerTransportCredentials) Info() credentials.ProtocolInfo {
	return c.tls.Info()
}

func (c *peerTransportCredentials) Clone() credentials.TransportCredentials {
	return &peerTransportCredentials{tls: c.tls.Clone()}
}

func (c *peerTransportCredentials) OverrideServerName(string) error {
	return nil
}

// peekedConn returns the bytes read to pick the transport before the rest
// of the connection.
type peekedConn struct {
	net.Conn
	first []byte
}

func (c *peekedConn) Read(p []byte) (int, error) {
	if len(c.first) == 0 {
		return c.Conn.Read(p)
	}
	n := copy(p, c.first)
	c.first = c.first[n:]
	return n, nil
}

// issueCoordinatorCertificate returns a server certificate signed by ca, with
// no expiry date, naming coordinatorCertName and each of hosts as a DNS name
// or an IP address. Its chain holds the CA certificate.
func (ca *PeerCA) issueCoordinatorCertificate(ctx context.Context, hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate coordinator key: %w", err)
	}
	serial, err := newCertSerial()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate coordinator certificate serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: coordinatorCertName},
		NotBefore:    time.Now().Add(-certBackdate),
		NotAfter:     peerCANoExpiry,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	addCertificateNames(ctx, template, append([]string{coordinatorCertName}, hosts...))
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, key.Public(), ca.key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue coordinator certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse coordinator certificate: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der, ca.cert.Raw},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

// addCertificateNames adds each non-empty host to template once, as an IP
// address when it is one, possibly in brackets, and as a DNS name otherwise.
// A name that is not ASCII is added in its IDNA form, and skipped when it has
// none, since a certificate carries ASCII names only. Clients on built-in TLS
// check coordinatorCertName alone, so a skipped name affects only tools that
// check the dialed address.
func addCertificateNames(ctx context.Context, template *x509.Certificate, hosts []string) {
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		name, err := certificateName(host)
		if err != nil {
			logger.Debug(ctx, "Coordinator certificate skips a host name it cannot carry",
				slog.String("host", host), tag.Error(err))
			continue
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if ip := listenIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
}

// certificateName returns host as a certificate names it: lowercase, and in
// its IDNA form when it is not ASCII.
func certificateName(host string) (string, error) {
	for _, r := range host {
		if r >= utf8.RuneSelf {
			return idna.Lookup.ToASCII(host)
		}
	}
	return strings.ToLower(host), nil
}
