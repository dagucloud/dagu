// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

// grpcMaxMsgSize is the maximum message size for gRPC calls.
// Default gRPC limit is 4 MB; we increase to 16 MB to handle large status
// payloads that include LLM session messages from workers.
const grpcMaxMsgSize = 16 * 1024 * 1024

func CmdCoordinator() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "coordinator [flags]",
			Short: "Start the coordinator gRPC server for distributed task execution",
			Long: `Launch the coordinator gRPC server that handles distributed task execution.

The coordinator server provides a central point for distributed workers to:
- Poll for tasks to execute
- Fetch DAG definitions and workspace bundles
- Report task execution status, logs, artifacts, and persistent state
- Send heartbeats, task-claim acknowledgements, and cancellation requests

This server uses gRPC for efficient communication with remote workers and
supports authentication via signing keys and TLS encryption.

Flags:
  --coordinator.host string         Host address to bind the gRPC server to (default: 127.0.0.1)
  --coordinator.advertise string    Address to advertise in service registry (default: auto-detected hostname)
  --coordinator.port int            Port number for the gRPC server to listen on (default: 50055)
  --coordinator.health-port int     Port number for the HTTP health check server (default: 8091, 0 disables)
  --peer.cert-file string           Path to TLS certificate file for peer connections
  --peer.key-file string            Path to TLS key file for peer connections
  --peer.client-ca-file string      Path to CA certificate file for client verification (mTLS)
  --peer.insecure                   Use insecure connection (h2c) instead of TLS
  --peer.skip-tls-verify            Skip TLS certificate verification (insecure)

Example:
  # Basic usage
  dagu coordinator --coordinator.host=0.0.0.0 --coordinator.port=50055

  # Bind to all interfaces and advertise service name (for containers/K8s)
  dagu coordinator --coordinator.host=0.0.0.0 --coordinator.advertise=dagu-server

  # Disable the dedicated HTTP health server
  dagu coordinator --coordinator.health-port=0

  # With TLS
  dagu coordinator --peer.cert-file=server.crt --peer.key-file=server.key

  # With mutual TLS
  dagu coordinator --peer.cert-file=server.crt --peer.key-file=server.key --peer.client-ca-file=ca.crt

This process runs continuously in the foreground until terminated.
`,
		}, coordinatorFlags, runCoordinator,
	)
}

var coordinatorFlags = []commandLineFlag{
	coordinatorHostFlag,
	coordinatorPortFlag,
	coordinatorHealthPortFlag,
	coordinatorAdvertiseFlag,
	// Peer configuration flags for TLS
	peerInsecureFlag,
	peerCertFileFlag,
	peerKeyFileFlag,
	peerClientCAFileFlag,
	peerSkipTLSVerifyFlag,
}

func runCoordinator(ctx *Context, _ []string) error {
	signalCtx, stop := notifyShutdownContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	coordCtx := ctx.WithContext(signalCtx).WithEventSource(eventstore.SourceServiceCoordinator)
	stores := coordCtx.runtimeStores()
	svc, _, err := newCoordinator(coordCtx, stores.SecretStore, stores.ProfileStore)
	if err != nil {
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}

	if err := svc.Start(coordCtx); err != nil {
		return fmt.Errorf("failed to start coordinator: %w", err)
	}

	// Wait for context cancellation
	<-coordCtx.Done()
	// Let a second SIGINT end shutdown; SIGTERM stays absorbed.
	stop()
	logger.Info(coordCtx, "Coordinator shutting down")

	// coordCtx is done, so shutdown gets the parent context.
	if err := svc.Stop(ctx.WithEventSource(eventstore.SourceServiceCoordinator)); err != nil {
		return fmt.Errorf("failed to stop coordinator: %w", err)
	}

	return nil
}

