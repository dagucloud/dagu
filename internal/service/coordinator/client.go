// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"context"
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
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
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
	exec.Dispatcher

	// Dispatch sends a task to the coordinator
	Dispatch(ctx context.Context, task *coordinatorv1.Task) error

	// Poll retrieves a task from the coordinator.
	Poll(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error)

	// GetWorkers retrieves the list of workers from the coordinator
	GetWorkers(ctx context.Context) ([]*coordinatorv1.WorkerInfo, error)

	// Heartbeat sends a heartbeat to the coordinator and returns the response
	// which may include cancellation directives
	Heartbeat(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)

	// AckTaskClaim confirms a claimed task with its owner coordinator.
	AckTaskClaimTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error)

	// RunHeartbeat refreshes leases for tasks owned by a specific coordinator.
	RunHeartbeatTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error)

	// ReportStatus sends a status update to the coordinator (for shared-nothing workers)
	ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)

	// ReportStatusTo sends a status update to a specific owner coordinator.
	ReportStatusTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)

	// StreamLogs returns a log streaming client for sending logs to the coordinator
	StreamLogs(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error)

	// StreamLogsTo opens a log stream to a specific owner coordinator.
	StreamLogsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error)

	// StreamArtifacts returns an artifact streaming client for sending artifacts to the coordinator.
	StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)

	// StreamArtifactsTo opens an artifact stream to a specific owner coordinator.
	StreamArtifactsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)

	// RequestCancel requests cancellation of a DAG run through the coordinator.
	// Used in shared-nothing mode for sub-DAG cancellation.
	RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *exec.DAGRunRef) error

	// GetDAGRunStatus is inherited from execution.Dispatcher

	// Metrics returns the metrics for the coordinator client
	Metrics() Metrics
}

// Metrics defines the metrics for the coordinator client
type Metrics struct {
	FailCount        int   // Total number of failures
	IsConnected      bool  // Whether the client is currently connected
	ConsecutiveFails int   // Number of consecutive failures
	LastError        error // Last error encountered
}

var (
	_ Client          = (*clientImpl)(nil)
	_ exec.Dispatcher = (*clientImpl)(nil)
)

// clientImpl is the concrete implementation
type clientImpl struct {
	config   *Config
	registry exec.ServiceRegistry

	clientsMu sync.RWMutex
	clients   map[string]*client // Cache of gRPC clients by coordinator ID

	stateMu sync.RWMutex // Mutex for state access
	state   *Metrics     // Connection state tracking
}

// client holds the gRPC connection and clients for the coordinator service.
// it should be closed and removed when no longer needed or when the coordinator
// is unhealthy.
type client struct {
	conn         *grpc.ClientConn
	client       coordinatorv1.CoordinatorServiceClient
	healthClient grpc_health_v1.HealthClient
}

// grpcMaxMsgSize is the maximum message size for gRPC calls.
// Default gRPC limit is 4 MB; we increase to 16 MB to handle large status
// payloads that include LLM session messages in shared-nothing mode.
const grpcMaxMsgSize = 16 * 1024 * 1024

// Errors
var (
	ErrMissingTLSConfig = fmt.Errorf("TLS enabled but no certificates provided")
)

// New creates a new coordinator client with the given configuration
func New(registry exec.ServiceRegistry, config *Config) Client {
	return &clientImpl{
		config:   config,
		registry: registry,
		clients:  make(map[string]*client),
		state: &Metrics{
			IsConnected: true, // Assume connected initially
		},
	}
}

