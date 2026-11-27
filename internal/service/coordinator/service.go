// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type Service struct {
	server              *grpc.Server
	handler             *Handler
	grpcListener        net.Listener
	grpcHealthServer    *health.Server
	httpHealthServer    *healthcheck.Server
	registry            serviceregistry.ServiceRegistry
	cfg                 *config.Config
	instanceID          string
	hostPort            string
	configuredHost      string
	disableHealthServer bool
	// peerCA, when set, makes the registry entry report built-in TLS, and
	// its certificate stays published in publishedCAFile. caPublished
	// reports that no retry of the write at start is left, and previousCA is
	// what the file held when that write failed.
	peerCA          *PeerCA
	publishedCAFile string
	caPublished     atomic.Bool
	previousCA      []byte

	// For graceful shutdown
	mu         sync.Mutex
	stopCancel context.CancelFunc // Cancels the service's internal context
}

func NewService(
	server *grpc.Server,
	handler *Handler,
	grpcListener net.Listener,
	grpcHealthServer *health.Server,
	httpHealthServer *healthcheck.Server,
	registry serviceregistry.ServiceRegistry,
	cfg *config.Config,
	instanceID string,
	configuredHost string,
) *Service {
	return &Service{
		server:           server,
		handler:          handler,
		grpcListener:     grpcListener,
		grpcHealthServer: grpcHealthServer,
		httpHealthServer: httpHealthServer,
		registry:         registry,
		cfg:              cfg,
		instanceID:       instanceID,
		hostPort:         grpcListener.Addr().String(),
		configuredHost:   configuredHost,
	}
}

// DisableHealthServer disables the dedicated HTTP health check server.
func (srv *Service) DisableHealthServer() {
	srv.disableHealthServer = true
}

// ServeBuiltinTLS makes the registry entry report built-in TLS with ca.
// Whenever the entry is refreshed, the certificate of ca is written to
// publishedCAFile when the file is missing. When published is false, as
// after a failed write at start while the file held previous, each refresh
// first retries that write over previous, until it succeeds or another
// coordinator publishes. Call it before Start.
func (srv *Service) ServeBuiltinTLS(ca *PeerCA, publishedCAFile string, published bool, previous []byte) {
	srv.peerCA = ca
	srv.publishedCAFile = publishedCAFile
	srv.caPublished.Store(published)
	srv.previousCA = previous
}

func (srv *Service) republishCA(ctx context.Context) {
	var err error
	if srv.caPublished.Load() {
		err = srv.peerCA.republishCertificate(srv.publishedCAFile)
	} else {
		var done bool
		done, err = srv.peerCA.retryCertificate(srv.publishedCAFile, srv.previousCA)
		srv.caPublished.Store(done)
	}
	if err != nil {
		logger.Warn(ctx, "Failed to publish the peer CA certificate", tag.Error(err))
	}
}