// newCoordinator constructs the coordinator service and handler.
func newCoordinator(
	ctx *Context,
	secretStore secret.Store,
	profileStore profile.Store,
) (*coordinator.Service, *coordinator.Handler, error) {
	cfg := ctx.Config
	persistence := ctx.Persistence
	// Generate instance ID
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	instanceID := fmt.Sprintf("%s@%d", hostname, cfg.Coordinator.Port)

	// Determine advertise address for service registry
	advertiseAddr := cfg.Coordinator.Advertise
	if advertiseAddr == "" {
		// No advertise address specified, try auto-detection
		if hostname != "unknown" {
			// Use auto-detected hostname
			advertiseAddr = hostname
		} else {
			// Hostname detection failed, fallback to configured host
			advertiseAddr = cfg.Coordinator.Host
			// Warn if fallback address is potentially invalid
			if advertiseAddr == "0.0.0.0" || advertiseAddr == "127.0.0.1" {
				logger.Warn(ctx, "Coordinator advertise address fallback is potentially invalid for service discovery",
					tag.Addr(advertiseAddr),
					tag.Reason("hostname detection failed and no explicit advertise address provided; workers may not be able to connect"),
				)
			} else {
				logger.Warn(ctx, "Coordinator advertise address fallback to configured host due to hostname detection failure",
					tag.Addr(advertiseAddr),
				)
			}
		}
	}

	logger.Info(ctx, "Coordinator initialization",
		slog.String("bind-address", cfg.Coordinator.Host),
		slog.String("advertise-address", advertiseAddr),
		tag.Port(cfg.Coordinator.Port),
		slog.String("instance-id", instanceID),
	)
	logAIConfig(ctx)
	// Worker pools bind the labels of a worker to its client certificate
	// (Spec 092). They need mutual TLS, so the check comes before the listener.
	workerPools, err := coordinator.NewWorkerPools(cfg.Coordinator.WorkerPools, cfg.Coordinator.Dispatchers, cfg.Core.Peer)
	if err != nil {
		return nil, nil, err
	}
	if workerPools.Enabled() {
		logger.Info(ctx, "Coordinator worker pools", slog.Any("pools", workerPoolSummaries(workerPools)), slog.Any("dispatchers", workerPools.Dispatchers()))
		if hasAgentPool(workerPools) && cfg.Coordinator.WorkerWaitTimeout <= 0 {
			// An agent worker takes one run and restarts. While it restarts, a start
			// with coordinator.worker_wait_timeout 0 is refused at once (Spec 092).
			logger.Warn(ctx, "Coordinator has an agent worker pool and coordinator.worker_wait_timeout is 0: "+
				"a run that needs an agent worker is refused while the agent worker restarts; set coordinator.worker_wait_timeout")
		}
	}

	// Create gRPC server options
	var serverOpts []grpc.ServerOption

	serverOpts = append(serverOpts,
		grpc.MaxRecvMsgSize(grpcMaxMsgSize),
		grpc.MaxSendMsgSize(grpcMaxMsgSize),
		// A relay stream can stay open for a long time. The pings find a connection
		// whose peer is gone, so its streams end and their slots go back.
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: coordinatorKeepaliveTime, Timeout: coordinatorKeepaliveTimeout}),
	)
	serverOpts = append(serverOpts, workerPools.ServerOptions()...)

	// Configure TLS using global peer config
	if cfg.Core.Peer.CertFile != "" && cfg.Core.Peer.KeyFile != "" {
		// Load server certificates
		creds, err := loadCoordinatorTLSCredentials(&config.TLSConfig{
			CertFile: cfg.Core.Peer.CertFile,
			KeyFile:  cfg.Core.Peer.KeyFile,
			CAFile:   cfg.Core.Peer.ClientCaFile,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}

	// Create gRPC server with options
	grpcServer := grpc.NewServer(serverOpts...)

	// Create health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Create dedicated HTTP health server
	httpHealthServer := healthcheck.NewServer("coordinator", cfg.Coordinator.HealthPort)

	// Create listener
	addr := fmt.Sprintf("%s:%d", cfg.Coordinator.Host, cfg.Coordinator.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create listener on %s: %w", addr, err)
	}

	// The gateway serves the relay of the workers (Spec 094). It reads the
	// endpoints and their keys, and calls the providers with the limits of the
	// block ai.
	aiGateway, err := newAIGateway(ctx, secretStore)
	if err != nil {
		return nil, nil, err
	}

	// Create the handler with DAG-run status persistence and streamed log storage.
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunRepository:          persistence.DAGRunRepository,
		StateStore:                persistence.StateStore,
		LogDir:                    cfg.Paths.LogDir,
		ArtifactDir:               cfg.Paths.ArtifactDir,
		WorkspaceBundleDir:        workspacebundle.StoreDir(cfg.Paths.DataDir),
		Owner:                     dispatch.CoordinatorEndpoint{ID: instanceID, Host: advertiseAddr, Port: cfg.Coordinator.Port},
		DispatchTaskStore:         persistence.DispatchTaskStore,
		WorkerHeartbeatStore:      persistence.WorkerHeartbeatStore,
		DAGRunLeaseStore:          persistence.DAGRunLeaseStore,
		ActiveDistributedRunStore: persistence.ActiveDistributedRunStore,
		DAGRepository:             persistence.DAGRepository,
		SecretStore:               secretStore,
		ProfileStore:              profileStore,
		AgentSessionCleanupQueue:  persistence.AgentSessionCleanupQueue,
		WorkerPools:               workerPools,
		AIGateway:                 aiGateway,
		WorkerWaitTimeout:         cfg.Coordinator.WorkerWaitTimeout,
		EventService:              ctx.event,
		EventSourceInstance:       ctx.EventSourceInstance,
	})

	// Create and return service with advertise address for service registry
	return coordinator.NewService(grpcServer, handler, listener, healthServer, httpHealthServer, persistence.ServiceRegistry, cfg, instanceID, advertiseAddr), handler, nil
}

