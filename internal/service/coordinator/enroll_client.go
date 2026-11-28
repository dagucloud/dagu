// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// The failures of Enroll that a worker reports each in its own words.
var (
	// ErrInviteCAMismatch is the error of a coordinator on built-in TLS
	// whose CA does not have the invite's pin: one of another Dagu.
	ErrInviteCAMismatch = errors.New("the coordinator's CA does not match the invite")
	// ErrOwnCertificates is the error of a coordinator that serves TLS with
	// its own certificates, peer.cert_file and peer.key_file, and so no
	// built-in TLS.
	ErrOwnCertificates = errors.New("the coordinator serves TLS with its own certificates")
	// ErrNoBuiltinTLS is the error of a coordinator that does not answer
	// built-in TLS, such as one that predates it or serves h2c only.
	ErrNoBuiltinTLS = errors.New("the coordinator does not serve built-in TLS")
	// ErrInvalidEnrollReply is the error of a reply whose certificate does
	// not match the request or the invite's CA.
	ErrInvalidEnrollReply = errors.New("the coordinator's enrollment reply does not match the request")
)

// errNotBuiltinCertificate fails a handshake whose server certificate is
// not one of built-in TLS: no CA in the chain signed a certificate that
// names coordinatorCertName.
var errNotBuiltinCertificate = errors.New("the server certificate is not one of built-in TLS")

// Invite is a worker.invite: dagu1.<pin>.<secret>.
type Invite struct {
	pin    string
	secret []byte
}

// ParseInvite returns the invite s, or false when s is not of the form
// dagu1.<pin>.<secret>.
func ParseInvite(s string) (Invite, bool) {
	pin, secret, ok := parseInvite(s)
	return Invite{pin: pin, secret: secret}, ok
}

// Names reports whether ca is the CA the invite names by its pin.
func (i Invite) Names(ca *x509.Certificate) bool {
	return caPin(ca) == i.pin
}

// Digest identifies the invite, whatever its spelling, without holding its
// secret: the hex SHA-256 of its canonical form.
func (i Invite) Digest() string {
	sum := sha256.Sum256([]byte(i.String()))
	return hex.EncodeToString(sum[:])
}

// String returns the canonical form of the invite.
func (i Invite) String() string {
	return strings.Join([]string{inviteVersion, i.pin, inviteEncoding.EncodeToString(i.secret)}, ".")
}

// Enroll sends req, with the invite of config, a client from WithInvite, to
// the coordinator at address. It dials as the client's connections do: over
// built-in TLS, accepting the coordinator only when the CA in the handshake
// has the invite's pin and signed the server certificate, without the peer
// token, and never over h2c. It returns the reply once its certificate
// matches the request and that CA.
//
// A failed handshake returns ErrInviteCAMismatch, ErrOwnCertificates, or
// ErrNoBuiltinTLS; a reply that does not match, ErrInvalidEnrollReply; a
// refused request, its gRPC status.
func Enroll(ctx context.Context, config *Config, address string, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
	if config.invite == nil {
		return nil, errors.New("enrollment needs a client with an invite")
	}
	cli := New(nil, config).(*clientImpl)
	c, err := cli.createClient(address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.conn.Close() }()

	req = proto.CloneOf(req)
	req.Invite = config.invite.String()
	callCtx, cancel := context.WithTimeout(ctx, config.DialTimeout)
	defer cancel()
	resp, err := c.client.Enroll(callCtx, req)
	if err != nil {
		return nil, enrollError(err, c.link.lastHandshakeError())
	}
	if err := checkEnrollReply(resp, req.Csr, *config.invite); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEnrollReply, err)
	}
	return resp, nil
}

// enrollError classifies the error of an Enroll call by why its handshake
// failed, if it did.
func enrollError(err, handshakeErr error) error {
	switch {
	case errors.Is(handshakeErr, ErrInviteCAMismatch):
		return ErrInviteCAMismatch
	case errors.Is(handshakeErr, errNotBuiltinCertificate):
		return ErrOwnCertificates
	case handshakeErr != nil && !isTimeout(handshakeErr):
		return fmt.Errorf("%w: %v", ErrNoBuiltinTLS, handshakeErr)
	case status.Code(err) == codes.Unimplemented:
		// A coordinator that predates enrollment.
		return fmt.Errorf("%w: %v", ErrNoBuiltinTLS, err)
	}
	return err
}

// verifyPinnedChain checks that a CA certificate in certs, the chain a
// coordinator sent, has pin, and that the server certificate, certs[0],
// chains to it, names coordinatorCertName, and allows server
// authentication. A chain of built-in TLS from another CA fails with
// ErrInviteCAMismatch, and any other certificate with
// errNotBuiltinCertificate.
func verifyPinnedChain(certs []*x509.Certificate, pin string) error {
	if len(certs) == 0 {
		return errNotBuiltinCertificate
	}
	leaf := certs[0]
	builtin := false
	for _, ca := range certs[1:] {
		if caPin(ca) == pin {
			_, err := leaf.Verify(x509.VerifyOptions{
				Roots:     certPool(ca),
				DNSName:   coordinatorCertName,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			})
			return err
		}
		// A coordinator of built-in TLS sends the CA that signed its
		// certificate.
		builtin = builtin || (ca.IsCA && leaf.CheckSignatureFrom(ca) == nil)
	}
	if builtin && leaf.VerifyHostname(coordinatorCertName) == nil {
		return ErrInviteCAMismatch
	}
	return errNotBuiltinCertificate
}

// checkEnrollReply checks that the worker certificate in resp is for the
// key of the certificate signing request csr, chains to a CA with the pin of
// invite, allows client authentication only, and names a certificate ID and
// an invite ID.
func checkEnrollReply(resp *coordinatorv1.EnrollResponse, csr []byte, invite Invite) error {
	request, err := x509.ParseCertificateRequest(csr)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(resp.CaCertificate)
	if err != nil {
		return fmt.Errorf("the CA certificate cannot be read: %w", err)
	}
	if !invite.Names(ca) {
		return errors.New("the CA certificate is not the one the invite names")
	}
	cert, err := x509.ParseCertificate(resp.Certificate)
	if err != nil {
		return fmt.Errorf("the certificate cannot be read: %w", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: certPool(ca), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("the certificate does not chain to the CA: %w", err)
	}
	key, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !key.Equal(request.PublicKey) {
		return errors.New("the certificate is for another key")
	}
	if cert.IsCA || len(cert.UnknownExtKeyUsage) > 0 || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		return errors.New("the certificate allows more than client authentication")
	}
	if cert.Subject.CommonName == "" || inviteIDOf(cert) == "" {
		return errors.New("the certificate names no certificate ID or invite ID")
	}
	return nil
}

// inviteIDOf returns the invite ID a worker certificate names, or "".
func inviteIDOf(cert *x509.Certificate) string {
	if len(cert.URIs) != 1 || cert.URIs[0].Scheme != "urn" {
		return ""
	}
	id, _ := strings.CutPrefix(cert.URIs[0].Opaque, workerCertInviteURNPrefix)
	if id == cert.URIs[0].Opaque {
		return ""
	}
	return id
}

func certPool(certs ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}
