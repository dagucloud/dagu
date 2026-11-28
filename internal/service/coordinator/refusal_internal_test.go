// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// Each refusal a caller acts on, as the coordinator makes it, has its name,
// also when an error wraps it.
func TestRefusalReason(t *testing.T) {
	t.Parallel()

	withoutCA := func(reason coordinatorv1.BuiltinTLSReason) error {
		h := NewHandler(HandlerConfig{JoinReadiness: JoinReadinessConfig{BuiltinTLSReason: reason}})
		_, err := h.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{})
		return err
	}
	auth := NewPeerTokenAuth("token", true, false)
	method := coordinatorv1.CoordinatorService_CreateInvite_FullMethodName
	wrongToken := metadata.NewIncomingContext(t.Context(), metadata.Pairs(peerTokenMetadataKey, "other"))

	for want, err := range map[string]error{
		"own_certificates":    withoutCA(coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES),
		"ca_unavailable":      withoutCA(coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_CA_UNAVAILABLE),
		"peer_token_missing":  auth.authorize(context.Background(), method),
		"peer_token_mismatch": auth.authorize(wrongToken, method),
		"require_tls":         fmt.Errorf("get join readiness: %w", requireTLS(context.Background(), method)),
	} {
		assert.Equal(t, want, RefusalReason(err), "%v", err)
	}
	assert.Empty(t, RefusalReason(fmt.Errorf("no coordinators available")))
}

// The reason travels as a status detail, so it survives a reworded
// message, and a coordinator of an earlier release that attaches none is
// still named by its message.
func TestRefusalReasonDetail(t *testing.T) {
	t.Parallel()

	reworded := reasonedRefusal(codes.FailedPrecondition, reasonOwnCertificates, "a reworded message")
	assert.Equal(t, reasonOwnCertificates, RefusalReason(reworded))
	assert.Equal(t, reasonCAUnavailable, RefusalReason(status.Error(codes.FailedPrecondition, caUnavailableMessage)))
	assert.Empty(t, RefusalReason(status.Error(codes.FailedPrecondition, "a reworded message")))
	assert.Equal(t, reasonPeerAuthMissing, RefusalReason(status.Error(codes.Unauthenticated, legacyPeerTokenMissing)))
}

// A call without the peer token is told the setting to change, and nothing
// about where the coordinator keeps its generated token.
func TestPeerTokenMissingGuidance(t *testing.T) {
	t.Parallel()

	err := NewPeerTokenAuth("token", true, false).authorize(context.Background(), coordinatorv1.CoordinatorService_CreateInvite_FullMethodName)
	assert.Equal(t, "the coordinator requires a peer token: set peer.token", status.Convert(err).Message())
}
