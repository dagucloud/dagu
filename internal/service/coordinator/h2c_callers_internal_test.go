// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const getWorkersMethod = coordinatorv1.CoordinatorService_GetWorkers_FullMethodName

// newTestH2CCallers returns h2c callers whose clock advance moves.
func newTestH2CCallers() (*H2CCallers, func(time.Duration)) {
	callers := NewH2CCallers()
	now := time.Unix(1_800_000_000, 0)
	callers.now = func() time.Time { return now }
	return callers, func(d time.Duration) { now = now.Add(d) }
}

// h2cCall returns the context of a call over h2c from host with reason,
// none when empty.
func h2cCall(host, reason string) context.Context {
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(host), Port: 40000}})
	if reason == "" {
		return ctx
	}
	return metadata.NewIncomingContext(ctx, metadata.Pairs(h2cReasonMetadataKey, reason))
}

func listedCallers(callers *H2CCallers) []string {
	var listed []string
	for _, caller := range callers.list() {
		listed = append(listed, caller.Host+" "+caller.Reason)
	}
	return listed
}

// Calls over h2c are listed by host, without the port, and reason; calls
// over TLS, workers' calls, and health checks are not (Spec 108 §h2c
// Callers).
func TestH2CCallersRecord(t *testing.T) {
	callers, advance := newTestH2CCallers()
	tlsCall := peer.NewContext(context.Background(), &peer.Peer{
		Addr:     &net.TCPAddr{IP: net.ParseIP("10.0.0.9"), Port: 40000},
		AuthInfo: credentials.TLSInfo{},
	})

	callers.observe(h2cCall("10.0.0.1", h2cReasonInsecure), getWorkersMethod)
	advance(time.Second)
	callers.observe(h2cCall("10.0.0.1", ""), coordinatorv1.CoordinatorService_Dispatch_FullMethodName)
	callers.observe(h2cCall("10.0.0.1", ""), coordinatorv1.CoordinatorService_Dispatch_FullMethodName)
	callers.observe(h2cCall("10.0.0.2", h2cReasonNoTLS), coordinatorv1.CoordinatorService_Heartbeat_FullMethodName)
	callers.observe(h2cCall("10.0.0.3", h2cReasonNoTLS), "/grpc.health.v1.Health/Check")
	callers.observe(tlsCall, getWorkersMethod)

	assert.Equal(t, []string{"10.0.0.1 older version", "10.0.0.1 peer.insecure"}, listedCallers(callers))
}

// A caller leaves the list 10 minutes after its last call, with no timer.
func TestH2CCallersExpire(t *testing.T) {
	callers, advance := newTestH2CCallers()
	callers.observe(h2cCall("10.0.0.1", h2cReasonNoTLS), getWorkersMethod)
	advance(5 * time.Minute)
	callers.observe(h2cCall("10.0.0.2", h2cReasonNoTLS), getWorkersMethod)

	advance(h2cCallerWindow - 5*time.Minute)
	assert.Equal(t, []string{"10.0.0.2 no TLS source", "10.0.0.1 no TLS source"}, listedCallers(callers))

	advance(time.Second)
	assert.Equal(t, []string{"10.0.0.2 no TLS source"}, listedCallers(callers))
}

// The list keeps at most 64 entries and drops the oldest call first.
func TestH2CCallersBound(t *testing.T) {
	callers, advance := newTestH2CCallers()
	for i := range maxH2CCallers + 1 {
		callers.observe(h2cCall(fmt.Sprintf("10.0.1.%d", i), h2cReasonNoTLS), getWorkersMethod)
		advance(time.Second)
	}

	listed := listedCallers(callers)
	require.Len(t, listed, maxH2CCallers)
	assert.Equal(t, "10.0.1.64 no TLS source", listed[0])
	assert.NotContains(t, listed, "10.0.1.0 no TLS source")
}

// GetH2CCallers returns the list with each caller's last call time, and an
// empty one from a handler that records none.
func TestGetH2CCallers(t *testing.T) {
	callers, _ := newTestH2CCallers()
	callers.observe(h2cCall("10.0.0.1", h2cReasonNetworkPath), getWorkersMethod)

	resp, err := NewHandler(HandlerConfig{H2CCallers: callers}).GetH2CCallers(t.Context(), &coordinatorv1.GetH2CCallersRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Callers, 1)
	assert.Equal(t, "10.0.0.1", resp.Callers[0].Host)
	assert.Equal(t, h2cReasonNetworkPath, resp.Callers[0].Reason)
	assert.Equal(t, int64(1_800_000_000), resp.Callers[0].LastCallAt)

	resp, err = NewHandler(HandlerConfig{}).GetH2CCallers(t.Context(), &coordinatorv1.GetH2CCallersRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Callers)
}
