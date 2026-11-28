// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// inviteVersion starts every invite: dagu1.<pin>.<secret>.
	inviteVersion = "dagu1"
	// inviteSecretSize is the number of random bytes in an invite's secret.
	inviteSecretSize = 32
	// inviteFieldLength is the length of the pin and of the secret: 32 bytes
	// in base64url without padding.
	inviteFieldLength = 43
	// inviteLength is the length of a whole invite.
	inviteLength = len(inviteVersion) + 2*(1+inviteFieldLength)
	// singleUseInviteExpiry is when a single-use invite created without an
	// expiry ends.
	singleUseInviteExpiry = 24 * time.Hour
	// inviteCreatorPeerToken is the creator of an invite whose call named
	// no user.
	inviteCreatorPeerToken = "peer token"
	// maxInviteExpiresInSeconds keeps an invite's expiry within a Duration.
	maxInviteExpiresInSeconds = int64(math.MaxInt64 / time.Second)
)

var inviteKinds = map[persis.WorkerInviteKind]coordinatorv1.InviteKind{
	persis.WorkerInviteSingleUse: coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE,
	persis.WorkerInviteReusable:  coordinatorv1.InviteKind_INVITE_KIND_REUSABLE,
}

// inviteEncoding encodes an invite's pin and secret.
var inviteEncoding = base64.RawURLEncoding.Strict()

// CreateInvite creates an invite. Its response is the only one that holds
// the invite.
func (h *Handler) CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	if err := h.requirePeerCA(); err != nil {
		return nil, err
	}
	if req.ExpiresInSeconds < 0 || req.ExpiresInSeconds > maxInviteExpiresInSeconds {
		return nil, status.Error(codes.InvalidArgument, "expires_in_seconds must be a positive number of seconds, or 0 for the default")
	}
	if req.Address != "" && !validInviteAddress(req.Address) {
		return nil, status.Error(codes.InvalidArgument, "address must be host:port")
	}

	now := h.clock().UTC()
	record := persis.WorkerInvite{
		Kind:      persis.WorkerInviteSingleUse,
		Address:   req.Address,
		CreatedBy: cmp.Or(req.CreatedBy, inviteCreatorPeerToken),
		CreatedAt: now,
	}
	if req.Reusable {
		record.Kind = persis.WorkerInviteReusable
	}
	switch {
	case req.ExpiresInSeconds > 0:
		record.ExpiresAt = now.Add(time.Duration(req.ExpiresInSeconds) * time.Second)
	case !req.Reusable:
		record.ExpiresAt = now.Add(singleUseInviteExpiry)
	}
	invite, secretHash, err := newInvite(h.peerCA.cert)
	if err != nil {
		logger.Error(ctx, "Failed to generate a worker invite", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to generate the invite")
	}
	record.SecretHash = secretHash
	record, err = h.workerInvites.Create(ctx, record)
	if err != nil {
		logger.Error(ctx, "Failed to store a worker invite", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to store the invite")
	}
	logger.Info(ctx, "Worker invite created",
		slog.String("invite-id", record.ID),
		slog.String("kind", string(record.Kind)),
		slog.String("created-by", record.CreatedBy),
	)
	return &coordinatorv1.CreateInviteResponse{Invite: invite, Record: inviteRecord(record)}, nil
}

// ListInvites lists the invites, oldest first, without their secrets.
func (h *Handler) ListInvites(ctx context.Context, _ *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	if err := h.requirePeerCA(); err != nil {
		return nil, err
	}
	invites, err := h.workerInvites.List(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to list worker invites", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to list the invites")
	}
	resp := &coordinatorv1.ListInvitesResponse{Invites: make([]*coordinatorv1.InviteRecord, 0, len(invites))}
	for _, invite := range invites {
		resp.Invites = append(resp.Invites, inviteRecord(invite))
	}
	return resp, nil
}

