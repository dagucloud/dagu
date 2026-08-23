// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// Coordinator represents a test gRPC coordinator instance
type Coordinator struct {
	Helper
	handlerConfig coordinator.HandlerConfig
	host          string
	port          int
	instanceID    string
	restartCount  int
	running       bool
	service       *coordinator.Service
	handler       *coordinator.Handler
}

// SetupCoordinator creates and starts a test coordinator instance
func SetupCoordinator(t *testing.T, opts ...HelperOption) *Coordinator {
	t.Helper()

	// Coordinator-backed tests often hand this config to workers or runtime
	// helpers that launch DAG subprocesses. Keep those subprocesses on the
	// current source tree's binary instead of .local/bin.
	opts = append(opts, WithBuiltExecutable())

	// Parse options to access coordinator-specific settings.
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	// Find an available port for the gRPC server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "failed to create listener")

	port := listener.Addr().(*net.TCPAddr).Port

	// Update config with the test port
	opts = append(opts, WithCoordinatorConfig("127.0.0.1", port))

	helper := Setup(t, opts...)

	// Build handler config based on coordinator test options
	cfg := coordinator.HandlerConfig{}
	if options.WithStatusPersistence {
		cfg.DAGRunRepository = helper.DAGRunRepository
	}
	if options.WithLogPersistence {
		cfg.LogDir = helper.Config.Paths.LogDir
	}
	if options.WithArtifactPersistence {
		cfg.ArtifactDir = helper.Config.Paths.ArtifactDir
	}
	cfg.WorkspaceBundleDir = workspacebundle.StoreDir(helper.Config.Paths.DataDir)
	if helper.StaleHeartbeatThreshold > 0 {
		cfg.StaleHeartbeatThreshold = helper.StaleHeartbeatThreshold
	}
	if helper.StaleLeaseThreshold > 0 {
		cfg.StaleLeaseThreshold = helper.StaleLeaseThreshold
	}
	cfg.StateStore = helper.StateStore
	cfg.DispatchTaskStore = helper.DispatchTaskStore
	cfg.WorkerHeartbeatStore = helper.WorkerHeartbeatStore
	cfg.DAGRunLeaseStore = helper.DAGRunLeaseStore
	cfg.ActiveDistributedRunStore = helper.ActiveDistributedRunStore

	coord := &Coordinator{
		Helper:        helper,
		handlerConfig: cfg,
		host:          "127.0.0.1",
		port:          port,
		instanceID:    "test-coordinator",
	}
	coord.start(t, listener)

	// Setup cleanup
	t.Cleanup(func() {
		_ = coord.Stop()
	})

	return coord
}

func (c *Coordinator) start(t *testing.T, listener net.Listener) {
	t.Helper()

	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	cfg := c.handlerConfig
	cfg.Owner = dispatch.CoordinatorEndpoint{ID: c.instanceID, Host: c.host, Port: c.port}
	handler := coordinator.NewHandler(cfg)
	service := coordinator.NewService(
		grpcServer,
		handler,
		listener,
		healthServer,
		healthcheck.NewServer("coordinator", 0),
		c.ServiceRegistry,
		c.Config,
		c.instanceID,
		c.host,
	)

	c.service = service
	c.handler = handler
	require.NoError(t, service.Start(c.Context), "failed to start coordinator")
	c.running = true
	waitForCoordinatorStart(t, c.Address())
}

// StartPeer starts another coordinator instance in the same ownership domain.
func (c *Coordinator) StartPeer(t *testing.T) *Coordinator {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "failed to create peer coordinator listener")
	port := listener.Addr().(*net.TCPAddr).Port
	peerHelper := c.Helper
	peerHelper.ServiceRegistry = file.NewServiceRegistry(c.Config)
	peer := &Coordinator{
		Helper:        peerHelper,
		handlerConfig: c.handlerConfig,
		host:          "127.0.0.1",
		port:          port,
		instanceID:    "test-coordinator-peer",
	}
	peer.start(t, listener)
	t.Cleanup(func() {
		_ = peer.Stop()
	})
	return peer
}

