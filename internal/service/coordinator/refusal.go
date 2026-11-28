// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// refusal is a coordinator's refusal of a call: its code and message.
type refusal struct {
	code    codes.Code
	message string
}

// refusalReasons names the refusals of the invite, removal, and join
// readiness calls that a caller acts on.
var refusalReasons = map[refusal]string{
	{codes.FailedPrecondition, ownCertificatesMessage}: "own_certificates",
	{codes.FailedPrecondition, caUnavailableMessage}:   "ca_unavailable",
	{codes.Unauthenticated, peerTokenMissingMessage}:   "peer_token_missing",
	{codes.Unauthenticated, peerTokenMismatchMessage}:  "peer_token_mismatch",
	{codes.Unauthenticated, requireTLSMessage}:         "require_tls",
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
	return refusalReasons[refusal{st.Code(), st.Message()}]
}
