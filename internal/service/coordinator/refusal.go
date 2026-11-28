// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// refusalDomain marks the reason a coordinator attaches to its refusals.
const refusalDomain = "coordinator.dagu"

// The reasons of the refusals that a caller acts on.
const (
	reasonOwnCertificates  = "own_certificates"
	reasonCAUnavailable    = "ca_unavailable"
	reasonPeerAuthMissing  = "peer_token_missing"
	reasonPeerAuthMismatch = "peer_token_mismatch"
	reasonRequireTLS       = "require_tls"
)

// legacyPeerTokenMissing is how coordinators of an earlier release refuse
// a call without the peer token.
const legacyPeerTokenMissing = "the coordinator requires a peer token: set peer.token, or let this process read the coordinator's data directory"

// refusal is a coordinator's refusal of a call: its code and message.
type refusal struct {
	code    codes.Code
	message string
}

// refusalReasons names the refusals of coordinators that attach no
// reason, as those of an earlier release.
var refusalReasons = map[refusal]string{
	{codes.FailedPrecondition, ownCertificatesMessage}: reasonOwnCertificates,
	{codes.FailedPrecondition, caUnavailableMessage}:   reasonCAUnavailable,
	{codes.Unauthenticated, peerTokenMissingMessage}:   reasonPeerAuthMissing,
	{codes.Unauthenticated, legacyPeerTokenMissing}:    reasonPeerAuthMissing,
	{codes.Unauthenticated, peerTokenMismatchMessage}:  reasonPeerAuthMismatch,
	{codes.Unauthenticated, requireTLSMessage}:         reasonRequireTLS,
}

// reasonedRefusal returns a refusal with code and message that carries reason,
// so a caller does not depend on the message.
func reasonedRefusal(code codes.Code, reason, message string) error {
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: refusalDomain})
	if err != nil {
		return status.Error(code, message)
	}
	return st.Err()
}

// RefusalReason returns why a coordinator refused an invite, removal, or
// join readiness call, as a stable name such as "own_certificates" or
// "require_tls", or "" for an error it does not name.
func RefusalReason(err error) string {
	// The status itself, not one that FromError builds around the message
	// of an error that wraps it.
	var withStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &withStatus) {
		return ""
	}
	st := withStatus.GRPCStatus()
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Domain == refusalDomain {
			return info.Reason
		}
	}
	return refusalReasons[refusal{st.Code(), st.Message()}]
}
