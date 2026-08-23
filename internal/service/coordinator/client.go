// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/queue"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Client abstracts handling communication with the coordinator service using
// service registry and gRPC.
type Client interface {
	dispatch.Dispatcher

	// Poll retrieves a task from the coordinator.
	Poll(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error)

	// GetWorkers retrieves the list of workers from the coordinator
	GetWorkers(ctx context.Context) ([]*coordinatorv1.WorkerInfo, error)

	// Heartbeat sends a heartbeat to the coordinator and returns the response
	// which may include cancellation directives
	Heartbeat(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)

	// AckTaskClaim confirms a claimed task with its owner coordinator.
	AckTaskClaimTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error)

	// RunHeartbeat refreshes leases for tasks owned by a specific coordinator.
	RunHeartbeatTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error)

	// ReportStatus sends a worker status update to the coordinator.
	ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)

	// ReportStatusTo sends a status update to a specific owner coordinator.
	ReportStatusTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)

	// StreamLogs returns a log streaming client for sending logs to the coordinator
	StreamLogs(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error)

	// StreamLogsTo opens a log stream to a specific owner coordinator.
	StreamLogsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error)

	// StreamArtifacts returns an artifact streaming client for sending artifacts to the coordinator.
	StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)

	// StreamArtifactsTo opens an artifact stream to a specific owner coordinator.
	StreamArtifactsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)

	// RequestCancel requests cancellation of a DAG run through the coordinator.
	// Used by worker sub-DAG cancellation.
	RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) error

	// GetDAGRunStatus is inherited from execution.Dispatcher

	// GetDAG retrieves a DAG definition (raw YAML) from the coordinator's DAG store.
	// Used as a fallback when a worker's local DAG store misses a definition.
	GetDAG(ctx context.Context, name string) (string, error)

	// Metrics returns the metrics for the coordinator client
	Metrics() Metrics
}

// StateClient exposes coordinator-backed persistent DAG state RPCs.
type StateClient interface {
	// GetState retrieves one state entry by reference.
	GetState(ctx context.Context, req *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error)
	// PutState creates or updates one state entry.
	PutState(ctx context.Context, req *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error)
	// DeleteState removes one state entry by reference.
	DeleteState(ctx context.Context, req *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error)
	// ListState lists state entries under a scope, namespace, and key prefix.
	ListState(ctx context.Context, req *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error)
}

// AgentSessionCleanupClient exposes coordinator-backed provider cleanup RPCs.
type AgentSessionCleanupClient interface {
	ClaimAgentSessionCleanup(ctx context.Context, req *coordinatorv1.ClaimAgentSessionCleanupRequest) (*coordinatorv1.ClaimAgentSessionCleanupResponse, error)
	CompleteAgentSessionCleanupTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.CompleteAgentSessionCleanupRequest) (*coordinatorv1.CompleteAgentSessionCleanupResponse, error)
}

// Metrics defines the metrics for the coordinator client
type Metrics struct {
	FailCount        int   // Total number of failures
	IsConnected      bool  // Whether the client is currently connected
	ConsecutiveFails int   // Number of consecutive failures
	LastError        error // Last error encountered
}

var (
	_ Client                    = (*clientImpl)(nil)
	_ AgentSessionCleanupClient = (*clientImpl)(nil)
	_ SecretReferenceClient     = (*clientImpl)(nil)
	_ dispatch.Dispatcher       = (*clientImpl)(nil)
)

const (
	legacyClaimOwnerEndpointRejection = "claim belongs to a different coordinator endpoint"
	legacyRunHeartbeatOwnerRejection  = "run heartbeat sent to non-owner coordinator"
	legacyStatusUpdateOwnerRejection  = "status update sent to non-owner coordinator"
	legacyLogChunkOwnerRejection      = "log chunk sent to non-owner coordinator"
	legacyArtifactChunkOwnerRejection = "artifact chunk sent to non-owner coordinator"
)

// clientImpl is the concrete implementation
type clientImpl struct {
	config   *Config
	registry serviceregistry.ServiceRegistry

	clientsMu sync.RWMutex
	clients   map[string]*client // Cache of gRPC clients by coordinator ID

	ownerRoutesMu sync.RWMutex
	ownerRoutes   map[string]serviceregistry.HostInfo
	ownerFailures map[string]string

	stateMu sync.RWMutex // Mutex for state access
	state   *Metrics     // Connection state tracking

	stateCoordinatorMu sync.Mutex
	stateCoordinators  map[string]pinnedStateCoordinator
}

type pinnedStateCoordinator struct {
	member    serviceregistry.HostInfo
	memberKey string
	lastUsed  time.Time
}

// client holds the gRPC connection and clients for one coordinator endpoint.
// The connection is closed when the endpoint is replaced or during cleanup.
type client struct {
	address      string
	startedAt    time.Time
	conn         *grpc.ClientConn
	client       coordinatorv1.CoordinatorServiceClient
	healthClient grpc_health_v1.HealthClient
}

type ownerLogStream struct {
	coordinatorv1.CoordinatorService_StreamLogsClient
	client *clientImpl
	owner  serviceregistry.HostInfo
	member serviceregistry.HostInfo
}

func (s *ownerLogStream) Send(chunk *coordinatorv1.LogChunk) error {
	err := s.CoordinatorService_StreamLogsClient.Send(chunk)
	return s.client.ownerStreamError(s.owner, s.member, err)
}

func (s *ownerLogStream) CloseAndRecv() (*coordinatorv1.StreamLogsResponse, error) {
	resp, err := s.CoordinatorService_StreamLogsClient.CloseAndRecv()
	return resp, s.client.ownerStreamError(s.owner, s.member, err)
}

type ownerArtifactStream struct {
	coordinatorv1.CoordinatorService_StreamArtifactsClient
	client *clientImpl
	owner  serviceregistry.HostInfo
	member serviceregistry.HostInfo
}

func (s *ownerArtifactStream) Send(chunk *coordinatorv1.ArtifactChunk) error {
	err := s.CoordinatorService_StreamArtifactsClient.Send(chunk)
	return s.client.ownerStreamError(s.owner, s.member, err)
}

func (s *ownerArtifactStream) CloseAndRecv() (*coordinatorv1.StreamArtifactsResponse, error) {
	resp, err := s.CoordinatorService_StreamArtifactsClient.CloseAndRecv()
	return resp, s.client.ownerStreamError(s.owner, s.member, err)
}

// grpcMaxMsgSize is the maximum message size for gRPC calls.
// Default gRPC limit is 4 MB; we increase to 16 MB to handle large status
// payloads that include LLM session messages from workers.
const grpcMaxMsgSize = 16 * 1024 * 1024

