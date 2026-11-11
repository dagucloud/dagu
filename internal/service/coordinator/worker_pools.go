// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Spec 092, "Worker pools". The identity of a caller is the subject Common Name
// of the verified leaf client certificate. With pools, each call of a worker
// must come from an identity in a pool, each label that the worker announces
// must be allowed by the pool, and each call of an attempt must come from the
// pool that claimed it.

// workerMethods lists the calls of a worker. The other calls need only a valid
// client certificate.
var workerMethods = map[string]struct{}{
	coordinatorv1.CoordinatorService_Poll_FullMethodName:                        {},
	coordinatorv1.CoordinatorService_Heartbeat_FullMethodName:                   {},
	coordinatorv1.CoordinatorService_AckTaskClaim_FullMethodName:                {},
	coordinatorv1.CoordinatorService_RunHeartbeat_FullMethodName:                {},
	coordinatorv1.CoordinatorService_ReportStatus_FullMethodName:                {},
	coordinatorv1.CoordinatorService_StreamLogs_FullMethodName:                  {},
	coordinatorv1.CoordinatorService_StreamArtifacts_FullMethodName:             {},
	coordinatorv1.CoordinatorService_ResolveSecretReference_FullMethodName:      {},
	coordinatorv1.CoordinatorService_ResolveRuntimeProfile_FullMethodName:       {},
	coordinatorv1.CoordinatorService_ClaimAgentSessionCleanup_FullMethodName:    {},
	coordinatorv1.CoordinatorService_CompleteAgentSessionCleanup_FullMethodName: {},
	coordinatorv1.CoordinatorService_CheckAIEndpoint_FullMethodName:             {},
	coordinatorv1.CoordinatorService_RelayAIRequest_FullMethodName:              {},
}

// IsWorkerMethod reports whether the full gRPC method name is a call of a worker.
func IsWorkerMethod(fullMethod string) bool {
	_, ok := workerMethods[fullMethod]
	return ok
}

var errNoVerifiedCertificate = errors.New("the call has no verified client certificate")

// WorkerPools is the typed form of coordinator.worker_pools. A nil value and a
// value with no pool mean that the coordinator has no worker pools.
type WorkerPools struct {
	pools       []config.WorkerPool
	byIdentity  map[string]*config.WorkerPool
	dispatchers map[string]struct{}
	// leases tells whether a pool holds a fresh lease that matches. The
	// handler sets it. Without it, only a dispatcher makes the calls that are
	// not calls of a worker.
	leases atomic.Pointer[leaseHolder]
}

// leaseHolder reports whether the pool holds a fresh lease that matches.
type leaseHolder func(ctx context.Context, pool string, match func(dispatch.DAGRunLease) bool) (bool, error)

