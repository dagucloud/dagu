// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// Only the coordinator's refusal of a revoked certificate counts as a
// removal, whatever wraps it.
func TestIsWorkerRemoved(t *testing.T) {
	t.Parallel()
	removed := status.Error(codes.Unauthenticated, removedWorkerMessage)
	assert.True(t, isWorkerRemoved(removed))
	assert.True(t, isWorkerRemoved(fmt.Errorf("heartbeat failed: %w", removed)))
	assert.False(t, isWorkerRemoved(status.Error(codes.PermissionDenied, removedWorkerMessage)))
	assert.False(t, isWorkerRemoved(status.Error(codes.Unauthenticated, "the peer token does not match the coordinator's peer token")))
	assert.False(t, isWorkerRemoved(errors.New(removedWorkerMessage)))
	assert.False(t, isWorkerRemoved(nil))
}

// endedStream is a client stream whose call ended with err.
type endedStream struct {
	grpc.ClientStream
	err error
}

func (s endedStream) RecvMsg(any) error { return s.err }

// A stream whose context ends before its refusal is read still reports the
// refusal: the end releases the stream on its link, and the read reports
// its status (Spec 108 §Removed Worker).
func TestStreamReportsAfterContextEnd(t *testing.T) {
	t.Parallel()
	var refusals int
	config := DefaultConfig()
	config.onRemoved = func() { refusals++ }
	link := &connLink{transport: newClientTransport(config), address: "coordinator:50055"}
	link.begin()
	s := &reportingStream{
		ClientStream: endedStream{err: status.Error(codes.Unauthenticated, removedWorkerMessage)},
		link:         link,
		ctx:          t.Context(),
		method:       coordinatorv1.CoordinatorService_StreamLogs_FullMethodName,
		peer:         &peer.Peer{},
		stop:         func() bool { return true },
	}

	s.release()
	assert.Error(t, s.RecvMsg(&emptypb.Empty{}))
	assert.Equal(t, 1, refusals)
	assert.Zero(t, link.inflight, "the stream ends on its link once")
}
