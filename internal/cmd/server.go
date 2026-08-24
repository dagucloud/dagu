// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	frontendfile "github.com/dagucloud/dagu/v2/internal/service/frontend/file"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/tunnel"
	"github.com/spf13/cobra"
)

func Server(serverOpts ...frontend.ServerOption) *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "server [flags]",
			Short: "Start the web UI server for DAG management",
			Long: `Launch the Dagu web server that provides a graphical interface for monitoring and managing DAGs.

The web UI allows you to:
- View and manage DAG definitions
- Monitor active and historical DAG-runs
- Inspect DAG-run details including step status and logs
- Start, stop, and retry DAG-runs 
- View execution history and statistics

Flags:
  --host string    Host address to bind the server to (default: 127.0.0.1)
  --port int       Port number to listen on (default: 8080)
  --dags string    Path to the directory containing DAG definition files

Example:
  dagu server --host=0.0.0.0 --port=8080 --dags=/path/to/dags
`,
		}, serverFlags, func(ctx *Context, args []string) error {
			return runServer(ctx, args, serverOpts...)
		},
	)
}

var serverFlags = []commandLineFlag{dagsFlag, hostFlag, portFlag, tunnelFlag, tunnelTokenFlag, tunnelFunnelFlag, tunnelHTTPSFlag}

const localAgentSessionShutdownTimeout = 10 * time.Second

func newServer(ctx *Context, rs *resource.Service, stores frontend.Stores, opts ...frontend.ServerOption) (*frontend.Server, error) {
	coordinatorClient, err := ctx.NewCoordinatorClient()
	if err != nil {
		return nil, err
	}
	return frontend.NewServer(frontend.ServerConfig{
		Context:              ctx.Context,
		Config:               ctx.Config,
		DAGRepository:        ctx.Persistence.DAGRepository,
		DAGRunRepository:     ctx.Persistence.DAGRunRepository,
		ProcRepository:       ctx.Persistence.ProcRepository,
		QueueStore:           ctx.Persistence.QueueStore,
		DAGRunManager:        ctx.DAGRunMgr,
		CoordinatorClient:    coordinatorClient,
		ServiceRegistry:      ctx.Persistence.ServiceRegistry,
		DAGRunLeaseStore:     ctx.Persistence.DAGRunLeaseStore,
		WorkerHeartbeatStore: ctx.Persistence.WorkerHeartbeatStore,
		SchedulerStateStore:  ctx.Persistence.SchedulerStateStore,
		Caches:               ctx.Caches,
		LicenseManager:       ctx.LicenseManager,
		ResourceService:      rs,
		Stores:               stores,
	}, opts...)
}

// runServer initializes and runs the web UI server and its resource monitoring service.
// It logs startup info, starts the resource service (deferring its shutdown and logging any stop errors),
// constructs the server with that resource service, and then begins serving.
// It returns an error if the resource service fails to start, the server fails to initialize, or serving fails.
func runServer(ctx *Context, _ []string, serverOpts ...frontend.ServerOption) error {
	// Create a context that will be cancelled on interrupt signal.
	// This must be created BEFORE server initialization so auth provider init can be cancelled.
	signalCtx, stop := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Create a signal-aware context for services
	serviceCtx := ctx.WithContext(signalCtx)
	openCodeHost := opencodehost.New(signalCtx, ctx.Config.OpenCode)
	cleanupCancel, cleanupDone := startLocalAgentSessionCleanup(signalCtx, ctx.Persistence, openCodeHost)
	var tunnelService *tunnel.Service
	var resourceService *resource.Service
	defer func() {
		stop()
		if resourceService != nil {
			if err := resourceService.Stop(ctx); err != nil {
				logger.Error(ctx, "Failed to stop resource service", tag.Error(err))
			}
		}
		if tunnelService != nil {
			if err := tunnelService.Stop(ctx); err != nil {
				logger.Error(ctx, "Failed to stop tunnel service", tag.Error(err))
			}
		}
		if ctx.LicenseManager != nil {
			ctx.LicenseManager.Stop()
		}
		shutdownCtx, shutdownCancel := localAgentSessionShutdownContext(ctx)
		defer shutdownCancel()
		if err := stopLocalAgentSessionCleanup(shutdownCtx, cleanupCancel, cleanupDone, openCodeHost); err != nil {
			logger.Error(ctx, "Failed to stop local agent session services", tag.Error(err))
		}
	}()
	serverOpts = append(serverOpts, frontend.WithAPIOption(apiv1.WithOpenCodeHost(openCodeHost)))

	stores, err := frontendfile.NewStores(serviceCtx, serviceCtx.Config, serviceCtx.backend)
	if err != nil {
		return err
	}
	serviceCtx = serviceCtx.withEvent(stores.Event)

	logger.Info(serviceCtx, "Server initialization",
		tag.Host(serviceCtx.Config.Server.Host),
		tag.Port(serviceCtx.Config.Server.Port),
	)

	// Initialize tunnel service if enabled
	tunnelService, err = initTunnelService(ctx.Config)
	if err != nil {
		return fmt.Errorf("failed to initialize tunnel: %w", err)
	}

	// Initialize resource monitoring service, but don't start it yet.
	// Resource monitoring must start AFTER server init to avoid race condition
	// with auth provider initialization (gopsutil conflicts with net/http dial).
	resourceService = resource.NewService(ctx.Config)

	serverOpts = append([]frontend.ServerOption(nil), serverOpts...)
	if tunnelService != nil {
		serverOpts = append(serverOpts, frontend.WithTunnelService(tunnelService))
	}

	// Initialize server (includes auth setup). Use serviceCtx so auth providers can
	// respond to termination signals during potentially slow network operations.
	server, err := newServer(serviceCtx, resourceService, stores, serverOpts...)
	if err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}

	// Start resource monitoring now that server initialization is complete.
	if err := resourceService.Start(serviceCtx); err != nil {
		return fmt.Errorf("failed to start resource service: %w", err)
	}

	// Start tunnel service after server is initialized
	if tunnelService != nil {
		localAddr := net.JoinHostPort(ctx.Config.Server.Host, strconv.Itoa(ctx.Config.Server.Port))
		if err := tunnelService.Start(serviceCtx, localAddr); err != nil {
			// Log warning but continue without tunnel - graceful degradation
			logger.Warn(serviceCtx, "Tunnel failed to start (server will continue without tunnel)",
				tag.Error(err),
			)
		} else {
			// Log tunnel URL prominently on success
			logTunnelStatus(serviceCtx, tunnelService)
		}
	}

	err = server.Serve(serviceCtx)
	stop() // Restore default signal handling while deferred cleanup runs.
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