// NewWorkerPools checks the pools against the rules that need the other parts of
// Dagu: a label agent.provider.<P> names a CLI provider, each other label is a
// valid label, the coordinator has mutual TLS, and the dispatchers are set and
// are in no pool. It returns a value with no pool when defs is empty.
func NewWorkerPools(defs []config.WorkerPool, dispatchers []string, peerCfg config.Peer) (*WorkerPools, error) {
	pools := &WorkerPools{byIdentity: map[string]*config.WorkerPool{}, dispatchers: map[string]struct{}{}}
	if len(defs) == 0 {
		if len(dispatchers) > 0 {
			return nil, fmt.Errorf("config: key %q needs coordinator.worker_pools", "coordinator.dispatchers")
		}
		return pools, nil
	}
	if len(dispatchers) == 0 {
		return nil, fmt.Errorf("config: key %q is not set: coordinator.worker_pools needs the certificate identities of the dispatchers", "coordinator.dispatchers")
	}
	for _, key := range []struct {
		name  string
		value string
	}{
		{"peer.cert_file", peerCfg.CertFile},
		{"peer.key_file", peerCfg.KeyFile},
		{"peer.client_ca_file", peerCfg.ClientCaFile},
	} {
		if strings.TrimSpace(key.value) == "" {
			return nil, fmt.Errorf("coordinator.worker_pools: key %q is not set: worker pools need mutual TLS with a verified client certificate", key.name)
		}
	}
	pools.pools = make([]config.WorkerPool, len(defs))
	for i, def := range defs {
		field := fmt.Sprintf("coordinator.worker_pools[%d]", i)
		for _, key := range slices.Sorted(maps.Keys(def.Labels)) {
			value := def.Labels[key]
			if provider, ok := strings.CutPrefix(key, ir.AgentProviderLabelPrefix); ok {
				if !ir.IsAgentCLIProvider(provider) {
					return nil, fmt.Errorf("config: key %q: %q is not a CLI provider: expected one of %s",
						field+".labels."+key, provider, strings.Join(ir.AgentCLIProviderNames(), ", "))
				}
				continue
			}
			label := ir.Label{Key: key, Value: value}
			if value == config.WorkerPoolAnyValue {
				label.Value = ""
			}
			if err := ir.ValidateLabel(label); err != nil {
				return nil, fmt.Errorf("config: key %q: %w", field+".labels."+key, err)
			}
		}
		pools.pools[i] = config.WorkerPool{
			Name:       def.Name,
			Identities: slices.Clone(def.Identities),
			Labels:     maps.Clone(def.Labels),
		}
	}
	for i := range pools.pools {
		for _, identity := range pools.pools[i].Identities {
			if _, dup := pools.byIdentity[identity]; dup {
				return nil, fmt.Errorf("config: key %q: identity %q is in two pools: an identity is in one pool only",
					"coordinator.worker_pools", identity)
			}
			pools.byIdentity[identity] = &pools.pools[i]
		}
	}
	for _, identity := range dispatchers {
		if pool, member := pools.byIdentity[identity]; member {
			return nil, fmt.Errorf("config: key %q: identity %q is a dispatcher and a member of the pool %q: an identity has one role",
				"coordinator.dispatchers", identity, pool.Name)
		}
		pools.dispatchers[identity] = struct{}{}
	}
	return pools, nil
}

// Dispatchers returns the identities of the dispatchers, sorted.
func (p *WorkerPools) Dispatchers() []string {
	if p == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(p.dispatchers))
}

func (p *WorkerPools) setLeaseHolder(holder leaseHolder) {
	if p != nil && holder != nil {
		p.leases.Store(&holder)
	}
}

// Enabled reports whether the coordinator has worker pools.
func (p *WorkerPools) Enabled() bool {
	return p != nil && len(p.pools) > 0
}

// certificateIdentity returns the subject Common Name of the verified leaf
// client certificate of the call. A call without a verified chain has no
// identity.
func certificateIdentity(ctx context.Context) (string, error) {
	pr, ok := peer.FromContext(ctx)
	if !ok || pr == nil {
		return "", errNoVerifiedCertificate
	}
	info, ok := pr.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", errNoVerifiedCertificate
	}
	chains := info.State.VerifiedChains
	if len(chains) == 0 || len(chains[0]) == 0 || chains[0][0] == nil {
		return "", errNoVerifiedCertificate
	}
	cn := chains[0][0].Subject.CommonName
	if cn == "" {
		return "", errors.New("the verified client certificate has no subject Common Name")
	}
	return cn, nil
}

// callerPool returns the pool of the identity of the call. It fails with
// PermissionDenied when the call has no identity or the identity is in no pool.
// It returns nil and no error when the coordinator has no pools.
func (p *WorkerPools) callerPool(ctx context.Context) (*config.WorkerPool, error) {
	if !p.Enabled() {
		return nil, nil
	}
	identity, err := certificateIdentity(ctx)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	pool, ok := p.byIdentity[identity]
	if !ok {
		return nil, status.Errorf(codes.PermissionDenied, "certificate identity %q is in no worker pool", identity)
	}
	return pool, nil
}

