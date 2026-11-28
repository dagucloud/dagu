// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"cmp"
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// h2cCallerWindow is how long a host stays listed after its last call
	// over h2c.
	h2cCallerWindow = 10 * time.Minute
	// maxH2CCallers bounds the h2c callers a coordinator keeps.
	maxH2CCallers = 64
)

// workerMethods are the calls only workers and their runs make. The Workers
// page shows how each worker connects, so the h2c callers leave them out.
var workerMethods = map[string]bool{
	coordinatorv1.CoordinatorService_Poll_FullMethodName:                        true,
	coordinatorv1.CoordinatorService_Heartbeat_FullMethodName:                   true,
	coordinatorv1.CoordinatorService_AckTaskClaim_FullMethodName:                true,
	coordinatorv1.CoordinatorService_RunHeartbeat_FullMethodName:                true,
	coordinatorv1.CoordinatorService_ReportStatus_FullMethodName:                true,
	coordinatorv1.CoordinatorService_StreamLogs_FullMethodName:                  true,
	coordinatorv1.CoordinatorService_StreamArtifacts_FullMethodName:             true,
	coordinatorv1.CoordinatorService_GetWorkspaceBundle_FullMethodName:          true,
	coordinatorv1.CoordinatorService_GetDAG_FullMethodName:                      true,
	coordinatorv1.CoordinatorService_ClaimAgentSessionCleanup_FullMethodName:    true,
	coordinatorv1.CoordinatorService_CompleteAgentSessionCleanup_FullMethodName: true,
	coordinatorv1.CoordinatorService_ResolveSecretReference_FullMethodName:      true,
	coordinatorv1.CoordinatorService_ResolveRuntimeProfile_FullMethodName:       true,
	coordinatorv1.CoordinatorService_ResolveNodes_FullMethodName:                true,
	coordinatorv1.CoordinatorService_ResolveConnectionToken_FullMethodName:      true,
	coordinatorv1.CoordinatorService_Enroll_FullMethodName:                      true,
}

// H2CCallersClient reads the h2c callers of one coordinator.
type H2CCallersClient interface {
	// GetH2CCallers returns the hosts, other than workers, that called
	// coordinator over h2c in the last 10 minutes, the latest call first.
	GetH2CCallers(ctx context.Context, coordinator serviceregistry.HostInfo) ([]*coordinatorv1.H2CCaller, error)
}

var _ H2CCallersClient = (*clientImpl)(nil)

func (cli *clientImpl) GetH2CCallers(ctx context.Context, coordinator serviceregistry.HostInfo) ([]*coordinatorv1.H2CCaller, error) {
	c, err := cli.getOrCreateDiscoveredClient(coordinator)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.GetH2CCallers(ctx, &coordinatorv1.GetH2CCallersRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Callers, nil
}

// H2CCallers keeps, in memory, the hosts that called the coordinator over
// h2c in the last 10 minutes, other than workers' calls and health checks,
// with the h2c reason of each call. It keeps at most 64 entries and drops
// the oldest first.
type H2CCallers struct {
	now func() time.Time

	mu       sync.Mutex
	lastCall map[h2cCaller]time.Time
}

// h2cCaller is a remote host, without the port, and an h2c reason it sent.
type h2cCaller struct {
	host   string
	reason string
}

// NewH2CCallers returns an empty list of h2c callers.
func NewH2CCallers() *H2CCallers {
	return &H2CCallers{now: time.Now, lastCall: make(map[h2cCaller]time.Time)}
}

// UnaryInterceptor records the unary calls over h2c.
func (c *H2CCallers) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		c.observe(ctx, info.FullMethod)
		return handler(ctx, req)
	}
}

// StreamInterceptor records the streaming calls over h2c.
func (c *H2CCallers) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		c.observe(ss.Context(), info.FullMethod)
		return handler(srv, ss)
	}
}

func (c *H2CCallers) observe(ctx context.Context, method string) {
	if workerMethods[method] || strings.HasPrefix(method, grpcHealthMethodPrefix) {
		return
	}
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return
	}
	if _, isTLS := p.AuthInfo.(credentials.TLSInfo); isTLS {
		return
	}
	host := p.Addr.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	c.record(h2cCaller{host: host, reason: h2cReason(ctx)})
}

func (c *H2CCallers) record(caller h2cCaller) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.lastCall[caller]; !ok && len(c.lastCall) >= maxH2CCallers {
		c.expire(now)
		if len(c.lastCall) >= maxH2CCallers {
			c.dropOldest()
		}
	}
	c.lastCall[caller] = now
}

// list returns the callers of the last 10 minutes, the latest call first.
func (c *H2CCallers) list() []*coordinatorv1.H2CCaller {
	now := c.now()
	c.mu.Lock()
	c.expire(now)
	callers := make([]*coordinatorv1.H2CCaller, 0, len(c.lastCall))
	for caller, at := range c.lastCall {
		callers = append(callers, &coordinatorv1.H2CCaller{Host: caller.host, Reason: caller.reason, LastCallAt: at.Unix()})
	}
	c.mu.Unlock()
	slices.SortFunc(callers, func(a, b *coordinatorv1.H2CCaller) int {
		return cmp.Or(cmp.Compare(b.LastCallAt, a.LastCallAt), strings.Compare(a.Host, b.Host), strings.Compare(a.Reason, b.Reason))
	})
	return callers
}

// expire drops the callers whose last call is older than h2cCallerWindow.
// The caller holds mu.
func (c *H2CCallers) expire(now time.Time) {
	for caller, at := range c.lastCall {
		if now.Sub(at) > h2cCallerWindow {
			delete(c.lastCall, caller)
		}
	}
}

// dropOldest drops the caller with the oldest last call. The caller holds
// mu.
func (c *H2CCallers) dropOldest() {
	var oldest h2cCaller
	var oldestAt time.Time
	for caller, at := range c.lastCall {
		if oldestAt.IsZero() || at.Before(oldestAt) {
			oldest, oldestAt = caller, at
		}
	}
	delete(c.lastCall, oldest)
}
