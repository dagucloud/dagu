// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	frontendfile "github.com/dagucloud/dagu/v2/internal/service/frontend/file"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/spf13/cobra"
)

func StartAll() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "start-all [flags]",
			Short: "Launch web UI server, scheduler, and optionally coordinator in a single process",
			Long: `Simultaneously start the web UI server, scheduler, and optionally coordinator in a single command.

This convenience command combines the functionality of 'dagu server', 'dagu scheduler',
and optionally 'dagu coordinator' into a single process. The web UI provides the management
interface, the scheduler handles automated DAG-run execution based on defined schedules,
and the coordinator (when enabled) manages distributed task execution across workers.

The coordinator is enabled by default and can be disabled by setting coordinator.enabled=false
in the config file or DAGU_COORDINATOR_ENABLED=false as an environment variable.

Flags:
  --host string                     Host address to bind the web server to (default: 127.0.0.1)
  --port int                        Port number for the web server to listen on (default: 8080)
  --dags string                     Path to the directory containing DAG definition files
  --coordinator.host string         Host address to bind the coordinator gRPC server to (default: 127.0.0.1)
  --coordinator.advertise string    Address to advertise in service registry (default: auto-detected hostname)
  --coordinator.port int            Port number for the coordinator gRPC server (default: 50055)
  --peer.cert-file string           Path to TLS certificate file for peer connections
  --peer.key-file string            Path to TLS key file for peer connections
  --peer.client-ca-file string      Path to CA certificate file for client verification (mTLS)
  --peer.insecure                   Use insecure connection (h2c) instead of TLS
  --peer.skip-tls-verify            Skip TLS certificate verification (insecure)

Example:
  # Default mode (coordinator enabled)
  dagu start-all

  # Disable coordinator
  DAGU_COORDINATOR_ENABLED=false dagu start-all

  # Distributed mode with coordinator on all interfaces
  dagu start-all --coordinator.host=0.0.0.0 --coordinator.port=50055

  # Production with both web and coordinator on all interfaces
  dagu start-all --host=0.0.0.0 --port=8080 --coordinator.host=0.0.0.0

This process runs continuously in the foreground until terminated.
`,
		}, startAllFlags, runStartAll,
	)
}

var startAllFlags = []commandLineFlag{
	dagsFlag,
	hostFlag,
	portFlag,
	coordinatorHostFlag,
	coordinatorPortFlag,
	coordinatorAdvertiseFlag,
	// Peer configuration flags for TLS
	peerInsecureFlag,
	peerCertFileFlag,
	peerKeyFileFlag,
	peerClientCAFileFlag,
	peerSkipTLSVerifyFlag,
}