// authorizeLabels checks the labels that a worker announces in a poll or a
// heartbeat. Without pools, a label that starts with agent. is refused. With
// pools, each label must be allowed by the pool of the caller, and a worker of
// an agent pool must announce the label of its provider.
func (p *WorkerPools) authorizeLabels(pool *config.WorkerPool, labels map[string]string) error {
	keys := slices.Sorted(maps.Keys(labels))
	if !p.Enabled() {
		for _, key := range keys {
			if ir.IsAgentLabel(key) {
				return status.Errorf(codes.PermissionDenied, "label %q needs coordinator.worker_pools", key)
			}
		}
		return nil
	}
	if pool == nil {
		return status.Error(codes.PermissionDenied, errNoVerifiedCertificate.Error())
	}
	for _, key := range keys {
		value := labels[key]
		if key == "os" || key == "arch" {
			continue
		}
		allowed, ok := pool.Labels[key]
		if !ok || (allowed != config.WorkerPoolAnyValue && allowed != value) {
			return status.Errorf(codes.PermissionDenied, "label %q with the value %q is not allowed by the worker pool %q", key, value, pool.Name)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(pool.Labels)) {
		if !strings.HasPrefix(key, ir.AgentProviderLabelPrefix) {
			continue
		}
		if labels[key] != pool.Labels[key] {
			return status.Errorf(codes.PermissionDenied, "the worker pool %q is an agent pool: the worker must announce the label %q with the value %q",
				pool.Name, key, pool.Labels[key])
		}
	}
	return nil
}

// UnaryServerInterceptor refuses a call of a worker whose identity is in no
// pool, and another call from an identity that may not make it (see
// authorizeOtherCall). It passes each call through when the coordinator has no
// pools.
func (p *WorkerPools) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if p.Enabled() && info != nil {
			if err := p.authorizeCall(ctx, info.FullMethod, req); err != nil {
				return nil, err
			}
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor is the stream form of UnaryServerInterceptor. For a
// stream that starts with a request, the check reads the first message.
func (p *WorkerPools) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !p.Enabled() || info == nil {
			return handler(srv, ss)
		}
		if info.FullMethod == coordinatorv1.CoordinatorService_GetWorkspaceBundle_FullMethodName {
			return handler(srv, &firstMessageCheck{ServerStream: ss, check: func(msg any) error {
				return p.authorizeCall(ss.Context(), info.FullMethod, msg)
			}})
		}
		if err := p.authorizeCall(ss.Context(), info.FullMethod, nil); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// firstMessageCheck runs the check on the first message that the server reads.
type firstMessageCheck struct {
	grpc.ServerStream
	check   func(any) error
	checked bool
}

func (s *firstMessageCheck) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	if s.checked {
		return nil
	}
	s.checked = true
	return s.check(m)
}

const coordinatorServicePrefix = "/coordinator.v1.CoordinatorService/"

// authorizeCall applies the rules of the pools to one call. A call of a worker
// needs an identity in a pool. Another call of the coordinator service needs a
// dispatcher, or a pool that holds a fresh lease that the call concerns. A call
// of another service, such as the health service, passes.
func (p *WorkerPools) authorizeCall(ctx context.Context, fullMethod string, req any) error {
	if IsWorkerMethod(fullMethod) {
		_, err := p.callerPool(ctx)
		return err
	}
	if !strings.HasPrefix(fullMethod, coordinatorServicePrefix) {
		return nil
	}
	return p.authorizeOtherCall(ctx, fullMethod, req)
}

// authorizeOtherCall checks a call that is not a call of a worker (Spec 092,
// "Behavior"). A dispatcher makes each such call. A worker makes only the calls
// for its own runs:
//   - the dispatch of a child, and the status and the cancel of a run: the pool
//     holds a fresh lease of the same root run;
//   - the read of a workspace bundle: the pool holds a fresh lease of the bundle;
//   - a DAG, the state, the check or the write of a bundle, and the list of the
//     workers: the pool holds a fresh lease.
//
// Each other call, and the dispatch of a root run, needs a dispatcher.
func (p *WorkerPools) authorizeOtherCall(ctx context.Context, fullMethod string, req any) error {
	identity, err := certificateIdentity(ctx)
	if err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	if _, ok := p.dispatchers[identity]; ok {
		return nil
	}
	pool, inPool := p.byIdentity[identity]
	if !inPool {
		return status.Errorf(codes.PermissionDenied, "certificate identity %q is not a dispatcher and is in no worker pool", identity)
	}
	needsDispatcher := status.Errorf(codes.PermissionDenied, "certificate identity %q is not a dispatcher: the call %s needs a dispatcher", identity, fullMethod)

	anyLease := func(dispatch.DAGRunLease) bool { return true }
	var (
		match func(dispatch.DAGRunLease) bool
		what  string
	)
	switch r := req.(type) {
	case *coordinatorv1.DispatchRequest:
		task := r.GetTask()
		root := ir.NewDAGRunRef(task.GetRootDagRunName(), task.GetRootDagRunId())
		if root.Zero() || (root.ID == task.GetDagRunId() && task.GetParentDagRunId() == "") {
			return status.Errorf(codes.PermissionDenied, "certificate identity %q is not a dispatcher: a worker cannot dispatch a root run", identity)
		}
		match, what = sameRootTree(root), " of the root run "+root.String()
	case *coordinatorv1.GetDAGRunStatusRequest:
		root := runTreeRoot(r.GetRootDagRunName(), r.GetRootDagRunId(), r.GetDagName(), r.GetDagRunId())
		match, what = sameRootTree(root), " of the root run "+root.String()
	case *coordinatorv1.RequestCancelRequest:
		root := runTreeRoot(r.GetRootDagRunName(), r.GetRootDagRunId(), r.GetDagName(), r.GetDagRunId())
		match, what = sameRootTree(root), " of the root run "+root.String()
	case *coordinatorv1.GetWorkspaceBundleRequest:
		digest := r.GetDigest()
		match = func(lease dispatch.DAGRunLease) bool { return digest != "" && lease.WorkspaceBundleDigest == digest }
		what = " of the workspace bundle " + digest
	case *coordinatorv1.GetDAGRequest, *coordinatorv1.GetStateRequest, *coordinatorv1.PutStateRequest,
		*coordinatorv1.DeleteStateRequest, *coordinatorv1.ListStateRequest, *coordinatorv1.HasWorkspaceBundleRequest,
		*coordinatorv1.GetWorkersRequest:
		match = anyLease
	default:
		if fullMethod != coordinatorv1.CoordinatorService_PutWorkspaceBundle_FullMethodName {
			return needsDispatcher
		}
		match = anyLease
	}

	holder := p.leases.Load()
	if holder == nil {
		return needsDispatcher
	}
	held, err := (*holder)(ctx, pool.Name, match)
	if err != nil {
		return status.Error(codes.Internal, "failed to load distributed run leases: "+err.Error())
	}
	if !held {
		return status.Errorf(codes.PermissionDenied, "certificate identity %q is not a dispatcher, and the worker pool %q holds no fresh lease%s",
			identity, pool.Name, what)
	}
	return nil
}

// runTreeRoot returns the root run of a request: the root that it names, or the
// run itself.
func runTreeRoot(rootName, rootID, name, id string) ir.DAGRunRef {
	if rootID != "" {
		return ir.NewDAGRunRef(rootName, rootID)
	}
	return ir.NewDAGRunRef(name, id)
}

func sameRootTree(root ir.DAGRunRef) func(dispatch.DAGRunLease) bool {
	return func(lease dispatch.DAGRunLease) bool {
		leaseRoot := lease.Root
		if leaseRoot.Zero() {
			leaseRoot = lease.DAGRun
		}
		return !root.Zero() && leaseRoot == root
	}
}

// poolHoldsFreshLease reports whether the pool holds a fresh lease that
// matches. It is the lease holder of the pools of the handler.
func (h *Handler) poolHoldsFreshLease(ctx context.Context, pool string, match func(dispatch.DAGRunLease) bool) (bool, error) {
	if h.dagRunLeaseStore == nil {
		return false, nil
	}
	leases, err := h.dagRunLeaseStore.ListAll(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	for _, lease := range leases {
		if lease.WorkerPool == pool && lease.IsFresh(now, h.staleLeaseThreshold) && match(lease) {
			return true, nil
		}
	}
	return false, nil
}

// ServerOptions returns the gRPC server options that install the interceptors.
// It returns nil when the coordinator has no pools.
func (p *WorkerPools) ServerOptions() []grpc.ServerOption {
	if !p.Enabled() {
		return nil
	}
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(p.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(p.StreamServerInterceptor()),
	}
}

// Pools returns a copy of the pools, in the order of the configuration.
func (p *WorkerPools) Pools() []config.WorkerPool {
	if p == nil {
		return nil
	}
	out := make([]config.WorkerPool, len(p.pools))
	for i, pool := range p.pools {
		out[i] = config.WorkerPool{Name: pool.Name, Identities: slices.Clone(pool.Identities), Labels: maps.Clone(pool.Labels)}
	}
	return out
}

func poolName(pool *config.WorkerPool) string {
	if pool == nil {
		return ""
	}
	return pool.Name
}

// callerPoolName returns the name of the pool of the caller, or an empty string
// when the coordinator has no pools.
func (h *Handler) callerPoolName(ctx context.Context) (string, error) {
	pool, err := h.workerPools.callerPool(ctx)
	if err != nil {
		return "", err
	}
	return poolName(pool), nil
}

// authorizeWorkerLabels checks the labels of a poll or a heartbeat before the
// coordinator keeps them. It returns the name of the pool of the caller.
func (h *Handler) authorizeWorkerLabels(ctx context.Context, labels map[string]string) (string, error) {
	pool, err := h.workerPools.callerPool(ctx)
	if err != nil {
		return "", err
	}
	if err := h.workerPools.authorizeLabels(pool, labels); err != nil {
		return "", err
	}
	return poolName(pool), nil
}

// checkWorkerIDPool binds a worker ID to the pool of its last fresh heartbeat.
// A call that names the worker ID from another pool fails with
// PermissionDenied. With requireRecord, a worker ID with no fresh heartbeat
// fails too. Without pools, the function checks nothing.
func (h *Handler) checkWorkerIDPool(ctx context.Context, workerID, pool string, requireRecord bool) error {
	if !h.workerPools.Enabled() {
		return nil
	}
	recordPool, fresh, err := h.workerIDPool(ctx, workerID)
	if err != nil {
		return err
	}
	switch {
	case fresh && recordPool != pool:
		return status.Errorf(codes.PermissionDenied, "worker %q belongs to the worker pool %q, and the call comes from the worker pool %q",
			workerID, recordPool, pool)
	case !fresh && requireRecord:
		return status.Errorf(codes.PermissionDenied, "worker %q has no fresh heartbeat of the worker pool %q", workerID, pool)
	}
	return nil
}

// checkCallerWorkerID requires a fresh heartbeat of the worker ID from the pool
// of the caller.
func (h *Handler) checkCallerWorkerID(ctx context.Context, workerID string) error {
	pool, err := h.callerPoolName(ctx)
	if err != nil {
		return err
	}
	return h.checkWorkerIDPool(ctx, workerID, pool, true)
}

// workerIDPool returns the pool of the last heartbeat of the worker ID, and
// whether that heartbeat is fresh.
func (h *Handler) workerIDPool(ctx context.Context, workerID string) (string, bool, error) {
	threshold := h.staleHeartbeatThreshold
	if h.workerHeartbeatStore != nil {
		record, err := h.workerHeartbeatStore.Get(ctx, workerID)
		if errors.Is(err, dispatch.ErrWorkerHeartbeatNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, status.Error(codes.Internal, "failed to load worker heartbeat: "+err.Error())
		}
		return record.WorkerPool, dispatch.WorkerHeartbeatFresh(*record, time.Now(), threshold), nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	info, ok := h.heartbeats[workerID]
	if !ok || info == nil {
		return "", false, nil
	}
	return info.workerPool, time.Since(info.lastHeartbeatAt) <= threshold, nil
}

// checkLeasePool refuses a call of an attempt from a pool other than the pool
// that claimed the attempt. Without pools, the lease must have no pool either.
func (h *Handler) checkLeasePool(ctx context.Context, leasePool string) error {
	callerPool, err := h.callerPoolName(ctx)
	if err != nil {
		return err
	}
	if callerPool != leasePool {
		return status.Errorf(codes.PermissionDenied, "the attempt belongs to the worker pool %q, and the call comes from the worker pool %q",
			leasePool, callerPool)
	}
	return nil
}