const maxPinnedStateCoordinators = 1024

// Errors
var (
	ErrMissingTLSConfig = fmt.Errorf("TLS enabled but no certificates provided")
)

// New creates a new coordinator client with the given configuration
func New(registry serviceregistry.ServiceRegistry, config *Config) Client {
	return &clientImpl{
		config:            config,
		registry:          registry,
		clients:           make(map[string]*client),
		ownerRoutes:       make(map[string]serviceregistry.HostInfo),
		ownerFailures:     make(map[string]string),
		stateCoordinators: make(map[string]pinnedStateCoordinator),
		state: &Metrics{
			IsConnected: true, // Assume connected initially
		},
	}
}

// Dispatch sends a task to the coordinator.
func (cli *clientImpl) Dispatch(ctx context.Context, req dispatch.DispatchRequest) error {
	task := req.Task
	if task == nil {
		return fmt.Errorf("dispatch task is nil")
	}
	if err := cli.prepareTaskWorkspace(ctx, task); err != nil {
		return err
	}
	protoTask, err := convert.DispatchTaskToProto(task)
	if err != nil {
		return err
	}

	logger.Info(ctx, "Client dispatching task",
		slog.String("operation", task.Operation.String()),
		tag.RunID(task.DAGRunID),
		tag.Target(task.Target),
	)

	// Set up retry policy
	basePolicy := backoff.NewExponentialBackoffPolicy(cli.config.RetryInterval)
	basePolicy.BackoffFactor = 2.0
	basePolicy.MaxInterval = 30 * time.Second
	basePolicy.MaxRetries = cli.config.MaxRetries

	policy := backoff.WithJitter(basePolicy, backoff.FullJitter)

	return backoff.Retry(ctx, func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		members, err := cli.getCoordinatorMembers(ctx)
		if err != nil {
			return err
		}

		return cli.attemptCall(ctx, members, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
			// Create request
			protoReq := &coordinatorv1.DispatchRequest{
				Task:                      protoTask,
				AdmissionReservationToken: req.AdmissionReservationToken,
			}

			// Apply request timeout
			dispatchCtx, cancel := context.WithTimeout(ctx, cli.config.RequestTimeout)
			defer cancel()

			// Try to dispatch
			if _, err := client.client.Dispatch(dispatchCtx, protoReq); err != nil {
				logger.Warn(ctx, "Failed to dispatch task to coordinator",
					tag.RunID(task.DAGRunID),
					tag.Target(task.Target),
					slog.Any("worker-selector", task.WorkerSelector),
					slog.String("coordinator-id", member.ID),
				)

				wrapped := fmt.Errorf("failed to dispatch task to coordinator %s: %w", member.ID, err)

				// FailedPrecondition means permanent misconfiguration (e.g. selector mismatch).
				// Stop retrying across coordinators and across the outer backoff loop.
				if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition {
					if staleErr, ok := queue.ParseStaleQueueDispatchError(st.Message()); ok {
						return backoff.PermanentError(fmt.Errorf("failed to dispatch task to coordinator %s: %w", member.ID, staleErr))
					}
					return backoff.PermanentError(wrapped)
				}

				// Unavailable and other transient errors will be retried.
				return wrapped
			}

			logger.Info(ctx, "Task dispatched successfully",
				tag.RunID(task.DAGRunID),
				tag.Target(task.Target),
				slog.Any("worker-selector", task.WorkerSelector),
				slog.String("coordinator-id", member.ID),
			)

			return nil
		})
	}, policy, nil)
}

func (cli *clientImpl) prepareTaskWorkspace(ctx context.Context, task *dispatch.DispatchTask) error {
	if task.WorkspaceBundleDigest != "" || strings.TrimSpace(task.Definition) == "" {
		return nil
	}

	loadOpts := []spec.LoadOption{
		spec.WithName(task.Target),
	}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	if task.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(task.Params))
	}
	var dag *ir.DAG
	var err error
	if task.SourceFile != "" {
		dag, err = spec.LoadYAMLAt(ctx, []byte(task.Definition), task.SourceFile, loadOpts...)
	} else {
		dag, err = spec.LoadYAML(ctx, []byte(task.Definition), loadOpts...)
	}
	if err != nil {
		return fmt.Errorf("load DAG for file dependency snapshot: %w", err)
	}
	desc, archivePath, err := runtimeexec.PrepareDAGWorkspaceFile(dag, cli.config.WorkspaceBundleDir)
	if err != nil {
		return err
	}
	if desc == nil {
		return nil
	}
	defer func() { _ = fileutil.Remove(archivePath) }()
	if err := cli.putWorkspaceBundleFile(ctx, *desc, archivePath); err != nil {
		return fmt.Errorf("upload DAG file dependencies: %w", err)
	}
	runtimeexec.WithWorkspaceBundle(*desc)(task)
	return nil
}

// Poll implements Client.
func (cli *clientImpl) Poll(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	var task *coordinatorv1.Task
	err := backoff.Retry(ctx, func(ctx context.Context) error {
		members, err := cli.getCoordinatorMembers(ctx)
		if err != nil {
			return err
		}

		return cli.attemptCall(ctx, members, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
			resp, err := client.client.Poll(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to poll task from coordinator %s: %w", member.ID, err)
			}

			if resp.Task != nil {
				task = resp.Task
				logger.Info(ctx, "Task polled successfully",
					tag.RunID(task.DagRunId),
					tag.Target(task.Target),
					slog.Any("worker-selector", task.WorkerSelector),
					slog.String("coordinator-id", member.ID),
				)
			}

			return nil
		})

	}, policy, nil)

	return task, err
}

// Metrics implements Client.
func (cli *clientImpl) Metrics() Metrics {
	cli.stateMu.RLock()
	defer cli.stateMu.RUnlock()

	return *cli.state
}