// DeleteInvite deletes an invite. Workers that enrolled with it keep their
// certificates.
func (h *Handler) DeleteInvite(ctx context.Context, req *coordinatorv1.DeleteInviteRequest) (*coordinatorv1.DeleteInviteResponse, error) {
	if err := h.requirePeerCA(); err != nil {
		return nil, err
	}
	// An invite pasted in place of its ID never reaches the store, and the
	// refusal does not repeat the ID, so the invite shows up in no error or
	// log line.
	notFound := status.Error(codes.NotFound, "no invite has this ID: list the invites to find their IDs")
	if strings.HasPrefix(req.Id, inviteVersion+".") {
		return nil, notFound
	}
	err := h.workerInvites.Delete(ctx, req.Id)
	if errors.Is(err, persis.ErrWorkerInviteNotFound) {
		return nil, notFound
	}
	if err != nil {
		logger.Error(ctx, "Failed to delete a worker invite", tag.Error(err))
		return nil, status.Error(codes.Internal, "failed to delete the invite")
	}
	logger.Info(ctx, "Worker invite deleted", slog.String("invite-id", req.Id))
	return &coordinatorv1.DeleteInviteResponse{}, nil
}

// requirePeerCA fails an invite or enrollment call on a coordinator without a
// peer CA, saying why it has none.
func (h *Handler) requirePeerCA() error {
	if h.peerCA != nil {
		return nil
	}
	if h.joinReadiness.BuiltinTLSReason == coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES {
		return status.Error(codes.FailedPrecondition,
			"invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file")
	}
	return status.Error(codes.FailedPrecondition,
		"invites need the built-in CA: this coordinator could not create or read its CA; see its start warning")
}

// newInvite returns a new invite for the CA certificate ca, and what its
// record keeps in place of the secret.
func newInvite(ca *x509.Certificate) (invite string, secretHash []byte, err error) {
	secret := make([]byte, inviteSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return "", nil, fmt.Errorf("generate invite secret: %w", err)
	}
	invite = strings.Join([]string{inviteVersion, caPin(ca), inviteEncoding.EncodeToString(secret)}, ".")
	return invite, inviteSecretHash(secret), nil
}

// parseInvite returns the pin and the secret of invite, or false when it is
// not of the form dagu1.<pin>.<secret>.
func parseInvite(invite string) (pin string, secret []byte, ok bool) {
	if len(invite) != inviteLength {
		return "", nil, false
	}
	parts := strings.Split(invite, ".")
	if len(parts) != 3 || parts[0] != inviteVersion || len(parts[1]) != inviteFieldLength || len(parts[2]) != inviteFieldLength {
		return "", nil, false
	}
	// The pin is a SHA-256 digest, so a pin that does not decode to one
	// names no CA.
	if digest, err := inviteEncoding.DecodeString(parts[1]); err != nil || len(digest) != sha256.Size {
		return "", nil, false
	}
	secret, err := inviteEncoding.DecodeString(parts[2])
	if err != nil {
		return "", nil, false
	}
	return parts[1], secret, true
}

// caPin returns the pin of the CA certificate ca: the base64url SHA-256 of
// its DER encoding, without padding.
func caPin(ca *x509.Certificate) string {
	sum := sha256.Sum256(ca.Raw)
	return inviteEncoding.EncodeToString(sum[:])
}

// inviteSecretHash returns what an invite's record keeps in place of secret.
func inviteSecretHash(secret []byte) []byte {
	sum := sha256.Sum256(secret)
	return sum[:]
}

// validInviteAddress reports whether addr is host:port with a port a TCP
// listener can have.
func validInviteAddress(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= math.MaxUint16
}

func inviteRecord(invite persis.WorkerInvite) *coordinatorv1.InviteRecord {
	return &coordinatorv1.InviteRecord{
		Id:         invite.ID,
		Kind:       inviteKinds[invite.Kind],
		ExpiresAt:  unixSeconds(invite.ExpiresAt),
		Address:    invite.Address,
		CreatedBy:  invite.CreatedBy,
		CreatedAt:  unixSeconds(invite.CreatedAt),
		UseCount:   int32(min(invite.UseCount, math.MaxInt32)), //nolint:gosec // Bounded above.
		LastUsedAt: unixSeconds(invite.LastUsedAt),
	}
}

// unixSeconds returns t as a Unix timestamp in seconds, or 0 for the zero
// time.
func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