// Dispatch sends a task to the coordinator
func (cli *clientImpl) Dispatch(ctx context.Context, task *coordinatorv1.Task) error {
	logger.Info(ctx, "Client dispatching task",
		slog.String("operation", task.Operation.String()),
		tag.RunID(task.DagRunId),
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

		return cli.attemptCall(ctx, members, func(ctx context.Context, member exec.HostInfo, client *client) error {
			// Create request
			req := &coordinatorv1.DispatchRequest{Task: task}

			// Apply request timeout
			dispatchCtx, cancel := context.WithTimeout(ctx, cli.config.RequestTimeout)
			defer cancel()

			// Try to dispatch
			if _, err := client.client.Dispatch(dispatchCtx, req); err != nil {
				logger.Warn(ctx, "Failed to dispatch task to coordinator",
					tag.RunID(task.DagRunId),
					tag.Target(task.Target),
					slog.Any("worker-selector", task.WorkerSelector),
					slog.String("coordinator-id", member.ID),
				)

				wrapped := fmt.Errorf("failed to dispatch task to coordinator %s: %w", member.ID, err)

				// FailedPrecondition means permanent misconfiguration (e.g. selector mismatch).
				// Stop retrying across coordinators and across the outer backoff loop.
				if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition {
					if staleErr, ok := exec.ParseStaleQueueDispatchError(st.Message()); ok {
						return backoff.PermanentError(fmt.Errorf("failed to dispatch task to coordinator %s: %w", member.ID, staleErr))
					}
					return backoff.PermanentError(wrapped)
				}

				// Unavailable and other transient errors will be retried.
				return wrapped
			}

			logger.Info(ctx, "Task dispatched successfully",
				tag.RunID(task.DagRunId),
				tag.Target(task.Target),
				slog.Any("worker-selector", task.WorkerSelector),
				slog.String("coordinator-id", member.ID),
			)

			return nil
		})
	}, policy, nil)
}