func (cli *clientImpl) attemptCall(ctx context.Context, members []serviceregistry.HostInfo, callback func(ctx context.Context, member serviceregistry.HostInfo, client *client) error) error {
	// Shuffle members to distribute load evenly
	shuffleCoordinatorMembers(members)

	// Try each coordinator in order (round-robin style)
	var lastErr error
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Get or create client for this coordinator
		client, err := cli.getOrCreateDiscoveredClient(member)
		if err != nil {
			logger.Warn(ctx, "Failed to connect to coordinator",
				slog.String("coordinator-id", member.ID),
				tag.Host(member.Host),
				tag.Port(member.Port),
				tag.Error(err))
			cli.recordFailure(err)
			lastErr = err
			continue
		}

		// Check if the coordinator is healthy
		if err := cli.isHealthy(ctx, client); err != nil {
			logger.Warn(ctx, "Failed to check coordinator health",
				slog.String("coordinator-id", member.ID),
				tag.Host(member.Host),
				tag.Port(member.Port),
				tag.Error(err))
			cli.recordFailure(err)
			lastErr = err
			continue
		}

		// Create request
		if err := callback(ctx, member, client); err != nil {
			logger.Debug(ctx, "Failed to dispatch to coordinator",
				slog.String("coordinator-id", member.ID),
				tag.Host(member.Host),
				tag.Port(member.Port),
				tag.Error(err))
			cli.recordFailure(err)

			// Permanent errors (e.g. selector mismatch) should not try other coordinators.
			if errors.Is(err, backoff.ErrPermanent) {
				return err
			}
			lastErr = err
		} else {
			// Success - record and return immediately
			cli.recordSuccess(ctx)
			return nil
		}
	}

	return lastErr
}

// callPinnedStateCoordinator keeps all state RPCs on one coordinator for this
// client because file-backed state can be local to each coordinator.
func (cli *clientImpl) callPinnedStateCoordinator(ctx context.Context, routingKey string, callback func(ctx context.Context, member serviceregistry.HostInfo, client *client) error) error {
	member, err := cli.pinnedStateCoordinator(ctx, routingKey)
	if err != nil {
		return err
	}

	err = cli.callMemberWithTimeout(ctx, member, func(ctx context.Context, client *client) error {
		return callback(ctx, member, client)
	})
	if shouldRefreshPinnedStateCoordinator(err) {
		cli.refreshPinnedStateCoordinator(ctx, routingKey, member)
	}
	return err
}

func shouldRefreshPinnedStateCoordinator(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	code := st.Code()
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}

func (cli *clientImpl) pinnedStateCoordinator(ctx context.Context, routingKey string) (serviceregistry.HostInfo, error) {
	if member, ok := cli.cachedPinnedStateCoordinator(routingKey); ok {
		return member, nil
	}

	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return serviceregistry.HostInfo{}, err
	}

	member, err := selectStateCoordinatorOwner(members, routingKey)
	if err != nil {
		return serviceregistry.HostInfo{}, err
	}
	if _, err := cli.getOrCreateDiscoveredClient(member); err != nil {
		return serviceregistry.HostInfo{}, err
	}

	cli.stateCoordinatorMu.Lock()
	defer cli.stateCoordinatorMu.Unlock()

	if pinned, ok := cli.stateCoordinators[routingKey]; ok {
		pinned.lastUsed = time.Now().UTC()
		cli.stateCoordinators[routingKey] = pinned
		return pinned.member, nil
	}

	cli.rememberPinnedStateCoordinatorLocked(routingKey, member)
	return member, nil
}

func (cli *clientImpl) cachedPinnedStateCoordinator(routingKey string) (serviceregistry.HostInfo, bool) {
	cli.stateCoordinatorMu.Lock()
	defer cli.stateCoordinatorMu.Unlock()

	if cli.stateCoordinators == nil {
		cli.stateCoordinators = make(map[string]pinnedStateCoordinator)
	}
	if pinned, ok := cli.stateCoordinators[routingKey]; ok {
		pinned.lastUsed = time.Now().UTC()
		cli.stateCoordinators[routingKey] = pinned
		return pinned.member, true
	}
	return serviceregistry.HostInfo{}, false
}

func (cli *clientImpl) rememberPinnedStateCoordinatorLocked(routingKey string, member serviceregistry.HostInfo) {
	if cli.stateCoordinators == nil {
		cli.stateCoordinators = make(map[string]pinnedStateCoordinator)
	}

	if _, exists := cli.stateCoordinators[routingKey]; !exists && len(cli.stateCoordinators) >= maxPinnedStateCoordinators {
		cli.evictOldestPinnedStateCoordinatorLocked()
	}
	cli.stateCoordinators[routingKey] = pinnedStateCoordinator{
		member:    member,
		memberKey: coordinatorMemberKey(member),
		lastUsed:  time.Now().UTC(),
	}
}

func (cli *clientImpl) evictOldestPinnedStateCoordinatorLocked() {
	var oldestKey string
	var oldestTime time.Time
	for key, pinned := range cli.stateCoordinators {
		if oldestKey == "" || pinned.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = pinned.lastUsed
		}
	}
	if oldestKey != "" {
		delete(cli.stateCoordinators, oldestKey)
	}
}

func (cli *clientImpl) refreshPinnedStateCoordinator(ctx context.Context, routingKey string, failed serviceregistry.HostInfo) {
	failedKey := coordinatorMemberKey(failed)

	cli.stateCoordinatorMu.Lock()
	pinned, ok := cli.stateCoordinators[routingKey]
	if !ok || pinned.memberKey != failedKey {
		cli.stateCoordinatorMu.Unlock()
		return
	}
	cli.stateCoordinatorMu.Unlock()

	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		logger.Debug(ctx, "Failed to refresh pinned state coordinator",
			slog.String("coordinator-id", failed.ID),
			tag.Host(failed.Host),
			tag.Port(failed.Port),
			tag.Error(err))
		return
	}

	var replacement *serviceregistry.HostInfo
	for _, member := range members {
		if coordinatorMemberKey(member) == failedKey {
			member := member
			replacement = &member
			break
		}
	}
	if replacement != nil {
		if _, err := cli.getOrCreateDiscoveredClient(*replacement); err != nil {
			logger.Debug(ctx, "Failed to refresh pinned state coordinator client",
				slog.String("coordinator-id", replacement.ID),
				tag.Host(replacement.Host),
				tag.Port(replacement.Port),
				tag.Error(err))
			return
		}
	}

	cli.stateCoordinatorMu.Lock()
	defer cli.stateCoordinatorMu.Unlock()

	pinned, ok = cli.stateCoordinators[routingKey]
	if !ok || pinned.memberKey != failedKey {
		return
	}
	if replacement != nil {
		cli.rememberPinnedStateCoordinatorLocked(routingKey, *replacement)
		return
	}
	delete(cli.stateCoordinators, routingKey)
}

func (cli *clientImpl) callMember(ctx context.Context, member serviceregistry.HostInfo, callback func(context.Context, *client) error) error {
	client, err := cli.getOrCreateClient(member)
	if err != nil {
		cli.recordFailure(err)
		return err
	}
	if err := callback(ctx, client); err != nil {
		cli.recordFailure(err)
		return err
	}
	cli.recordSuccess(ctx)
	return nil
}

