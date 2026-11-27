// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"syscall"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// grpcMaxMsgSize is the maximum message size for gRPC calls.
// Default gRPC limit is 4 MB; we increase to 16 MB to handle large status
// payloads that include LLM session messages from workers.
const grpcMaxMsgSize = 16 * 1024 * 1024

// coordinatorLineFormat is the line a coordinator logs at start for what it
// serves, from the transport and what selected it.
const coordinatorLineFormat = "Coordinator transport: %s (from %s)"

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
  --peer.ca-cert-file string        Path to the CA certificate that signs peer certificates
  --peer.ca-key-file string         Path to the private key of --peer.ca-cert-file

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
	peerCACertFileFlag,
	peerCAKeyFileFlag,
}

func runCoordinator(ctx *Context, _ []string) error {
	signalCtx, stop := notifyShutdownContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	coordCtx := ctx.WithContext(signalCtx).WithEventSource(eventstore.SourceServiceCoordinator)
	stores := coordCtx.runtimeStores()
	peerCA, err := prepareCoordinatorPeerCA(coordCtx, coordCtx.Config)
	if err != nil {
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}
	svc, _, err := newCoordinator(coordCtx, peerCA, stores.SecretStore, stores.ProfileStore, coordCtx.serviceInventoryStore(), stores.HostKeys)
	if err != nil {
		peerCA.unpublish()
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}

	if err := svc.Start(coordCtx); err != nil {
		peerCA.unpublish()
		return fmt.Errorf("failed to start coordinator: %w", err)
	}
	coordCtx.followProjects(coordCtx)

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

// newCoordinator constructs the coordinator service and handler with peerCA,
// from prepareCoordinatorPeerCA.
func newCoordinator(
	ctx *Context,
	peerCA preparedPeerCA,
	secretStore secret.Store,
	profileStore profile.Store,
	nodeInventory inventory.Store,
	hostKeys inventory.HostKeyStore,
) (*coordinator.Service, *coordinator.Handler, error) {
	cfg := ctx.Config
	persistence := ctx.Persistence
	// Generate instance ID
	hostname, err := os.Hostname()
	// The built-in TLS certificate names the host name only when it is known.
	certHostname := hostname
	if err != nil {
		hostname = "unknown"
		certHostname = ""
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

	peerAuth, err := coordinatorPeerAuth(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	workerNodes, err := ctx.workerNodeStore()
	if err != nil {
		return nil, nil, err
	}
	workerInvites, err := ctx.serverStores.WorkerInviteStore()
	if err != nil {
		return nil, nil, err
	}
	workerCertificates, err := ctx.serverStores.WorkerCertificateStore()
	if err != nil {
		return nil, nil, err
	}
	if peerCA.ca != nil {
		peerAuth.AcceptWorkerCertificates(peerCA.ca)
	}
	transportOpts, err := coordinatorTransport(ctx, cfg, peerCA.ca, advertiseAddr, certHostname)
	if err != nil {
		return nil, nil, err
	}
	serverOpts = append(serverOpts, transportOpts...)
	serverOpts = append(serverOpts,
		grpc.ChainUnaryInterceptor(peerAuth.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(peerAuth.StreamInterceptor()),
	)

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

	connections, tokens := coordinatorConnections(ctx)
	// Create the handler with DAG-run status persistence and streamed log storage.
	handler := coordinator.NewHandler(coordinator.HandlerConfig{
		DAGRunRepository:          persistence.DAGRunRepository,
		StateStore:                persistence.StateStore,
		LogDir:                    ctx.projectPaths().LogDir,
		ArtifactDir:               ctx.projectPaths().ArtifactDir,
		WorkspaceBundleDir:        workspacebundle.StoreDir(cfg.Paths.DataDir),
		Owner:                     dispatch.CoordinatorEndpoint{ID: instanceID, Host: advertiseAddr, Port: cfg.Coordinator.Port},
		DispatchTaskStore:         persistence.DispatchTaskStore,
		WorkerHeartbeatStore:      persistence.WorkerHeartbeatStore,
		DAGRunLeaseStore:          persistence.DAGRunLeaseStore,
		ActiveDistributedRunStore: persistence.ActiveDistributedRunStore,
		DAGRepository:             persistence.DAGRepository,
		SecretStore:               secretStore,
		ProfileStore:              profileStore,
		ConnectionStore:           connections,
		ConnectionTokens:          tokens,
		ProjectsEnabled:           cfg.ProjectsEnabled,
		NodeInventory:             nodeInventory,
		TrustedHostKeys:           hostKeys,
		AgentSessionCleanupQueue:  persistence.AgentSessionCleanupQueue,
		WorkerNodeStore:           workerNodes,
		JoinReadiness:             coordinatorJoinReadiness(cfg, peerCA.ca),
		PeerCA:                    peerCA.ca,
		WorkerInviteStore:         workerInvites,
		WorkerCertificateStore:    workerCertificates,
		WorkerWaitTimeout:         cfg.Coordinator.WorkerWaitTimeout,
		EventService:              ctx.event,
		EventSourceInstance:       ctx.EventSourceInstance,
	})

	// Create and return service with advertise address for service registry
	svc := coordinator.NewService(grpcServer, handler, listener, healthServer, httpHealthServer, persistence.ServiceRegistry, cfg, instanceID, advertiseAddr)
	if peerCA.ca != nil {
		svc.ServeBuiltinTLS(peerCA.ca, cfg.Core.Peer.PublishedCAFile, peerCA.published, peerCA.previous)
	}
	return svc, handler, nil
}

// preparedPeerCA is the peer CA of a coordinator, if any, and whether its
// certificate is published.
type preparedPeerCA struct {
	ca        *coordinator.PeerCA
	published bool
	// previous is what the published CA certificate held when publishing
	// failed, nil for no file.
	previous []byte
	// undo removes the certificate that preparing published.
	undo func()
}

// unpublish removes the certificate that preparing published, for a
// coordinator that does not start.
func (p preparedPeerCA) unpublish() {
	if p.undo != nil {
		p.undo()
	}
}

// prepareCoordinatorPeerCA returns the coordinator's peer CA, from
// coordinatorPeerCA, and keeps the published CA certificate in the data
// directory right for the clients that share it: the certificate of that CA,
// and none for a coordinator with own certificates. A coordinator that could
// not prepare its generated CA leaves the file to the other coordinators that
// share the data directory.
func prepareCoordinatorPeerCA(ctx context.Context, cfg *config.Config) (preparedPeerCA, error) {
	peer := cfg.Core.Peer
	ca, err := coordinatorPeerCA(ctx, peer, cfg.Coordinator.RequireTLS)
	if err != nil {
		return preparedPeerCA{}, err
	}
	var undo func()
	switch {
	case ca != nil:
		undo, err = ca.PublishCertificate(peer.PublishedCAFile)
	case peer.CertFile != "" && peer.KeyFile != "":
		err = coordinator.RemovePublishedCertificate(peer.PublishedCAFile)
	}
	prepared := preparedPeerCA{ca: ca, published: err == nil, undo: undo}
	if err != nil {
		logger.Warn(ctx, "Clients that share the data directory may not use built-in TLS", tag.Error(err))
		prepared.previous, _ = fileutil.ReadFile(peer.PublishedCAFile)
	}
	return prepared, nil
}

// coordinatorTransport returns the server options that set what the
// coordinator serves, and logs it: TLS with own certificates, or built-in TLS
// and h2c on one port with peerCA, the CA from coordinatorPeerCA, or h2c
// without either. Under coordinator.require_tls, built-in TLS refuses calls
// over h2c. The built-in TLS certificate names advertise and hostname, when
// not empty.
func coordinatorTransport(ctx context.Context, cfg *config.Config, peerCA *coordinator.PeerCA, advertise, hostname string) ([]grpc.ServerOption, error) {
	peer := cfg.Core.Peer
	switch {
	case peer.CertFile != "" && peer.KeyFile != "":
		cert, err := loadPeerKeyPair(peer)
		if err != nil {
			return nil, err
		}
		warnCoordinatorCertificate(ctx, peer.CertFile, cert.Leaf, advertise)
		creds, err := loadCoordinatorTLSCredentials(cert, peer.ClientCaFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		logger.Info(ctx, fmt.Sprintf(coordinatorLineFormat, "TLS", "peer.cert_file"))
		return []grpc.ServerOption{grpc.Creds(creds)}, nil
	case peerCA == nil:
		logger.Info(ctx, fmt.Sprintf(coordinatorLineFormat, "h2c", "no peer CA"))
		return nil, nil
	}
	creds, err := coordinator.NewPeerTransportCredentials(ctx, peerCA, advertise, hostname)
	if err != nil {
		return nil, err
	}
	opts := []grpc.ServerOption{grpc.Creds(creds)}
	switch {
	case cfg.Coordinator.RequireTLS:
		logger.Info(ctx, fmt.Sprintf(coordinatorLineFormat, "built-in TLS only", "coordinator.require_tls"))
		// These run before the peer token check, so an h2c call learns that
		// it needs TLS.
		opts = append(opts,
			grpc.ChainUnaryInterceptor(coordinator.RequireTLSUnaryInterceptor()),
			grpc.ChainStreamInterceptor(coordinator.RequireTLSStreamInterceptor()),
		)
	case peer.CACertFile != "":
		logger.Info(ctx, fmt.Sprintf(coordinatorLineFormat, "built-in TLS and h2c", "peer.ca_cert_file"))
	default:
		logger.Info(ctx, fmt.Sprintf(coordinatorLineFormat, "built-in TLS and h2c", "the generated CA"))
	}
	return opts, nil
}

// coordinatorJoinReadiness returns the settings the coordinator reports as
// its join readiness. peerCA is the CA from coordinatorPeerCA; with it, the
// coordinator serves built-in TLS.
func coordinatorJoinReadiness(cfg *config.Config, peerCA *coordinator.PeerCA) coordinator.JoinReadinessConfig {
	configFile := cfg.Paths.ConfigFileUsed
	if configFile == "" {
		configFile = cfg.Paths.DefaultConfigFile
	}
	readiness := coordinator.JoinReadinessConfig{
		ListenHost: cfg.Coordinator.Host,
		Port:       cfg.Coordinator.Port,
		ConfigFile: configFile,
		HostSource: cfg.Sources["coordinator.host"],
		Advertise:  cfg.Coordinator.Advertise,
	}
	switch {
	case peerCA != nil:
		readiness.BuiltinTLS = true
	case cfg.Core.Peer.CertFile != "" && cfg.Core.Peer.KeyFile != "":
		readiness.BuiltinTLSReason = coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES
	default:
		// Without own certificates, only a generated CA that could not be
		// created or read leaves the coordinator without a peer CA.
		readiness.BuiltinTLSReason = coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_CA_UNAVAILABLE
	}
	return readiness
}

// coordinatorConnections returns the connection store and the token broker
// that answer runs on workers, or nils when the store cannot be opened. The
// coordinator runs for long, so its broker caches no token and reads the
// token state on each request.
func coordinatorConnections(ctx *Context) (connection.Store, broker.Source) {
	if ctx.serverStores == nil {
		return nil, nil
	}
	store, err := ctx.serverStores.ConnectionStore()
	if err == nil {
		var tokens *broker.Broker
		if tokens, err = broker.New(broker.Config{Store: store, Locks: ctx.serverStores.ConnectionLocks()}); err == nil {
			return store, tokens
		}
	}
	logger.Warn(ctx, "Connections are unavailable to runs on workers", tag.Error(err))
	return nil, nil
}

// coordinatorPeerAuth checks the peer token on coordinator calls: the
// configured peer.token, or the token generated in the data directory. When the
// generated token cannot be prepared and no token is required, the coordinator
// still starts and accepts calls with a warning, as it did before peer tokens.
func coordinatorPeerAuth(ctx *Context, cfg *config.Config) (*coordinator.PeerTokenAuth, error) {
	require := cfg.Coordinator.RequireToken
	if token := cfg.Core.Peer.Token; token != "" {
		return coordinator.NewPeerTokenAuth(token, true, require), nil
	}
	token, err := coordinator.LoadOrCreatePeerToken(cfg.Core.Peer.TokenFile)
	if err != nil {
		if require {
			return nil, fmt.Errorf("coordinator.require_token is set, but the peer token could not be prepared: %w", err)
		}
		logger.Warn(ctx, "Coordinator runs without a peer token", tag.Error(err))
		return coordinator.NewPeerTokenAuth("", false, false), nil
	}
	logger.Info(ctx, "Coordinator peer token",
		slog.String("file", cfg.Core.Peer.TokenFile),
		slog.Bool("required", require),
	)
	return coordinator.NewPeerTokenAuth(token, false, require), nil
}

// coordinatorPeerCA returns the CA that signs coordinator and worker
// certificates: the one peer.ca_cert_file and peer.ca_key_file supply, or the
// one generated in the data directory. A coordinator with own certificates
// has none. When the generated CA cannot be prepared, it logs a warning and
// returns no CA and no error, so the coordinator still starts on h2c, unless
// requireTLS forbids h2c.
func coordinatorPeerCA(ctx context.Context, peer config.Peer, requireTLS bool) (*coordinator.PeerCA, error) {
	switch {
	case peer.CertFile != "" && peer.KeyFile != "":
		return nil, nil
	case peer.CACertFile == "" && peer.CAKeyFile != "":
		return nil, errors.New("peer.ca_key_file needs peer.ca_cert_file")
	case peer.CACertFile != "" && peer.CAKeyFile == "":
		return nil, errors.New("peer.ca_cert_file needs peer.ca_key_file on a coordinator")
	case peer.CACertFile != "":
		ca, err := coordinator.LoadPeerCA(peer.CACertFile, peer.CAKeyFile)
		if err != nil {
			return nil, fmt.Errorf("peer.ca_cert_file and peer.ca_key_file: %w", err)
		}
		return ca, nil
	}
	ca, err := coordinator.LoadOrCreatePeerCA(peer.GeneratedCAFile)
	if err != nil && requireTLS {
		return nil, fmt.Errorf("coordinator.require_tls is set, but the peer CA could not be prepared: %w", err)
	}
	if err != nil {
		logger.Warn(ctx, "Coordinator runs without a peer CA", tag.Error(err))
		return nil, nil
	}
	return ca, nil
}

// loadCoordinatorTLSCredentials returns the coordinator's server credentials
// for cert. A non-empty clientCAFile enables mutual TLS.
func loadCoordinatorTLSCredentials(cert tls.Certificate, clientCAFile string) (credentials.TransportCredentials, error) {
	// Create TLS configuration
	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS12,
	}

	// If CA file is provided, enable mutual TLS
	if clientCAFile != "" {
		// Load CA certificate
		caCert, err := fileutil.ReadFile(clientCAFile)
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