const (
	// coordinatorKeepaliveTime is the idle time of a connection after which the
	// coordinator pings the peer.
	coordinatorKeepaliveTime = time.Minute
	// coordinatorKeepaliveTimeout is the time that the peer has to answer a ping
	// before the coordinator closes the connection.
	coordinatorKeepaliveTimeout = 20 * time.Second
)

// hasAgentPool reports whether a pool of the coordinator serves a CLI provider.
func hasAgentPool(pools *coordinator.WorkerPools) bool {
	for _, pool := range pools.Pools() {
		for key := range pool.Labels {
			if strings.HasPrefix(key, ir.AgentProviderLabelPrefix) {
				return true
			}
		}
	}
	return false
}

// workerPoolSummaries renders each pool as name, identities and labels, for the
// start log.
func workerPoolSummaries(pools *coordinator.WorkerPools) []string {
	var out []string
	for _, pool := range pools.Pools() {
		out = append(out, fmt.Sprintf("%s identities=%v labels=%s", pool.Name, pool.Identities, ir.FormatQuotedWorkerSelector(pool.Labels)))
	}
	return out
}

// loadCoordinatorTLSCredentials loads TLS credentials for the coordinator server.
// It supports both standard TLS and mutual TLS (mTLS) configurations.
func loadCoordinatorTLSCredentials(tlsConfig *config.TLSConfig) (credentials.TransportCredentials, error) {
	if tlsConfig == nil {
		return nil, fmt.Errorf("TLS configuration is nil")
	}

	// Load server certificate and key
	cert, err := tls.LoadX509KeyPair(tlsConfig.CertFile, tlsConfig.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificates: %w", err)
	}

	// Create TLS configuration
	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS12,
	}

	// If CA file is provided, enable mutual TLS
	if tlsConfig.CAFile != "" {
		// Load CA certificate
		caCert, err := os.ReadFile(tlsConfig.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		// Create certificate pool
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		// Update TLS config for mutual TLS
		config.ClientAuth = tls.RequireAndVerifyClientCert
		config.ClientCAs = certPool
	}

	return credentials.NewTLS(config), nil
}