func (cli *clientImpl) callMemberWithTimeout(ctx context.Context, member serviceregistry.HostInfo, callback func(context.Context, *client) error) error {
	if cli.config.RequestTimeout <= 0 {
		return cli.callMember(ctx, member, callback)
	}

	callCtx, cancel := context.WithTimeout(ctx, cli.config.RequestTimeout)
	defer cancel()
	return cli.callMember(callCtx, member, callback)
}

func (cli *clientImpl) callOwner(
	ctx context.Context,
	owner serviceregistry.HostInfo,
	checkHealth bool,
	callback func(context.Context, serviceregistry.HostInfo, *client) error,
) error {
	members, discoveryErr := cli.ownerMembers(ctx, owner)
	if len(members) == 0 {
		return discoveryErr
	}

	var lastErr error
	for i, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}

		callCtx, cancel := cli.memberCallContext(ctx, len(members)-i)
		memberClient, err := cli.getOrCreateDiscoveredClient(member)
		healthFailed := false
		if err == nil && checkHealth {
			err = cli.isHealthy(callCtx, memberClient)
			healthFailed = err != nil
		}
		if err == nil {
			callbackCtx := callCtx
			if checkHealth {
				// Streaming callbacks use the parent context so the member probe
				// timeout does not limit the lifetime of an established stream.
				callbackCtx = ctx
			}
			err = callback(callbackCtx, member, memberClient)
		}
		cancel()
		if err == nil {
			cli.rememberOwnerRoute(owner, member)
			cli.recordSuccess(ctx)
			return nil
		}

		cli.recordFailure(err)
		cli.forgetOwnerRoute(owner, member)
		if isLegacyOwnerRejection(err) {
			cli.rememberOwnerFailure(owner, member)
			cli.removeClient(member)
		}
		lastErr = err
		if !healthFailed && !isOwnerFailoverError(err) {
			return err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return discoveryErr
}

func (cli *clientImpl) ownerMembers(ctx context.Context, owner serviceregistry.HostInfo) ([]serviceregistry.HostInfo, error) {
	candidates := make([]serviceregistry.HostInfo, 0, 2)
	seen := make(map[string]struct{})
	add := func(member serviceregistry.HostInfo) {
		if member.Host == "" || member.Port <= 0 {
			return
		}
		key := coordinatorMemberKey(member)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, member)
	}

	if member, ok := cli.cachedOwnerRoute(owner); ok {
		add(member)
	}
	add(owner)

	members, err := cli.getCoordinatorMembers(ctx)
	if err == nil {
		for _, member := range members {
			add(member)
		}
	}
	if len(candidates) > 0 {
		if failedKey := cli.cachedOwnerFailure(owner); failedKey != "" && len(candidates) > 1 {
			for i, member := range candidates[:len(candidates)-1] {
				if coordinatorMemberKey(member) == failedKey {
					copy(candidates[i:], candidates[i+1:])
					candidates[len(candidates)-1] = member
					break
				}
			}
		}
		return candidates, nil
	}
	if err == nil {
		err = fmt.Errorf("no coordinators available")
	}
	return nil, err
}