// runStartAll starts the scheduler, web server, resource monitoring service, and optionally
// the coordinator in-process, then manages their lifecycles and graceful shutdown.
// It creates a signal-aware context, decides whether to enable the coordinator based on
// coordinator host binding, waits for an error or termination signal, shuts down services
// in order, and returns the first service error encountered (if any).
func runStartAll(ctx *Context, _ []string) error {
	if dagsDir, _ := ctx.Command.Flags().GetString("dags"); dagsDir != "" {
		ctx.Config.Paths.DAGsDir = dagsDir
	}

	// Stop license manager on shutdown
	if ctx.LicenseManager != nil {
		defer ctx.LicenseManager.Stop()
	}

	// Create a context that will be cancelled on interrupt signal.
	// This must be created BEFORE server initialization so auth provider init can be cancelled.
	signalCtx, stop := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Create a signal-aware context for services (used for auth init and all service operations)
	serviceCtx := ctx.WithContext(signalCtx)
	if _, err := persisfile.NewDAGSettingsStore(
		serviceCtx.Config,
		serviceCtx.backend.Collection(persis.CollectionDAGSettings),
	); err != nil {
		return fmt.Errorf("failed to initialize DAG settings store: %w", err)
	}
	stores, err := frontendfile.NewStores(serviceCtx, serviceCtx.Config, serviceCtx.backend)
	if err != nil {
		return err
	}
	serviceCtx = serviceCtx.withEvent(stores.Event)

	var collector func(context.Context)
	if stores.Event != nil {
		eventCollector, err := fileeventstore.NewCollector(
			serviceCtx.Config.Paths.EventStoreDir,
			serviceCtx.Config.EventStore.RetentionDays,
		)
		if err != nil {
			logger.Warn(serviceCtx, "Failed to initialize event collector; continuing without collection", tag.Error(err))
		} else {
			collector = eventCollector.Start
		}
	}
	schedulerDeps := scheduler.Dependencies{
		DAGSettingsStore:     stores.DAGSettings,
		ProfileStore:         stores.Profile,
		EventService:         stores.Event,
		EventCollector:       collector,
		NotificationStore:    stores.Notification,
		NotificationState:    stores.NotificationState,
		NewNotificationLease: stores.NewNotificationLease,
		IncidentStore:        stores.Incident,
		IncidentState:        stores.IncidentState,
		NewIncidentLease:     stores.NewIncidentLease,
	}

	openCodeHost := opencodehost.New(signalCtx, ctx.Config.OpenCode)
	cleanupCancel, cleanupDone := startLocalAgentSessionCleanup(signalCtx, ctx.Persistence, openCodeHost)
	defer func() {
		stop()
		shutdownCtx, shutdownCancel := localAgentSessionShutdownContext(ctx)
		defer shutdownCancel()
		if err := stopLocalAgentSessionCleanup(shutdownCtx, cleanupCancel, cleanupDone, openCodeHost); err != nil {
			logger.Error(ctx, "Failed to stop local agent session services", tag.Error(err))
		}
	}()

	// Initialize all services using the signal-aware context
	scheduler, err := newScheduler(serviceCtx, schedulerDeps)
	if err != nil {
		return fmt.Errorf("failed to initialize scheduler: %w", err)
	}
	// Disable health server when running from start-all
	scheduler.DisableHealthServer()
	scheduler.SetOpenCodeHost(openCodeHost)

	// Initialize resource monitoring service
	resourceService := resource.NewService(ctx.Config)

	// Use serviceCtx so auth initialization can respond to termination signals
	server, err := newServer(serviceCtx, resourceService, stores, frontend.WithAPIOption(apiv1.WithOpenCodeHost(openCodeHost)))
	if err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}

	// Initialize coordinator if enabled
	var coord *coordinator.Service
	if ctx.Config.Coordinator.Enabled {
		var err error
		coord, _, err = newCoordinator(serviceCtx, stores.Secret)
		if err != nil {
			return fmt.Errorf("failed to initialize coordinator: %w", err)
		}
		coord.DisableHealthServer()
	} else {
		logger.Info(serviceCtx, "Coordinator disabled via configuration")
	}

	// Persist monitor boundaries before any bundled service can emit DAG-run events.
	if err := scheduler.BootstrapMonitors(serviceCtx); err != nil {
		return fmt.Errorf("failed to bootstrap monitors: %w", err)
	}

	// Start resource monitoring service (starts its own goroutine internally)
	if err := resourceService.Start(serviceCtx); err != nil {
		return fmt.Errorf("failed to start resource service: %w", err)
	}

	// WaitGroup to track all services
	var wg sync.WaitGroup
	serviceCount := 2 // scheduler + server
	if coord != nil {
		serviceCount++
	}
	errCh := make(chan error, serviceCount)

	// Start scheduler
	wg.Go(func() {
		logger.Info(serviceCtx, "Scheduler initialization",
			tag.Dir(serviceCtx.Config.Paths.DAGsDir),
		)
		if err := scheduler.Start(serviceCtx.WithEventSource(eventstore.SourceServiceScheduler)); err != nil {
			select {
			case errCh <- fmt.Errorf("scheduler failed: %w", err):
			default:
			}
		}
	})

	if coord != nil {
		wg.Go(func() {
			if err := coord.Start(serviceCtx.WithEventSource(eventstore.SourceServiceCoordinator)); err != nil {
				select {
				case errCh <- fmt.Errorf("coordinator failed: %w", err):
				default:
				}
			}
		})
	}

	// Start server
	wg.Go(func() {
		// Give scheduler and coordinator a moment to start
		time.Sleep(100 * time.Millisecond)
		logger.Info(serviceCtx, "Server initialization",
			tag.Host(serviceCtx.Config.Server.Host),
			tag.Port(serviceCtx.Config.Server.Port),
		)
		if err := server.Serve(serviceCtx); err != nil {
			select {
			case errCh <- fmt.Errorf("server failed: %w", err):
			default:
			}
		}
	})

	// Wait for signal or error
	var firstErr error
	select {
	case <-signalCtx.Done():
		logger.Info(ctx, "Received shutdown signal", slog.Any("signal", signalCtx.Err()))
	case err := <-errCh:
		// If the context was already canceled (e.g. by a signal), services returning
		// context.Canceled is expected shutdown behavior, not a failure.
		if signalCtx.Err() != nil && errors.Is(err, context.Canceled) {
			logger.Info(ctx, "Received shutdown signal", slog.Any("signal", signalCtx.Err()))
		} else {
			firstErr = err
			logger.Error(ctx, "Service failed, shutting down", tag.Error(err))
		}
	}
	stop() // Restore default signal handling while graceful shutdown runs.

	// Stop all services gracefully
	logger.Info(ctx, "Stopping all services")

	// Stop coordinator first to unregister from service registry
	if coord != nil {
		if err := coord.Stop(ctx); err != nil {
			logger.Error(ctx, "Failed to stop coordinator",
				tag.Error(err),
			)
		}
	}

	// Stop scheduler to release lock, flush planner, clean up
	scheduler.Stop(ctx)

	// Stop resource service
	if err := resourceService.Stop(ctx); err != nil {
		logger.Error(ctx, "Failed to stop resource service", tag.Error(err))
	}

	// Wait for all services to finish with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info(ctx, "All services stopped gracefully")
	case <-time.After(30 * time.Second):
		logger.Error(ctx, "Timeout waiting for services to stop")
	}

	return firstErr
}
