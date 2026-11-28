// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
)

// peerConnection is how a call reached the coordinator.
type peerConnection struct {
	kind dispatch.WorkerConnection
	// plaintextReason is why a plaintext caller uses h2c.
	plaintextReason string
}

// callerConnection returns how the call of ctx reached the coordinator with
// the peer CA ca, nil without built-in TLS.
func callerConnection(ctx context.Context, ca *PeerCA) peerConnection {
	if p, ok := peer.FromContext(ctx); ok {
		if _, isTLS := p.AuthInfo.(credentials.TLSInfo); isTLS {
			if ca != nil && workerCertificate(ctx, ca.cert) != nil {
				return peerConnection{kind: dispatch.WorkerConnectionCertificate}
			}
			return peerConnection{kind: dispatch.WorkerConnectionTLS}
		}
	}
	return peerConnection{kind: dispatch.WorkerConnectionPlaintext, plaintextReason: h2cReason(ctx)}
}

// h2cReason returns the h2c reason a call carries. A call without one, or
// with a value outside the known reasons, reads as an older version.
func h2cReason(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	if values := md.Get(h2cReasonMetadataKey); len(values) > 0 {
		switch values[0] {
		case h2cReasonInsecure, h2cReasonNoTLS, h2cReasonNetworkPath:
			return values[0]
		}
	}
	return h2cReasonOlderVersion
}