func (cli *clientImpl) memberCallContext(ctx context.Context, remainingMembers int) (context.Context, context.CancelFunc) {
	timeout := cli.config.RequestTimeout
	if deadline, ok := ctx.Deadline(); ok && remainingMembers > 0 {
		share := time.Until(deadline) / time.Duration(remainingMembers)
		if timeout <= 0 || share < timeout {
			timeout = share
		}
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func isOwnerFailoverError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded || isLegacyOwnerRejection(err)
}

func isLegacyOwnerRejection(err error) bool {
	if status.Code(err) != codes.FailedPrecondition {
		return false
	}
	// Older coordinators expose owner rejection only as status text. Keep these
	// frozen wire messages recognizable during mixed-version rolling upgrades.
	message := err.Error()
	return strings.Contains(message, legacyRunHeartbeatOwnerRejection) ||
		strings.Contains(message, legacyStatusUpdateOwnerRejection) ||
		strings.Contains(message, legacyLogChunkOwnerRejection) ||
		strings.Contains(message, legacyArtifactChunkOwnerRejection)
}

func (cli *clientImpl) cachedOwnerRoute(owner serviceregistry.HostInfo) (serviceregistry.HostInfo, bool) {
	cli.ownerRoutesMu.RLock()
	defer cli.ownerRoutesMu.RUnlock()
	member, ok := cli.ownerRoutes[coordinatorMemberKey(owner)]
	return member, ok
}

func (cli *clientImpl) rememberOwnerRoute(owner, member serviceregistry.HostInfo) {
	cli.ownerRoutesMu.Lock()
	defer cli.ownerRoutesMu.Unlock()
	key := coordinatorMemberKey(owner)
	cli.ownerRoutes[key] = member
	delete(cli.ownerFailures, key)
}

func (cli *clientImpl) cachedOwnerFailure(owner serviceregistry.HostInfo) string {
	cli.ownerRoutesMu.RLock()
	defer cli.ownerRoutesMu.RUnlock()
	return cli.ownerFailures[coordinatorMemberKey(owner)]
}

func (cli *clientImpl) rememberOwnerFailure(owner, failed serviceregistry.HostInfo) {
	cli.ownerRoutesMu.Lock()
	defer cli.ownerRoutesMu.Unlock()
	cli.ownerFailures[coordinatorMemberKey(owner)] = coordinatorMemberKey(failed)
}

func (cli *clientImpl) removeClient(member serviceregistry.HostInfo) {
	key := coordinatorMemberKey(member)
	address := coordinatorAddress(member)
	cli.clientsMu.Lock()
	memberClient, ok := cli.clients[key]
	if ok && memberClient.address == address {
		delete(cli.clients, key)
	} else {
		memberClient = nil
	}
	cli.clientsMu.Unlock()
	if memberClient != nil {
		_ = memberClient.conn.Close()
	}
}

func (cli *clientImpl) ownerStreamError(owner, member serviceregistry.HostInfo, err error) error {
	if err == nil || !isOwnerFailoverError(err) {
		return err
	}
	cli.forgetOwnerRoute(owner, member)
	if isLegacyOwnerRejection(err) {
		cli.rememberOwnerFailure(owner, member)
		cli.removeClient(member)
		if st, ok := status.FromError(err); ok {
			return status.Error(codes.Unavailable, st.Message())
		}
	}
	return err
}

func (cli *clientImpl) forgetOwnerRoute(owner, failed serviceregistry.HostInfo) {
	cli.ownerRoutesMu.Lock()
	defer cli.ownerRoutesMu.Unlock()
	key := coordinatorMemberKey(owner)
	if member, ok := cli.ownerRoutes[key]; ok && coordinatorMemberKey(member) == coordinatorMemberKey(failed) {
		delete(cli.ownerRoutes, key)
	}
}

func (cli *clientImpl) isHealthy(ctx context.Context, client *client) error {
	// Check health
	req := &grpc_health_v1.HealthCheckRequest{
		Service: "", // Check overall server health
	}

	resp, err := client.healthClient.Check(ctx, req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("coordinator not healthy: %s", resp.Status)
	}

	return nil
}

// getOrCreateClient gets a client for the member without changing the cached address.
func (cli *clientImpl) getOrCreateClient(member serviceregistry.HostInfo) (*client, error) {
	return cli.getOrCreateClientWithAddressRefresh(member, false)
}

// getOrCreateDiscoveredClient treats the discovered address as authoritative.
func (cli *clientImpl) getOrCreateDiscoveredClient(member serviceregistry.HostInfo) (*client, error) {
	return cli.getOrCreateClientWithAddressRefresh(member, true)
}

func (cli *clientImpl) getOrCreateClientWithAddressRefresh(member serviceregistry.HostInfo, refreshAddress bool) (*client, error) {
	key := coordinatorMemberKey(member)
	address := coordinatorAddress(member)

	// Try to get existing client with read lock
	cli.clientsMu.RLock()
	if c, exists := cli.clients[key]; exists &&
		(!refreshAddress ||
			isOlderCoordinatorIncarnation(member.StartedAt, c.startedAt) ||
			(c.address == address && !member.StartedAt.After(c.startedAt))) {
		cli.clientsMu.RUnlock()
		return c, nil
	}
	cli.clientsMu.RUnlock()

	// Need to create new client, acquire write lock
	cli.clientsMu.Lock()
	defer cli.clientsMu.Unlock()

	// Double-check after acquiring write lock
	if c, exists := cli.clients[key]; exists {
		if !refreshAddress || isOlderCoordinatorIncarnation(member.StartedAt, c.startedAt) {
			return c, nil
		}
		if c.address == address {
			if member.StartedAt.After(c.startedAt) {
				c.startedAt = member.StartedAt
			}
			return c, nil
		}
	}

	// Create new client
	c, err := cli.createClient(member)
	if err != nil {
		return nil, err
	}
	if refreshAddress {
		c.startedAt = member.StartedAt
	}

	if stale, exists := cli.clients[key]; exists {
		_ = stale.conn.Close()
	}

	// Cache it
	cli.clients[key] = c
	return c, nil
}

func isOlderCoordinatorIncarnation(candidate, current time.Time) bool {
	return !candidate.IsZero() && !current.IsZero() && candidate.Before(current)
}

// createClient creates a new gRPC client for the given coordinator
func (cli *clientImpl) createClient(member serviceregistry.HostInfo) (*client, error) {
	// Get dial options based on TLS configuration
	dialOpts, err := getDialOptions(cli.config)
	if err != nil {
		return nil, fmt.Errorf("failed to configure gRPC connection: %w", err)
	}

	// Construct address from host and port
	address := coordinatorAddress(member)

	// Create gRPC connection
	conn, err := grpc.NewClient(address, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client for %s: %w", address, err)
	}

	return &client{
		address:      address,
		conn:         conn,
		client:       coordinatorv1.NewCoordinatorServiceClient(conn),
		healthClient: grpc_health_v1.NewHealthClient(conn),
	}, nil
}

// Cleanup cleans up all connections
func (cli *clientImpl) Cleanup(ctx context.Context) error {
	cli.clientsMu.Lock()
	defer cli.clientsMu.Unlock()

	for id, c := range cli.clients {
		if err := c.conn.Close(); err != nil {
			logger.Error(ctx, "Failed to close connection", slog.String("coordinator-id", id), tag.Error(err))
		}
	}

	// Clear the map
	cli.clients = make(map[string]*client)
	return nil
}

// recordFailure updates the state for a failed coordinator connection/operation
func (cli *clientImpl) recordFailure(err error) {
	cli.stateMu.Lock()
	defer cli.stateMu.Unlock()

	cli.state.IsConnected = false
	cli.state.ConsecutiveFails++
	cli.state.FailCount++
	cli.state.LastError = err
}

// recordSuccess updates the state for a successful coordinator operation
func (cli *clientImpl) recordSuccess(ctx context.Context) {
	var previousConsecutiveFailures int

	cli.stateMu.Lock()
	if !cli.state.IsConnected && cli.state.ConsecutiveFails > 0 {
		previousConsecutiveFailures = cli.state.ConsecutiveFails
	}

	// Reset consecutive failures on success before logging so any logging side
	// effects cannot deadlock by re-entering the coordinator client while the
	// state mutex is held.
	cli.state.IsConnected = true
	cli.state.ConsecutiveFails = 0
	cli.state.LastError = nil
	cli.stateMu.Unlock()

	if previousConsecutiveFailures > 0 {
		logger.Info(ctx, "CoordinatorCli connection recovered",
			slog.Int("previous-consecutive-failures", previousConsecutiveFailures))
	}
}

// getCoordinatorMembers discovers available coordinators from the service registry.
// Returns an error if discovery fails or no coordinators are available.
func (cli *clientImpl) getCoordinatorMembers(ctx context.Context) ([]serviceregistry.HostInfo, error) {
	members, err := cli.registry.GetServiceMembers(ctx, serviceregistry.ServiceNameCoordinator)
	if err != nil {
		return nil, fmt.Errorf("failed to discover coordinators: %w", err)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("no coordinators available")
	}
	return sortCoordinatorMembers(members), nil
}

// GetWorkers retrieves the list of workers from all coordinators
func (cli *clientImpl) GetWorkers(ctx context.Context) ([]*coordinatorv1.WorkerInfo, error) {
	// Get all available coordinators from discovery
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}

	// Collect workers from all coordinators
	workersByID := make(map[string]*coordinatorv1.WorkerInfo)
	var lastErr error
	var successfulReads bool

	for _, member := range members {
		// Get or create client for this member
		c, err := cli.getOrCreateDiscoveredClient(member)
		if err != nil {
			logger.Warn(ctx, "Failed to connect to coordinator",
				tag.ID(member.ID),
				tag.Host(member.Host),
				tag.Port(member.Port),
				tag.Error(err))
			lastErr = err
			continue
		}

		// Try to get workers from this coordinator
		resp, err := c.client.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
		if err != nil {
			logger.Warn(ctx, "Failed to get workers from coordinator",
				tag.ID(member.ID),
				tag.Host(member.Host),
				tag.Port(member.Port),
				tag.Error(err))
			lastErr = err
			continue
		}
		successfulReads = true

		// Append workers from this coordinator
		if resp != nil && resp.Workers != nil {
			for _, worker := range resp.Workers {
				if worker == nil {
					continue
				}
				workersByID[worker.WorkerId] = selectAuthoritativeWorker(workersByID[worker.WorkerId], worker)
			}
		}
	}

	allWorkers := make([]*coordinatorv1.WorkerInfo, 0, len(workersByID))
	for _, worker := range workersByID {
		allWorkers = append(allWorkers, worker)
	}
	sort.Slice(allWorkers, func(i, j int) bool {
		return allWorkers[i].WorkerId < allWorkers[j].WorkerId
	})

	if lastErr != nil {
		if successfulReads {
			return allWorkers, fmt.Errorf("partial failure getting workers: %w", lastErr)
		}
		return nil, fmt.Errorf("failed to get workers from any coordinator: %w", lastErr)
	}

	return allWorkers, nil
}

