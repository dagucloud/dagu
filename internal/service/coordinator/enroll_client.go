// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

var (
	// ErrInviteCAMismatch is the error of Enroll at a coordinator on
	// built-in TLS whose CA does not have the invite's pin: one of another
	// Dagu.
	ErrInviteCAMismatch = errors.New("the coordinator's CA does not match the invite")
	// ErrNoBuiltinTLS is the error of Enroll at a coordinator that does not
	// serve built-in TLS, such as one with own certificates or one that
	// predates it. An invite worker counts it as unavailable.
	ErrNoBuiltinTLS = errors.New("the coordinator does not serve built-in TLS")
)

// InvitePin returns the pin of invite, or false when invite is not of the
// form dagu1.<pin>.<secret>.
func InvitePin(invite string) (string, bool) {
	pin, _, ok := parseInvite(invite)
	return pin, ok
}

// CAPin returns the pin an invite carries for the CA certificate ca.
func CAPin(ca *x509.Certificate) string {
	return caPin(ca)
}

// Enroll sends req to the coordinator at address over built-in TLS, without
// the peer token. It accepts the coordinator only when the CA certificate
// in the handshake has the pin of req.Invite and the server certificate
// chains to it, names coordinator.dagu.invalid, and allows server
// authentication. A handshake that fails on the pin returns
// ErrInviteCAMismatch, and any other failed handshake ErrNoBuiltinTLS. A
// refused request returns its gRPC status.
func Enroll(ctx context.Context, config *Config, address string, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
	pin, ok := InvitePin(req.Invite)
	if !ok {
		return nil, status.Error(codes.PermissionDenied, invalidInviteMessage)
	}
	creds := &enrollCredentials{handshake: &handshakeRecord{}}
	creds.TransportCredentials = credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: coordinatorCertName,
		// verifyPinnedChain checks the chain against the pin instead.
		InsecureSkipVerify: true, //nolint:gosec
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyPinnedChain(state.PeerCertificates, pin)
		},
	})
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(grpcMaxMsgSize)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client for %s: %w", address, err)
	}
	defer func() { _ = conn.Close() }()

	callCtx, cancel := context.WithTimeout(ctx, config.DialTimeout)
	defer cancel()
	resp, err := coordinatorv1.NewCoordinatorServiceClient(conn).Enroll(callCtx, req)
	switch handshakeErr := creds.handshake.get(); {
	case err == nil:
		return resp, nil
	case errors.Is(handshakeErr, ErrInviteCAMismatch):
		return nil, ErrInviteCAMismatch
	case handshakeErr != nil && !isTimeout(handshakeErr):
		return nil, fmt.Errorf("%w: %v", ErrNoBuiltinTLS, handshakeErr)
	case status.Code(err) == codes.Unimplemented:
		// A coordinator that predates enrollment.
		return nil, fmt.Errorf("%w: %v", ErrNoBuiltinTLS, err)
	}
	return nil, err
}

// verifyPinnedChain checks that a CA certificate in certs, the chain a
// coordinator sent, has pin, and that the server certificate, certs[0],
// chains to it, names coordinatorCertName, and allows server
// authentication.
func verifyPinnedChain(certs []*x509.Certificate, pin string) error {
	if len(certs) == 0 {
		return errors.New("the coordinator sent no certificate")
	}
	leaf := certs[0]
	for _, ca := range certs[1:] {
		if caPin(ca) != pin {
			continue
		}
		roots := x509.NewCertPool()
		roots.AddCert(ca)
		_, err := leaf.Verify(x509.VerifyOptions{
			Roots:     roots,
			DNSName:   coordinatorCertName,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		return err
	}
	if leaf.VerifyHostname(coordinatorCertName) == nil {
		return ErrInviteCAMismatch
	}
	return errors.New("the server certificate is not one of built-in TLS")
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

// enrollCredentials dial TLS and record why a handshake failed, which gRPC
// reports only as Unavailable.
type enrollCredentials struct {
	credentials.TransportCredentials
	// handshake is shared by clones, since gRPC may dial with one.
	handshake *handshakeRecord
}

func (c *enrollCredentials) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ClientHandshake(ctx, authority, rawConn)
	if err != nil {
		c.handshake.set(err)
	}
	return conn, info, err
}

func (c *enrollCredentials) Clone() credentials.TransportCredentials {
	return &enrollCredentials{TransportCredentials: c.TransportCredentials.Clone(), handshake: c.handshake}
}

// handshakeRecord holds the error of the last failed handshake.
type handshakeRecord struct {
	mu  sync.Mutex
	err error
}

func (r *handshakeRecord) set(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *handshakeRecord) get() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}
