// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// workerCertInviteURNPrefix is the start of the opaque part of the urn:
	// URI by which a worker certificate names its invite.
	workerCertInviteURNPrefix = "dagu:invite:"
	// invalidInviteMessage refuses an invite that is malformed, unknown,
	// deleted, expired, or used up, without telling which.
	invalidInviteMessage = "the invite is invalid, expired, or already used"
	// removedKeyMessage refuses the key of a revoked certificate.
	removedKeyMessage = "this key belongs to a removed worker"
)

// Enroll issues a worker certificate for the key of the request's
// certificate signing request when the request's invite admits it, and
// consumes a single-use invite.
func (h *Handler) Enroll(ctx context.Context, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
	if err := h.requirePeerCA(); err != nil {
		return nil, err
	}
	if !overTLS(ctx) {
		return nil, status.Error(codes.Unauthenticated, "enrollment needs TLS")
	}
	pin, secret, ok := parseInvite(req.Invite)
	if !ok || pin != caPin(h.peerCA.cert) {
		return nil, status.Error(codes.PermissionDenied, invalidInviteMessage)
	}
	csr, err := parseWorkerCSR(req.Csr)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	invite, err := h.workerInvites.GetBySecretHash(ctx, inviteSecretHash(secret))
	if err != nil {
		return nil, inviteUseError(ctx, err)
	}
	now := h.clock().UTC()
	if invite.Ended(now) {
		return nil, status.Error(codes.PermissionDenied, invalidInviteMessage)
	}
	if invite.Kind == persis.WorkerInviteSingleUse && !req.KeepsCertificate {
		return nil, status.Error(codes.FailedPrecondition,
			"a single-use invite enrolls only a worker that keeps its certificate: make the worker's data directory writable, or use a reusable invite")
	}

	fingerprint := sha256.Sum256(csr.RawSubjectPublicKeyInfo)
	revoked, err := h.keyRevoked(ctx, fingerprint[:])
	if err != nil {
		logger.Error(ctx, "Failed to read the removed workers", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to check the key")
	}
	if revoked {
		return nil, status.Error(codes.PermissionDenied, removedKeyMessage)
	}

	certID := uuid.NewString()
	// The record takes the issuance time at the certificate's precision,
	// so a record written again from the certificate keeps it.
	issuedAt := now.Truncate(time.Second)
	der, err := h.peerCA.issueWorkerCertificate(csr.PublicKey, certID, invite.ID, issuedAt)
	if err != nil {
		logger.Error(ctx, "Failed to issue a worker certificate", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to issue the worker certificate")
	}
	// The invite is used at the time it admits the worker, after signing,
	// so one that ends meanwhile admits nothing.
	now = h.clock().UTC()
	if _, err := h.workerInvites.Use(ctx, invite.ID, now); err != nil {
		return nil, inviteUseError(ctx, err)
	}
	if err := h.workerCertificates.Create(ctx, persis.WorkerCertificate{
		ID:             certID,
		InviteID:       invite.ID,
		KeyFingerprint: fingerprint[:],
		State:          persis.WorkerCertificateActive,
		WorkerID:       req.WorkerId,
		Host:           req.Host,
		CreatedAt:      issuedAt,
	}); err != nil {
		logger.Error(ctx, "Failed to record a worker certificate",
			slog.String("certificate-id", certID),
			slog.String("invite-id", invite.ID),
			tag.Error(err),
		)
		return nil, status.Error(codes.Internal, "failed to record the worker certificate")
	}
	// A removal that revoked the key while the certificate was issued
	// refuses the enrollment, and the certificate is never handed out. The
	// invite stays used, as when recording the certificate fails.
	revoked, err = h.keyRevoked(ctx, fingerprint[:])
	if err != nil {
		logger.Error(ctx, "Failed to read the removed workers", slog.String("certificate-id", certID), tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to check the key")
	}
	if revoked {
		_ = h.revoke(ctx, certID)
		return nil, status.Error(codes.PermissionDenied, removedKeyMessage)
	}
	logger.Info(ctx, "Worker enrolled",
		slog.String("certificate-id", certID),
		slog.String("invite-id", invite.ID),
		tag.WorkerID(req.WorkerId),
		tag.Host(req.Host),
	)
	return &coordinatorv1.EnrollResponse{Certificate: der, CaCertificate: h.peerCA.cert.Raw}, nil
}

// inviteUseError is the status of an invite that could not be read or used.
func inviteUseError(ctx context.Context, err error) error {
	if errors.Is(err, persis.ErrWorkerInviteNotFound) || errors.Is(err, persis.ErrWorkerInviteEnded) {
		return status.Error(codes.PermissionDenied, invalidInviteMessage)
	}
	logger.Error(ctx, "Failed to use a worker invite", tag.Error(err))
	return status.Error(codes.Internal, "failed to use the invite")
}

// overTLS reports whether the call arrived over TLS.
func overTLS(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return false
	}
	_, ok = p.AuthInfo.(credentials.TLSInfo)
	return ok
}

// parseWorkerCSR returns the DER certificate signing request der when its
// signature verifies and its key is ECDSA P-256, the key a worker
// generates.
func parseWorkerCSR(der []byte) (*x509.CertificateRequest, error) {
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, errors.New("the certificate signing request cannot be read")
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, errors.New("the certificate signing request's signature does not verify")
	}
	if key, ok := csr.PublicKey.(*ecdsa.PublicKey); !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("the certificate signing request's key must be ECDSA P-256")
	}
	return csr, nil
}

// issueWorkerCertificate returns a DER certificate for key, signed by ca,
// with no expiry date. Its subject common name is certID and a URI
// urn:dagu:invite:<inviteID> names its invite. It allows client
// authentication only and is not a CA.
func (ca *PeerCA) issueWorkerCertificate(key crypto.PublicKey, certID, inviteID string, now time.Time) ([]byte, error) {
	serial, err := newCertSerial()
	if err != nil {
		return nil, fmt.Errorf("generate worker certificate serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: certID},
		NotBefore:             now.Add(-certBackdate),
		NotAfter:              peerCANoExpiry,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		URIs:                  []*url.URL{{Scheme: "urn", Opaque: workerCertInviteURNPrefix + inviteID}},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, key, ca.key)
	if err != nil {
		return nil, fmt.Errorf("issue worker certificate: %w", err)
	}
	return der, nil
}