func sortCoordinatorMembers(members []serviceregistry.HostInfo) []serviceregistry.HostInfo {
	byEndpoint := make(map[string]serviceregistry.HostInfo, len(members))
	for _, member := range members {
		key := coordinatorMemberKey(member)
		current, exists := byEndpoint[key]
		if !exists || member.StartedAt.After(current.StartedAt) {
			byEndpoint[key] = member
		}
	}
	sorted := make([]serviceregistry.HostInfo, 0, len(byEndpoint))
	for _, member := range byEndpoint {
		sorted = append(sorted, member)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return coordinatorMemberKey(sorted[i]) < coordinatorMemberKey(sorted[j])
	})
	return sorted
}

func orderStateCoordinatorMembers(members []serviceregistry.HostInfo, routingKey string) []serviceregistry.HostInfo {
	ordered := append([]serviceregistry.HostInfo(nil), members...)
	if len(ordered) < 2 {
		return ordered
	}

	sort.Slice(ordered, func(i, j int) bool {
		left := stateCoordinatorMemberScore(routingKey, ordered[i])
		right := stateCoordinatorMemberScore(routingKey, ordered[j])
		if cmp := bytes.Compare(left[:], right[:]); cmp != 0 {
			return cmp > 0
		}
		return coordinatorMemberKey(ordered[i]) < coordinatorMemberKey(ordered[j])
	})
	return ordered
}

func selectStateCoordinatorOwner(members []serviceregistry.HostInfo, routingKey string) (serviceregistry.HostInfo, error) {
	ordered := orderStateCoordinatorMembers(members, routingKey)
	if len(ordered) == 0 {
		return serviceregistry.HostInfo{}, fmt.Errorf("no coordinators available")
	}
	return ordered[0], nil
}

func stateCoordinatorMemberScore(routingKey string, member serviceregistry.HostInfo) [sha256.Size]byte {
	return sha256.Sum256([]byte(routingKey + "\x00" + coordinatorMemberKey(member)))
}

func coordinatorMemberKey(member serviceregistry.HostInfo) string {
	if member.Host != "" && member.Port > 0 {
		return coordinatorAddress(member)
	}
	return member.ID
}

func coordinatorAddress(member serviceregistry.HostInfo) string {
	return net.JoinHostPort(member.Host, strconv.Itoa(member.Port))
}

func selectAuthoritativeWorker(current, candidate *coordinatorv1.WorkerInfo) *coordinatorv1.WorkerInfo {
	if candidate == nil {
		return current
	}
	if current == nil {
		return candidate
	}
	if candidate.LastHeartbeatAt > current.LastHeartbeatAt {
		return candidate
	}
	return current
}

// Heartbeat sends a heartbeat to coordinators and returns the response
func (cli *clientImpl) Heartbeat(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	if cli.config.HeartbeatTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cli.config.HeartbeatTimeout)
		defer cancel()
	}

	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, heartbeatContextError(ctx, err)
	}

	var resp *coordinatorv1.HeartbeatResponse
	call := func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		callResp, callErr := client.client.Heartbeat(ctx, req)
		if callErr != nil {
			return fmt.Errorf("heartbeat failed: %w", callErr)
		}
		resp = callResp
		return nil
	}

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline || len(members) == 1 {
		err = cli.attemptCall(ctx, members, call)
		return resp, heartbeatContextError(ctx, err)
	}

	shuffleCoordinatorMembers(members)

	var lastErr error
	for i := range members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		remainingAttempts := len(members) - i
		attemptTimeout := time.Until(deadline) / time.Duration(remainingAttempts)
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		lastErr = cli.attemptCall(attemptCtx, members[i:i+1], call)
		cancel()
		if lastErr == nil {
			return resp, nil
		}
	}
	return nil, heartbeatContextError(ctx, lastErr)
}

func heartbeatContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return err
}

func (cli *clientImpl) AckTaskClaimTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	var resp *coordinatorv1.AckTaskClaimResponse
	err := cli.callOwner(ctx, owner, false, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.AckTaskClaim(ctx, req)
		if callErr != nil {
			return fmt.Errorf("ack task claim failed: %w", callErr)
		}
		if resp != nil && !resp.Accepted && resp.Error == legacyClaimOwnerEndpointRejection {
			return status.Error(codes.Unavailable, resp.Error)
		}
		return nil
	})
	return resp, err
}