// Poll implements Client.
func (cli *clientImpl) Poll(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	var task *coordinatorv1.Task
	err := backoff.Retry(ctx, func(ctx context.Context) error {
		members, err := cli.getCoordinatorMembers(ctx)
		if err != nil {
			return err
		}

		return cli.attemptCall(ctx, members, func(ctx context.Context, member exec.HostInfo, client *client) error {
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

func (cli *clientImpl) attemptCall(ctx context.Context, members []exec.HostInfo, callback func(ctx context.Context, member exec.HostInfo, client *client) error) error {
	// Shuffle members to distribute load evenly
	rand.Shuffle(len(members), func(i, j int) {
		members[i], members[j] = members[j], members[i]
	})

	// Try each coordinator in order (round-robin style)
	var lastErr error
	for _, member := range members {
		// Get or create client for this coordinator
		client, err := cli.getOrCreateClient(member)
		if err != nil {
			logger.Warn(ctx, "Failed to connect to coordinator",
				slog.String("coordinator-id", member.ID),
				tag.Host(member.Host),
				tag.Port(member.Port),
				tag.Error(err))
			cli.removeClient(member) // Remove failed client
			cli.recordFailure(err)
			lastErr = err
			continue
		}

		// Check if the coordinator is healthy
		if err := cli.isHealthy(ctx, member); err != nil {
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

func (cli *clientImpl) callMember(ctx context.Context, member exec.HostInfo, callback func(context.Context, *client) error) error {
	client, err := cli.getOrCreateClient(member)
	if err != nil {
		cli.recordFailure(err)
		return err
	}
	if err := callback(ctx, client); err != nil {
		cli.recordFailure(err)
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			cli.removeClient(member)
		}
		return err
	}
	cli.recordSuccess(ctx)
	return nil
}

func (cli *clientImpl) callMemberWithTimeout(ctx context.Context, member exec.HostInfo, callback func(context.Context, *client) error) error {
	if cli.config.RequestTimeout <= 0 {
		return cli.callMember(ctx, member, callback)
	}

	callCtx, cancel := context.WithTimeout(ctx, cli.config.RequestTimeout)
	defer cancel()
	return cli.callMember(callCtx, member, callback)
}

func (cli *clientImpl) isHealthy(ctx context.Context, member exec.HostInfo) error {
	// Get or create client for this coordinator
	client, err := cli.getOrCreateClient(member)
	if err != nil {
		return fmt.Errorf("failed to get coordinator client: %w", err)
	}

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

// getOrCreateClient gets an existing client or creates a new one for the given member
func (cli *clientImpl) getOrCreateClient(member exec.HostInfo) (*client, error) {
	key := coordinatorMemberKey(member)

	// Try to get existing client with read lock
	cli.clientsMu.RLock()
	if c, exists := cli.clients[key]; exists {
		cli.clientsMu.RUnlock()
		return c, nil
	}
	cli.clientsMu.RUnlock()

	// Need to create new client, acquire write lock
	cli.clientsMu.Lock()
	defer cli.clientsMu.Unlock()

	// Double-check after acquiring write lock
	if c, exists := cli.clients[key]; exists {
		return c, nil
	}

	// Create new client
	c, err := cli.createClient(member)
	if err != nil {
		return nil, err
	}

	// Cache it
	cli.clients[key] = c
	return c, nil
}

// createClient creates a new gRPC client for the given coordinator
func (cli *clientImpl) createClient(member exec.HostInfo) (*client, error) {
	// Get dial options based on TLS configuration
	dialOpts, err := getDialOptions(cli.config)
	if err != nil {
		return nil, fmt.Errorf("failed to configure gRPC connection: %w", err)
	}

	// Construct address from host and port
	address := net.JoinHostPort(member.Host, strconv.Itoa(member.Port))

	// Create gRPC connection
	conn, err := grpc.NewClient(address, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator client for %s: %w", address, err)
	}

	return &client{
		conn:         conn,
		client:       coordinatorv1.NewCoordinatorServiceClient(conn),
		healthClient: grpc_health_v1.NewHealthClient(conn),
	}, nil
}

// removeClient removes a client from the cache
func (cli *clientImpl) removeClient(member exec.HostInfo) {
	key := coordinatorMemberKey(member)

	cli.clientsMu.Lock()
	defer cli.clientsMu.Unlock()

	if c, exists := cli.clients[key]; exists {
		_ = c.conn.Close()
		delete(cli.clients, key)
	}
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
func (cli *clientImpl) getCoordinatorMembers(ctx context.Context) ([]exec.HostInfo, error) {
	members, err := cli.registry.GetServiceMembers(ctx, exec.ServiceNameCoordinator)
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
		c, err := cli.getOrCreateClient(member)
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

			// If this is a connection error, remove the client from cache
			if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
				cli.removeClient(member)
			}
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

func sortCoordinatorMembers(members []exec.HostInfo) []exec.HostInfo {
	sorted := append([]exec.HostInfo(nil), members...)
	sort.Slice(sorted, func(i, j int) bool {
		return coordinatorMemberKey(sorted[i]) < coordinatorMemberKey(sorted[j])
	})
	return sorted
}

func coordinatorMemberKey(member exec.HostInfo) string {
	if member.ID != "" {
		return member.ID
	}
	return fmt.Sprintf("%s:%d", member.Host, member.Port)
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
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}

	var resp *coordinatorv1.HeartbeatResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.Heartbeat(ctx, req)
		if callErr != nil {
			return fmt.Errorf("heartbeat failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

func (cli *clientImpl) AckTaskClaimTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	var resp *coordinatorv1.AckTaskClaimResponse
	err := cli.callMemberWithTimeout(ctx, owner, func(ctx context.Context, client *client) error {
		var callErr error
		resp, callErr = client.client.AckTaskClaim(ctx, req)
		if callErr != nil {
			return fmt.Errorf("ack task claim failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

func (cli *clientImpl) RunHeartbeatTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	var resp *coordinatorv1.RunHeartbeatResponse
	err := cli.callMemberWithTimeout(ctx, owner, func(ctx context.Context, client *client) error {
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
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.ReportStatus(ctx, req)
		if callErr != nil {
			return fmt.Errorf("report status failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

func (cli *clientImpl) ReportStatusTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	var resp *coordinatorv1.ReportStatusResponse
	err := cli.callMemberWithTimeout(ctx, owner, func(ctx context.Context, client *client) error {
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

func (cli *clientImpl) StreamLogsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	var stream coordinatorv1.CoordinatorService_StreamLogsClient
	err := cli.callMember(ctx, owner, func(ctx context.Context, client *client) error {
		var callErr error
		stream, callErr = client.client.StreamLogs(ctx)
		if callErr != nil {
			return fmt.Errorf("stream logs failed: %w", callErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stream, nil
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

	rand.Shuffle(len(members), func(i, j int) {
		members[i], members[j] = members[j], members[i]
	})

	var lastErr error
	for _, member := range members {
		memberClient, err := cli.getOrCreateClient(member)
		if err != nil {
			cli.recordFailure(err)
			lastErr = err
			continue
		}
		if err := cli.isHealthy(ctx, member); err != nil {
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

// GetDAGRunStatus retrieves the status of a DAG run from the coordinator
func (cli *clientImpl) GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *exec.DAGRunRef) (*coordinatorv1.GetDAGRunStatusResponse, error) {
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
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.GetDAGRunStatus(ctx, req)
		if callErr != nil {
			return fmt.Errorf("get DAG run status failed: %w", callErr)
		}
		return nil
	})
	return resp, err
}

// RequestCancel requests cancellation of a DAG run through the coordinator
func (cli *clientImpl) RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *exec.DAGRunRef) error {
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

	return cli.attemptCall(ctx, members, func(ctx context.Context, _ exec.HostInfo, client *client) error {
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

func (cli *clientImpl) StreamArtifactsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	var stream coordinatorv1.CoordinatorService_StreamArtifactsClient
	err := cli.callMember(ctx, owner, func(ctx context.Context, client *client) error {
		var callErr error
		stream, callErr = client.client.StreamArtifacts(ctx)
		if callErr != nil {
			return fmt.Errorf("stream artifacts failed: %w", callErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stream, nil
}

func (cli *clientImpl) PutWorkspaceBundle(ctx context.Context, desc workspacebundle.Descriptor, data []byte) error {
	if err := workspacebundle.Verify(data, desc.Digest); err != nil {
		return err
	}
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return err
	}
	var satisfied int
	var lastErr error
	for _, member := range members {
		memberClient, err := cli.getOrCreateClient(member)
		if err != nil {
			lastErr = err
			continue
		}
		if err := cli.isHealthy(ctx, member); err != nil {
			lastErr = err
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, cli.config.RequestTimeout)
		exists, err := hasWorkspaceBundleInMember(callCtx, memberClient, desc.Digest)
		cancel()
		if err != nil {
			return fmt.Errorf("check workspace bundle on coordinator %q: %w", member.ID, err)
		}
		if exists {
			satisfied++
			continue
		}

		callCtx, cancel = context.WithTimeout(ctx, cli.config.RequestTimeout)
		err = putWorkspaceBundleToMember(callCtx, memberClient, desc, data)
		cancel()
		if err != nil {
			return fmt.Errorf("upload workspace bundle to coordinator %q: %w", member.ID, err)
		}
		satisfied++
	}
	if satisfied == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no healthy coordinators available")
		}
		return fmt.Errorf("failed to upload workspace bundle: %w", lastErr)
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

func putWorkspaceBundleToMember(ctx context.Context, client *client, desc workspacebundle.Descriptor, data []byte) error {
	stream, err := client.client.PutWorkspaceBundle(ctx)
	if err != nil {
		return fmt.Errorf("open workspace bundle upload stream: %w", err)
	}
	protoDesc := descriptorToProto(desc)
	for offset, sequence := 0, uint64(0); offset < len(data) || sequence == 0; sequence++ {
		end := offset + workspaceBundleChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := &coordinatorv1.WorkspaceBundleChunk{
			Sequence: sequence,
			IsFinal:  end == len(data),
		}
		if sequence == 0 {
			chunk.Bundle = protoDesc
		}
		if offset < len(data) {
			chunk.Data = data[offset:end]
		}
		if err := stream.Send(chunk); err != nil {
			return fmt.Errorf("send workspace bundle chunk: %w", err)
		}
		offset = end
		if len(data) == 0 {
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
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ exec.HostInfo, client *client) error {
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
