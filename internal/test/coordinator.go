// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/healthcheck"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// Coordinator represents a test gRPC coordinator instance
type Coordinator struct {
	Helper
	service      *coordinator.Service
	handler      *coordinator.Handler
	grpcServer   *grpc.Server
	healthServer *health.Server
	listener     net.Listener
	logDir       string // Log directory for remote log streaming
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

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Create health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Build handler config based on coordinator test options
	cfg := coordinator.HandlerConfig{}
	if options.WithStatusPersistence {
		cfg.DAGRunStore = helper.DAGRunStore
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
	cfg.Owner = exec.CoordinatorEndpoint{ID: "test-coordinator", Host: "127.0.0.1", Port: port}
	cfg.DispatchTaskStore = helper.DispatchTaskStore
	cfg.WorkerHeartbeatStore = helper.WorkerHeartbeatStore
	cfg.DAGRunLeaseStore = helper.DAGRunLeaseStore
	cfg.ActiveDistributedRunStore = helper.ActiveDistributedRunStore

	// Create handler with config
	handler := coordinator.NewHandler(cfg)

	// Create service with ServiceMonitor from helper
	service := coordinator.NewService(
		grpcServer,
		handler,
		listener,
		healthServer,
		healthcheck.NewServer("coordinator", 0),
		helper.ServiceRegistry,
		helper.Config,
		"test-coordinator",
		"127.0.0.1",
	)

	coord := &Coordinator{
		Helper:       helper,
		service:      service,
		handler:      handler,
		grpcServer:   grpcServer,
		healthServer: healthServer,
		listener:     listener,
		logDir:       helper.Config.Paths.LogDir,
	}

	// Start the coordinator
	err = service.Start(helper.Context)
	require.NoError(t, err, "failed to start coordinator")

	// Wait for the coordinator to be ready
	waitForCoordinatorStart(t, fmt.Sprintf("127.0.0.1:%d", port))

	// Setup cleanup
	t.Cleanup(func() {
		_ = coord.Stop()
	})

	return coord
}

// Stop gracefully shuts down the coordinator
func (c *Coordinator) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return c.service.Stop(ctx)
}

// Address returns the address the coordinator is listening on
func (c *Coordinator) Address() string {
	return c.listener.Addr().String()
}

// Port returns the port the coordinator is listening on
func (c *Coordinator) Port() int {
	return c.listener.Addr().(*net.TCPAddr).Port
}

// DispatchTask dispatches a task to a waiting worker
func (c *Coordinator) DispatchTask(t *testing.T, task *coordinatorv1.Task) error {
	t.Helper()

	conn, err := grpc.NewClient(c.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, "failed to create gRPC client")
	defer func() { _ = conn.Close() }()

	client := coordinatorv1.NewCoordinatorServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Dispatch(ctx, &coordinatorv1.DispatchRequest{Task: task})
	return err
}

// GetCoordinatorClient returns a coordinator client for this coordinator
func (c *Coordinator) GetCoordinatorClient(t *testing.T) coordinator.Client {
	t.Helper()

	// Create coordinator client config
	config := coordinator.DefaultConfig()
	config.Insecure = true

	// Create coordinator client - cast to Client interface
	return coordinator.New(c.ServiceRegistry, config)
}

// Handler returns the coordinator handler for direct testing
func (c *Coordinator) Handler() *coordinator.Handler {
	return c.handler
}

// LogDir returns the log directory path for verifying log persistence
func (c *Coordinator) LogDir() string {
	return c.logDir
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