// ClaimAgentSessionCleanup reserves provider cleanup from one coordinator.
func (cli *clientImpl) ClaimAgentSessionCleanup(
	ctx context.Context,
	req *coordinatorv1.ClaimAgentSessionCleanupRequest,
) (*coordinatorv1.ClaimAgentSessionCleanupResponse, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}
	var resp *coordinatorv1.ClaimAgentSessionCleanupResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.ClaimAgentSessionCleanup(ctx, req)
		if callErr != nil {
			return fmt.Errorf("claim agent session cleanup failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

// CompleteAgentSessionCleanupTo updates a cleanup claim on its owning coordinator.
func (cli *clientImpl) CompleteAgentSessionCleanupTo(
	ctx context.Context,
	owner serviceregistry.HostInfo,
	req *coordinatorv1.CompleteAgentSessionCleanupRequest,
) (*coordinatorv1.CompleteAgentSessionCleanupResponse, error) {
	var resp *coordinatorv1.CompleteAgentSessionCleanupResponse
	err := cli.callOwner(ctx, owner, false, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.CompleteAgentSessionCleanup(ctx, req)
		if callErr != nil {
			return fmt.Errorf("complete agent session cleanup failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

func (cli *clientImpl) RunHeartbeatTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	var resp *coordinatorv1.RunHeartbeatResponse
	err := cli.callOwner(ctx, owner, false, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.RunHeartbeat(ctx, req)
		if callErr != nil {
			return fmt.Errorf("run heartbeat failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

// ReportStatus sends a status update to the coordinator
func (cli *clientImpl) ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}

	var resp *coordinatorv1.ReportStatusResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.ReportStatus(ctx, req)
		if callErr != nil {
			return fmt.Errorf("report status failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

func (cli *clientImpl) ReportStatusTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	var resp *coordinatorv1.ReportStatusResponse
	err := cli.callOwner(ctx, owner, false, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.ReportStatus(ctx, req)
		if callErr != nil {
			return fmt.Errorf("report status failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

// StreamLogs returns a log streaming client for sending logs to the coordinator.
// It performs health checks and tries multiple coordinators for failover.
func (cli *clientImpl) StreamLogs(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return openStreamWithFailover(cli, ctx, "log", func(ctx context.Context, client *client) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		return client.client.StreamLogs(ctx)
	})
}

func (cli *clientImpl) StreamLogsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	var stream coordinatorv1.CoordinatorService_StreamLogsClient
	var member serviceregistry.HostInfo
	err := cli.callOwner(ctx, owner, true, func(ctx context.Context, selected serviceregistry.HostInfo, client *client) error {
		var callErr error
		stream, callErr = client.client.StreamLogs(ctx)
		if callErr != nil {
			return fmt.Errorf("stream logs failed: %w", callErr)
		}
		member = selected
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ownerLogStream{
		CoordinatorService_StreamLogsClient: stream,
		client:                              cli,
		owner:                               owner,
		member:                              member,
	}, nil
}

// StreamArtifacts returns an artifact streaming client for sending artifacts to the coordinator.
// It performs health checks and tries multiple coordinators for failover.
func (cli *clientImpl) StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return openStreamWithFailover(cli, ctx, "artifact", func(ctx context.Context, client *client) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
		return client.client.StreamArtifacts(ctx)
	})
}

func openStreamWithFailover[T any](
	cli *clientImpl,
	ctx context.Context,
	streamType string,
	open func(context.Context, *client) (T, error),
) (T, error) {
	var zero T

	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return zero, err
	}

	shuffleCoordinatorMembers(members)

	var lastErr error
	for _, member := range members {
		memberClient, err := cli.getOrCreateDiscoveredClient(member)
		if err != nil {
			cli.recordFailure(err)
			lastErr = err
			continue
		}
		if err := cli.isHealthy(ctx, memberClient); err != nil {
			cli.recordFailure(err)
			lastErr = err
			continue
		}

		stream, err := open(ctx, memberClient)
		if err != nil {
			cli.recordFailure(err)
			lastErr = err
			continue
		}

		cli.recordSuccess(ctx)
		return stream, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no healthy coordinators available")
	}
	return zero, fmt.Errorf("failed to create %s stream: %w", streamType, lastErr)
}

func shuffleCoordinatorMembers(members []serviceregistry.HostInfo) {
	//nolint:gosec // Coordinator ordering is not security-sensitive.
	rand.Shuffle(len(members), func(i, j int) {
		members[i], members[j] = members[j], members[i]
	})
}

// GetDAGRunStatus retrieves the status of a DAG run from the coordinator.
func (cli *clientImpl) GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}

	req := &coordinatorv1.GetDAGRunStatusRequest{
		DagName:  dagName,
		DagRunId: dagRunID,
	}

	// Include root reference for sub-DAG queries
	if rootRef != nil {
		req.RootDagRunName = rootRef.Name
		req.RootDagRunId = rootRef.ID
	}

	var resp *coordinatorv1.GetDAGRunStatusResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.GetDAGRunStatus(ctx, req)
		if callErr != nil {
			return fmt.Errorf("get DAG run status failed: %w", callErr)
		}
		if resp == nil {
			return fmt.Errorf("coordinator %s returned empty DAG run status response", member.ID)
		}
		if resp.Error != "" {
			return fmt.Errorf("coordinator %s failed to get DAG run status: %s", member.ID, resp.Error)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := &dispatch.DAGRunStatusResult{Found: resp.Found}
	if resp.Status != nil {
		status, convErr := convert.ProtoToDAGRunStatus(resp.Status)
		if convErr != nil {
			return nil, fmt.Errorf("convert coordinator status: %w", convErr)
		}
		result.Status = status
	}
	return result, nil
}

// GetDAG retrieves a DAG definition (raw YAML spec) from the coordinator's DAG store.
func (cli *clientImpl) GetDAG(ctx context.Context, name string) (string, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return "", err
	}

	req := &coordinatorv1.GetDAGRequest{
		Name: name,
	}

	var resp *coordinatorv1.GetDAGResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.GetDAG(ctx, req)
		if callErr != nil {
			return fmt.Errorf("get DAG definition failed: %w", callErr)
		}
		if resp == nil {
			return fmt.Errorf("coordinator %s returned empty DAG definition response", member.ID)
		}
		if resp.Error != "" {
			return fmt.Errorf("coordinator %s get DAG failed: %s", member.ID, resp.Error)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return resp.Spec, nil
}

// RequestCancel requests cancellation of a DAG run through the coordinator
func (cli *clientImpl) RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) error {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return err
	}

	req := &coordinatorv1.RequestCancelRequest{
		DagName:  dagName,
		DagRunId: dagRunID,
	}

	// Include root reference for sub-DAG cancellation
	if rootRef != nil {
		req.RootDagRunName = rootRef.Name
		req.RootDagRunId = rootRef.ID
	}

	return cli.attemptCall(ctx, members, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		resp, callErr := client.client.RequestCancel(ctx, req)
		if callErr != nil {
			return fmt.Errorf("request cancel failed: %w", callErr)
		}
		if !resp.Accepted {
			return fmt.Errorf("cancellation not accepted: %s", resp.Error)
		}
		return nil
	})
}

func (cli *clientImpl) StreamArtifactsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	var stream coordinatorv1.CoordinatorService_StreamArtifactsClient
	var member serviceregistry.HostInfo
	err := cli.callOwner(ctx, owner, true, func(ctx context.Context, selected serviceregistry.HostInfo, client *client) error {
		var callErr error
		stream, callErr = client.client.StreamArtifacts(ctx)
		if callErr != nil {
			return fmt.Errorf("stream artifacts failed: %w", callErr)
		}
		member = selected
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ownerArtifactStream{
		CoordinatorService_StreamArtifactsClient: stream,
		client:                                   cli,
		owner:                                    owner,
		member:                                   member,
	}, nil
}

func (cli *clientImpl) PutWorkspaceBundle(ctx context.Context, desc workspacebundle.Descriptor, data []byte) error {
	if err := workspacebundle.Verify(data, desc.Digest); err != nil {
		return err
	}
	return cli.putWorkspaceBundle(ctx, desc, int64(len(data)), func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

func (cli *clientImpl) putWorkspaceBundleFile(ctx context.Context, desc workspacebundle.Descriptor, archivePath string) error {
	return cli.putWorkspaceBundle(ctx, desc, desc.Size, func() (io.ReadCloser, error) {
		return os.Open(archivePath) //nolint:gosec // archivePath is created by PrepareDAGWorkspaceFile.
	})
}

func (cli *clientImpl) putWorkspaceBundle(
	ctx context.Context,
	desc workspacebundle.Descriptor,
	size int64,
	open func() (io.ReadCloser, error),
) error {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return err
	}
	var satisfied int
	errs := make([]error, 0)
	for _, member := range members {
		memberClient, err := cli.getOrCreateDiscoveredClient(member)
		if err != nil {
			errs = append(errs, fmt.Errorf("coordinator %q: %w", member.ID, err))
			continue
		}
		if err := cli.isHealthy(ctx, memberClient); err != nil {
			errs = append(errs, fmt.Errorf("coordinator %q is unhealthy: %w", member.ID, err))
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, cli.config.RequestTimeout)
		exists, err := hasWorkspaceBundleInMember(callCtx, memberClient, desc.Digest)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("check workspace bundle on coordinator %q: %w", member.ID, err))
			continue
		}
		if exists {
			satisfied++
			continue
		}

		reader, err := open()
		if err != nil {
			errs = append(errs, fmt.Errorf("open workspace bundle for coordinator %q: %w", member.ID, err))
			continue
		}
		callCtx, cancel = context.WithTimeout(ctx, cli.config.RequestTimeout)
		err = putWorkspaceBundleToMember(callCtx, memberClient, desc, reader, size)
		cancel()
		closeErr := reader.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("upload workspace bundle to coordinator %q: %w", member.ID, err))
			continue
		}
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("close workspace bundle for coordinator %q: %w", member.ID, closeErr))
		}
		satisfied++
	}
	if satisfied == 0 {
		if len(errs) == 0 {
			errs = append(errs, fmt.Errorf("no healthy coordinators available"))
		}
		return fmt.Errorf("failed to upload workspace bundle: %w", errors.Join(errs...))
	}
	return nil
}

func hasWorkspaceBundleInMember(ctx context.Context, client *client, digest string) (bool, error) {
	resp, err := client.client.HasWorkspaceBundle(ctx, &coordinatorv1.HasWorkspaceBundleRequest{Digest: digest})
	if err != nil {
		return false, err
	}
	return resp != nil && resp.Exists, nil
}

func putWorkspaceBundleToMember(ctx context.Context, client *client, desc workspacebundle.Descriptor, reader io.Reader, size int64) error {
	stream, err := client.client.PutWorkspaceBundle(ctx)
	if err != nil {
		return fmt.Errorf("open workspace bundle upload stream: %w", err)
	}
	protoDesc := descriptorToProto(desc)
	for remaining, sequence := size, uint64(0); remaining > 0 || sequence == 0; sequence++ {
		chunkSize := min(remaining, int64(workspaceBundleChunkSize))
		chunk := &coordinatorv1.WorkspaceBundleChunk{
			Sequence: sequence,
			IsFinal:  chunkSize == remaining,
		}
		if sequence == 0 {
			chunk.Bundle = protoDesc
		}
		if chunkSize > 0 {
			chunk.Data = make([]byte, chunkSize)
			if _, err := io.ReadFull(reader, chunk.Data); err != nil {
				return fmt.Errorf("read workspace bundle chunk: %w", err)
			}
		}
		if err := stream.Send(chunk); err != nil {
			return fmt.Errorf("send workspace bundle chunk: %w", err)
		}
		remaining -= chunkSize
		if size == 0 {
			break
		}
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("close workspace bundle upload stream: %w", err)
	}
	if resp == nil || !resp.Accepted {
		msg := "workspace bundle upload rejected"
		if resp != nil && resp.Error != "" {
			msg += ": " + resp.Error
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (cli *clientImpl) GetWorkspaceBundle(ctx context.Context, digest string) ([]byte, error) {
	if !workspacebundle.ValidDigest(digest) {
		return nil, fmt.Errorf("invalid workspace bundle digest %q", digest)
	}
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}
	var data []byte
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		data, callErr = getWorkspaceBundleFromMember(ctx, client, digest)
		return callErr
	})
	return data, err
}

func getWorkspaceBundleFromMember(ctx context.Context, client *client, digest string) ([]byte, error) {
	stream, err := client.client.GetWorkspaceBundle(ctx, &coordinatorv1.GetWorkspaceBundleRequest{Digest: digest})
	if err != nil {
		return nil, fmt.Errorf("open workspace bundle download stream: %w", err)
	}
	var buf bytes.Buffer
	var sequence uint64
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("receive workspace bundle chunk: %w", err)
		}
		if chunk.Sequence != sequence {
			return nil, fmt.Errorf("workspace bundle sequence mismatch: got %d, want %d", chunk.Sequence, sequence)
		}
		sequence++
		if len(chunk.Data) > 0 {
			if int64(buf.Len()+len(chunk.Data)) > workspacebundle.DefaultMaxCompressedSize {
				return nil, fmt.Errorf("workspace bundle exceeds compressed size limit %d", workspacebundle.DefaultMaxCompressedSize)
			}
			if _, err := buf.Write(chunk.Data); err != nil {
				return nil, fmt.Errorf("buffer workspace bundle chunk: %w", err)
			}
		}
	}
	data := buf.Bytes()
	if err := workspacebundle.Verify(data, digest); err != nil {
		return nil, err
	}
	return data, nil
}

// getDialOptions returns the appropriate gRPC dial options based on TLS configuration
func getDialOptions(config *Config) ([]grpc.DialOption, error) {
	var opts []grpc.DialOption

	opts = append(opts, grpc.WithDefaultCallOptions(
		grpc.MaxCallRecvMsgSize(grpcMaxMsgSize),
		grpc.MaxCallSendMsgSize(grpcMaxMsgSize),
	))

	if config.Insecure {
		// Use insecure connection (h2c)
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		return opts, nil
	}

	// Configure TLS
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Set InsecureSkipVerify if requested
	if config.SkipTLSVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	// Load client certificates if provided
	if config.CertFile != "" && config.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificates: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA certificate if provided
	if config.CAFile != "" {
		caData, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		certPool, err := x509.SystemCertPool()
		if err != nil {
			// Fall back to empty pool
			certPool = x509.NewCertPool()
		}

		if !certPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("failed to append CA certificate")
		}
		tlsConfig.RootCAs = certPool
	}

	opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	return opts, nil
}