// Stop gracefully shuts down the coordinator
func (c *Coordinator) Stop() error {
	if !c.running {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.service.Stop(ctx); err != nil {
		return err
	}
	c.running = false
	return nil
}

// Restart replaces the coordinator process while retaining its endpoint and stores.
func (c *Coordinator) Restart(t *testing.T) {
	t.Helper()

	require.NoError(t, c.Stop(), "failed to stop coordinator")
	c.StartReplacement(t)
}

// StartReplacement starts a new coordinator process on the retained endpoint.
func (c *Coordinator) StartReplacement(t *testing.T) {
	t.Helper()

	listener, err := net.Listen("tcp", c.Address())
	require.NoError(t, err, "failed to recreate coordinator listener")
	c.restartCount++
	c.instanceID = fmt.Sprintf("test-coordinator-restart-%d", c.restartCount)
	c.start(t, listener)
}

// Address returns the address the coordinator is listening on
func (c *Coordinator) Address() string {
	return fmt.Sprintf("%s:%d", c.host, c.port)
}

// Port returns the port the coordinator is listening on
func (c *Coordinator) Port() int {
	return c.port
}

// InstanceID returns the current coordinator process identifier.
func (c *Coordinator) InstanceID() string {
	return c.instanceID
}

// RunHeartbeat sends a run heartbeat directly to this coordinator instance.
func (c *Coordinator) RunHeartbeat(t *testing.T, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	t.Helper()

	conn, err := grpc.NewClient(c.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "failed to create coordinator client")
	defer func() { _ = conn.Close() }()

	return coordinatorv1.NewCoordinatorServiceClient(conn).RunHeartbeat(c.Context, req)
}

// DispatchTask dispatches a task to a waiting worker
func (c *Coordinator) DispatchTask(t *testing.T, task *coordinatorv1.Task) error {
	t.Helper()

	conn, err := grpc.NewClient(c.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "failed to create gRPC client")
	defer func() { _ = conn.Close() }()

	client := coordinatorv1.NewCoordinatorServiceClient(conn)

	timeout := 5 * time.Second
	if runtime.GOOS == "windows" {
		timeout *= 5
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_, err = client.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
	return err
}

// GetCoordinatorClient returns a coordinator client for this coordinator
func (c *Coordinator) GetCoordinatorClient(t *testing.T) coordinator.Client {
	t.Helper()
	return coordinator.New(c.ServiceRegistry, CoordinatorClientConfig(c.Config.Paths.DataDir))
}

// CoordinatorClientConfig returns coordinator client settings backed by the test data directory.
func CoordinatorClientConfig(dataDir string) *coordinator.Config {
	config := coordinator.DefaultConfig()
	config.WorkspaceBundleDir = workspacebundle.StoreDir(dataDir)
	return config
}

// Handler returns the coordinator handler for direct testing
func (c *Coordinator) Handler() *coordinator.Handler {
	return c.handler
}

// LogDir returns the log directory path for verifying log persistence
func (c *Coordinator) LogDir() string {
	return c.Config.Paths.LogDir
}

// WithCoordinatorConfig creates a coordinator configuration option
func WithCoordinatorConfig(host string, port int) HelperOption {
	return func(opts *Options) {
		opts.CoordinatorHost = host
		opts.CoordinatorPort = port
	}
}

// waitForCoordinatorStart polls the coordinator health check until ready
func waitForCoordinatorStart(t *testing.T, addr string) {
	t.Helper()

	const (
		maxRetries = 20
		retryDelay = 100 * time.Millisecond
	)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "failed to create gRPC client for health check")
	defer func() { _ = conn.Close() }()

	healthClient := grpc_health_v1.NewHealthClient(conn)

	for range maxRetries {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		cancel()

		if err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_SERVING {
			return
		}

		time.Sleep(retryDelay)
	}

	t.Fatalf("coordinator failed to start within %v", maxRetries*retryDelay)
}
