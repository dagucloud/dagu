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

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/service/healthcheck"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// grpcMaxMsgSize is the maximum message size for gRPC calls.
// Default gRPC limit is 4 MB; we increase to 16 MB to handle large status
// payloads that include LLM session messages in shared-nothing mode.
const grpcMaxMsgSize = 16 * 1024 * 1024

func CmdCoordinator() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "coordinator [flags]",
			Short: "Start the coordinator gRPC server for distributed task execution",
			Long: `Launch the coordinator gRPC server that handles distributed task execution.

The coordinator server provides a central point for distributed workers to:
- Poll for tasks to execute
- Fetch DAG definitions (To be implemented)
- Report task execution status (To be implemented)
- Register themselves with the system (to be implemented)

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
	coordCtx := ctx.WithEventSource(eventstore.SourceServiceCoordinator)
	svc, _, err := newCoordinator(
		coordCtx,
		coordCtx.Config,
		coordCtx.ServiceRegistry,
		coordCtx.DAGRunStore,
		coordCtx.DispatchTaskStore,
		coordCtx.WorkerHeartbeatStore,
		coordCtx.DAGRunLeaseStore,
		coordCtx.ActiveDistributedRunStore,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}

	if err := svc.Start(coordCtx); err != nil {
		return fmt.Errorf("failed to start coordinator: %w", err)
	}

	// Wait for context cancellation
	<-coordCtx.Done()
	logger.Info(coordCtx, "Coordinator shutting down")

	if err := svc.Stop(coordCtx); err != nil {
		return fmt.Errorf("failed to stop coordinator: %w", err)
	}

	return nil
}

// newCoordinator creates and configures a Coordinator service with its gRPC server,
// health server, network listener, and handler, ready for registration in the service registry.
// It derives an instance ID from the host name and configured port and determines an
// advertise address (using cfg.Coordinator.Advertise, auto-detected hostname, or a
// configured host fallback); a warning is logged when the fallback address may be
// unsuitable for discovery. If peer TLS certificate and key files are provided in
// cfg.Core.Peer, it loads TLS credentials for the gRPC server. It binds a TCP listener
// to cfg.Coordinator.Host:cfg.Coordinator.Port and returns an initialized coordinator.Service.
// It returns an error if any part of setup (TLS loading, listener binding, etc.) fails.
func newCoordinator(
	ctx *Context,
	cfg *config.Config,
	registry exec.ServiceRegistry,
	dagRunStore exec.DAGRunStore,
	dispatchTaskStore exec.DispatchTaskStore,
	workerHeartbeatStore exec.WorkerHeartbeatStore,
	dagRunLeaseStore exec.DAGRunLeaseStore,
	activeDistributedRunStore exec.ActiveDistributedRunStore,
) (*coordinator.Service, *coordinator.Handler, error) {
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
	// Create gRPC server options
	var serverOpts []grpc.ServerOption

	serverOpts = append(serverOpts,
		grpc.MaxRecvMsgSize(grpcMaxMsgSize),
		grpc.MaxSendMsgSize(grpcMaxMsgSize),
	)

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

	// Create handler with DAGRunStore for status persistence and LogDir for log streaming
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunStore:               dagRunStore,
		LogDir:                    cfg.Paths.LogDir,
		ArtifactDir:               cfg.Paths.ArtifactDir,
		WorkspaceBundleDir:        workspacebundle.StoreDir(cfg.Paths.DataDir),
		Owner:                     exec.CoordinatorEndpoint{ID: instanceID, Host: advertiseAddr, Port: cfg.Coordinator.Port},
		DispatchTaskStore:         dispatchTaskStore,
		WorkerHeartbeatStore:      workerHeartbeatStore,
		DAGRunLeaseStore:          dagRunLeaseStore,
		ActiveDistributedRunStore: activeDistributedRunStore,
		EventService:              ctx.EventService,
		EventSourceInstance:       ctx.EventSourceInstance,
	})

	// Create and return service with advertise address for service registry
	return coordinator.NewService(grpcServer, handler, listener, healthServer, httpHealthServer, registry, cfg, instanceID, advertiseAddr), handler, nil
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