func startLocalAgentSessionCleanup(ctx context.Context, persistence Persistence, host *opencodehost.Host) (context.CancelFunc, <-chan struct{}) {
	cleanupCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		agentsession.RunCleanupLoop(cleanupCtx, "local", persistence.AgentSessionCleanupQueue, persistence.DAGRunRepository,
			func(cleanupCtx context.Context, resource ir.AgentSessionResource) error {
				if resource.Provider != "opencode" {
					return fmt.Errorf("unsupported managed agent provider %q", resource.Provider)
				}
				hostConfig, err := host.Ensure()
				if err != nil {
					return err
				}
				return opencodehost.DeleteSession(cleanupCtx, hostConfig, resource.Directory, resource.SessionID)
			})
	}()
	return cancel, done
}

func stopLocalAgentSessionCleanup(
	ctx context.Context,
	cancel context.CancelFunc,
	done <-chan struct{},
	host *opencodehost.Host,
) error {
	cancel()
	var cleanupErr error
	select {
	case <-done:
	case <-ctx.Done():
		cleanupErr = fmt.Errorf("wait for agent session cleanup: %w", ctx.Err())
	}
	return errors.Join(cleanupErr, host.Close(ctx))
}

func localAgentSessionShutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), localAgentSessionShutdownTimeout)
}

// initTunnelService creates and returns a tunnel service based on configuration.
// Returns nil if tunneling is not enabled.
func initTunnelService(cfg *config.Config) (*tunnel.Service, error) {
	if !cfg.Tunnel.Enabled {
		return nil, nil
	}

	tc := cfg.Tunnel
	tunnelCfg := &tunnel.Config{
		Enabled:       tc.Enabled,
		AllowTerminal: tc.AllowTerminal,
		AllowedIPs:    tc.AllowedIPs,
		Tailscale: tunnel.TailscaleConfig{
			AuthKey:  tc.Tailscale.AuthKey,
			Hostname: tc.Tailscale.Hostname,
			Funnel:   tc.Tailscale.Funnel,
			HTTPS:    tc.Tailscale.HTTPS,
			StateDir: tc.Tailscale.StateDir,
		},
		RateLimiting: tunnel.RateLimitConfig{
			Enabled:              tc.RateLimiting.Enabled,
			LoginAttempts:        tc.RateLimiting.LoginAttempts,
			WindowSeconds:        tc.RateLimiting.WindowSeconds,
			BlockDurationSeconds: tc.RateLimiting.BlockDurationSeconds,
		},
	}

	return tunnel.NewService(tunnelCfg, cfg.Paths.DataDir)
}

// logTunnelStatus logs the tunnel status prominently to the console.
func logTunnelStatus(ctx *Context, svc *tunnel.Service) {
	info := svc.Info()

	accessType := "Private (tailnet only)"
	if info.IsPublic {
		accessType = "Public"
	}

	var authStatus string
	switch ctx.Config.Server.Auth.Mode {
	case config.AuthModeBuiltin:
		authStatus = "Builtin (enabled)"
	case config.AuthModeBasic:
		authStatus = "Basic (enabled)"
	case config.AuthModeNone:
		authStatus = "Disabled"
	default:
		authStatus = fmt.Sprintf("Unknown (%s)", ctx.Config.Server.Auth.Mode)
	}

	terminalStatus := "Disabled"
	if ctx.Config.Tunnel.AllowTerminal && ctx.Config.Server.Terminal.Enabled {
		terminalStatus = "Enabled"
	}

	// Print a prominent banner for tunnel status
	fmt.Printf("\n"+
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
		"  TUNNEL ACTIVE - Server is %s accessible\n"+
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
		"  Provider: %s (%s)\n"+
		"  URL:      %s\n"+
		"  Auth:     %s\n"+
		"  Terminal: %s\n"+
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n",
		accessType,
		info.Provider, info.Mode,
		info.PublicURL,
		authStatus,
		terminalStatus,
	)
}
