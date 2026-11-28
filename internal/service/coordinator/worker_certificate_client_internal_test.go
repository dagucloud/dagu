// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/tls"
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

// The start confirmation asks the coordinators at the addresses the client
// dials, in their configured order, with the default port filled in, and
// names another Dagu by the address it dialed (Spec 108 §Worker Start).
func TestNewWorkerClientProbesRegistryAddresses(t *testing.T) {
	t.Parallel()
	config := DefaultConfig().WithWorkerCertificate(tls.Certificate{}, nil)
	var probed []string
	_, otherDagu, err := newWorkerClient(t.Context(), []string{"127.0.0.1", "coordinator-b:50056"}, config, func() {},
		func(_ context.Context, _ *Config, address string) (bool, error) {
			probed = append(probed, address)
			return false, ErrInviteCAMismatch
		})
	assert.ErrorIs(t, err, ErrInviteCAMismatch)
	assert.Equal(t, []string{"127.0.0.1:50055", "coordinator-b:50056"}, probed)
	assert.Equal(t, "127.0.0.1:50055", otherDagu)
}

// The check's answer for a certificate refused as revoked gives the next
// step for where the worker runs, as the worker's own exit does (Spec 108
// §Messages).
func TestRemovedWorkerCheckAnswer(t *testing.T) {
	t.Parallel()
	assert.Equal(t, removedWorkerCheckAnswer, removedWorkerAnswer(coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST))
	assert.Equal(t, removedWorkerCheckAnswer, removedWorkerAnswer(coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER))
	assert.Equal(t, removedWorkerCheckAnswerKubernetes, removedWorkerAnswer(coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES))
	assert.Equal(t, removedWorkerAnswer(ProcessEnvironment()), removedWorkerError{}.Error())
}