func (srv *Service) Start(ctx context.Context) (err error) {
	coordinatorv1.RegisterCoordinatorServiceServer(srv.server, srv.handler)

	// Set the serving status for the coordinator service
	srv.grpcHealthServer.SetServingStatus(coordinatorv1.CoordinatorService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	// Also set the overall server status
	srv.grpcHealthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Create an internal context that can be cancelled by Stop()
	internalCtx, cancel := context.WithCancel(ctx)
	internalCtx = eventstore.WithContext(internalCtx, srv.handler.eventService, eventstore.Source{
		Service:  eventstore.SourceServiceCoordinator,
		Instance: srv.handler.eventSourceInstance,
	})
	srv.mu.Lock()
	srv.stopCancel = cancel
	srv.mu.Unlock()

	startedZombieDetector := false
	startedHealthServer := false
	defer func() {
		if err == nil {
			return
		}

		srv.grpcHealthServer.SetServingStatus(coordinatorv1.CoordinatorService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		srv.grpcHealthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		cancel()
		if startedZombieDetector {
			srv.handler.WaitZombieDetector()
			srv.handler.Close(cleanupCtx)
		}
		if startedHealthServer {
			srv.stopHealthServer(cleanupCtx, "Failed to stop coordinator health check server during startup cleanup")
		}
	}()

	// Start the zombie detector to clean up runs from crashed workers
	// Use configured interval or default to 45 seconds
	zombieInterval := 45 * time.Second
	if srv.cfg != nil && srv.cfg.Scheduler.ZombieDetectionInterval > 0 {
		zombieInterval = srv.cfg.Scheduler.ZombieDetectionInterval
	}
	srv.handler.StartZombieDetector(internalCtx, zombieInterval)
	startedZombieDetector = true
	logger.Info(ctx, "Started zombie detector", tag.Interval(zombieInterval))

	if srv.httpHealthServer != nil && !srv.disableHealthServer {
		if err := srv.httpHealthServer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start coordinator health check server: %w", err)
		}
		startedHealthServer = true
	}

	// Register with service registry if monitor is available
	if srv.registry != nil {
		// Parse port from listener address
		_, portStr, err := net.SplitHostPort(srv.hostPort)
		if err != nil {
			return fmt.Errorf("failed to parse host:port: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("failed to parse port number: %w", err)
		}

		hostInfo := serviceregistry.HostInfo{
			ID:     srv.instanceID,
			Host:   srv.configuredHost,
			Port:   port,
			Status: serviceregistry.ServiceStatusActive, // Coordinator is active when serving
		}
		if srv.peerCA != nil {
			hostInfo.TLS = serviceregistry.TLSBuiltin
			if observer, ok := srv.registry.(serviceregistry.RefreshObserver); ok {
				observer.OnRefresh(serviceregistry.ServiceNameCoordinator, srv.republishCA)
			}
		}
		if err := srv.registry.Register(ctx, serviceregistry.ServiceNameCoordinator, hostInfo); err != nil {
			return fmt.Errorf("failed to register with service registry: %w", err)
		}
		logger.Info(ctx, "Registered with service registry",
			tag.ServiceID(srv.instanceID),
			slog.String("configured-host", srv.configuredHost),
			tag.Port(port),
			tag.Addr(srv.hostPort))
	}

	go func() {
		logger.Info(ctx, "Starting to serve on coordinator service", tag.Addr(srv.hostPort))
		if err := srv.server.Serve(srv.grpcListener); err != nil {
			logger.Fatal(ctx, "Failed to serve on coordinator service listener")
		}
	}()

	return nil
}

func (srv *Service) Stop(ctx context.Context) error {
	// Set NOT_SERVING status when shutting down
	srv.grpcHealthServer.SetServingStatus(coordinatorv1.CoordinatorService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	srv.grpcHealthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	// Unregister from service registry if monitor is available
	if srv.registry != nil {
		srv.registry.Unregister(ctx)
		logger.Info(ctx, "Unregistered from service registry", tag.ServiceID(srv.instanceID))
	}

	t := time.AfterFunc(2*time.Second, func() {
		logger.Info(ctx, "ShutdownHandler: Drain time expired, stopping all traffic")
		srv.server.Stop()
	})

	srv.server.GracefulStop()
	t.Stop()

	// Cancel the internal context to signal zombie detector to stop
	srv.mu.Lock()
	cancel := srv.stopCancel
	srv.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	// Wait for zombie detector to finish before closing handler
	srv.handler.WaitZombieDetector()

	// Close handler resources (open attempts, etc.)
	srv.handler.Close(ctx)

	srv.stopHealthServer(ctx, "Failed to stop coordinator health check server")

	return nil
}

func (srv *Service) stopHealthServer(ctx context.Context, msg string) {
	if srv.httpHealthServer == nil || srv.disableHealthServer {
		return
	}
	if err := srv.httpHealthServer.Stop(ctx); err != nil {
		logger.Error(ctx, msg, tag.Error(err))
	}
}
