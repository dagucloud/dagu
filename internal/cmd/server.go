// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/cloudaccess"
	"github.com/dagucloud/dagu/v2/internal/cloudrelay"
	"github.com/dagucloud/dagu/v2/internal/cloudreport"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
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

// webPortInUseHint explains a web UI listen failure caused by a taken port.
const webPortInUseHint = "another Dagu may be running; stop it, or choose a free port with --port or DAGU_PORT"

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
		ArtifactRepository:   ctx.Persistence.ArtifactRepository,
		ProcRepository:       ctx.Persistence.ProcRepository,
		QueueStore:           ctx.Persistence.QueueStore,
		DAGRunManager:        ctx.DAGRunMgr,
		CoordinatorClient:    coordinatorClient,
		ServiceRegistry:      ctx.Persistence.ServiceRegistry,
		DAGRunLeaseStore:     ctx.Persistence.DAGRunLeaseStore,
		WorkerHeartbeatStore: ctx.Persistence.WorkerHeartbeatStore,
		SchedulerStateStore:  ctx.Persistence.SchedulerStateStore,
		SchedulerPauseStore:  ctx.Persistence.SchedulerPauseStore,
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
	signalCtx, stop := notifyShutdownContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Create a signal-aware context for services
	serviceCtx := ctx.WithContext(ctx.withSignalPropagation(signalCtx))
	openCodeHost := opencodehost.New(signalCtx, ctx.Config.OpenCode)
	cleanupCancel, cleanupDone := startLocalAgentSessionCleanup(signalCtx, ctx.Persistence, openCodeHost)
	startBrowserReaper(signalCtx, ctx.Config.Paths.DataDir, ctx.Persistence.DAGRunRepository)
	var tunnelService *tunnel.Service
	var resourceService *resource.Service
	var cloudReporter *cloudreport.Reporter
	stopCloudAccess := func() {}
	defer func() {
		stop()
		stopCloudAccess()
		if cloudReporter != nil {
			cloudReporter.Stop()
		}
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
	cloudMonitoring := newCloudReportMonitoring(serviceCtx)
	if cloudMonitoring != nil {
		serverOpts = append(serverOpts, frontend.WithAPIOption(apiv1.WithCloudReport(cloudMonitoring)))
	}
	cloudAccess := newCloudAccess(serviceCtx)
	if cloudAccess != nil {
		serverOpts = append(serverOpts, frontend.WithAPIOption(apiv1.WithCloudAccess(cloudAccess)))
	}
	serverOpts = append(serverOpts, frontend.WithOnReady(printStartupBanner(serviceCtx, stores.AuthSetupRequired)))

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

	cloudReporter = startCloudReport(serviceCtx, cloudMonitoring, cloudAccess)
	stopCloudAccess = startCloudAccess(serviceCtx, cloudAccess, server)

	err = server.Serve(serviceCtx)
	stop() // Let a second SIGINT end deferred cleanup; SIGTERM stays absorbed.
	if err != nil {
		return fmt.Errorf("failed to start server: %w", withPortInUseHint(err, webPortInUseHint))
	}

	return nil
}

// withPortInUseHint appends hint to err when err reports an address already
// in use, and returns other errors unchanged.
func withPortInUseHint(err error, hint string) error {
	if !isAddrInUse(err) {
		return err
	}
	return fmt.Errorf("%w (%s)", err, hint)
}

// isAddrInUse reports whether err comes from binding an address already in
// use. Windows reports WSAEADDRINUSE (10048), which syscall.EADDRINUSE does
// not match there.
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return goruntime.GOOS == "windows" && errors.Is(err, syscall.Errno(10048))
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

// newCloudReportMonitoring returns what the server reports to Dagu Console:
// the level cloud.report fixes, or else the level an administrator chooses.
// It returns nil when no license manager runs.
func newCloudReportMonitoring(ctx *Context) *cloudreport.Monitoring {
	if ctx.LicenseManager == nil {
		return nil
	}
	return cloudreport.NewMonitoring(ctx.Config.Cloud.Report, filemonitor.NewStateStore(cloudReportSettingsFile(ctx.Config)))
}

// cloudReportSettingsFile is where an administrator's choice of what the
// server reports to Dagu Console is kept.
func cloudReportSettingsFile(cfg *config.Config) string {
	return filepath.Join(cfg.Paths.DataDir, "cloud", "report-settings.json")
}

// startCloudReport reports to Dagu Console at the level monitoring sets while
// the server holds an online license, with how far access lets calls from
// Dagu Cloud go. When cloud.report is off, it only tells Dagu Console that the
// server no longer reports, if it reported before. It returns nil when
// monitoring is nil.
func startCloudReport(ctx *Context, monitoring *cloudreport.Monitoring, access *cloudaccess.Service) *cloudreport.Reporter {
	if monitoring == nil {
		return nil
	}
	var remote cloudreport.RemoteAccess
	if access != nil {
		remote = access
	}
	scheduler := cloudreport.Scheduler{State: ctx.Persistence.SchedulerStateStore, Pause: ctx.Persistence.SchedulerPauseStore}
	return cloudreport.Start(ctx, ctx.LicenseManager.CloudCredentials, ctx.Persistence.ServiceRegistry, cloudReportEvents(ctx), scheduler, monitoring, remote, ctx.LicenseManager.Refresh)
}

// newCloudAccess returns what takes calls from Dagu Cloud while remote access
// is on: as cloud.remote_access and cloud.remote_approvals fix it, or else as
// an administrator chooses. It returns nil when no license manager runs.
func newCloudAccess(ctx *Context) *cloudaccess.Service {
	if ctx.LicenseManager == nil {
		return nil
	}
	key, err := license.PublicKey()
	if err != nil {
		logger.Warn(ctx, "Remote access is unavailable: the license public key could not be loaded", tag.Error(err))
		return nil
	}
	settingsFile := filepath.Join(ctx.Config.Paths.DataDir, "cloud", "access-settings.json")
	name := ctx.Config.License.ServerName
	if name == "" {
		name, _ = os.Hostname()
	}
	return cloudaccess.New(cloudaccess.Config{
		Credentials: ctx.LicenseManager.CloudCredentials,
		Settings:    cloudaccess.NewSettings(ctx.Config.Cloud, filemonitor.NewStateStore(settingsFile)),
		Key:         key,
		Hello:       cloudrelay.Hello{Name: name, Version: config.Version},
	})
}

// startCloudAccess takes calls from Dagu Cloud through server's MCP tools
// until the returned function stops it. It does nothing when access is nil.
func startCloudAccess(ctx *Context, access *cloudaccess.Service, server *frontend.Server) (stop func()) {
	if access == nil {
		return func() {}
	}
	approvals := func(ctx context.Context) bool {
		status, err := access.Status(ctx)
		return err == nil && status.Approvals
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		access.Run(runCtx, server.CloudMCPHandler(approvals))
	}()
	return func() {
		cancel()
		<-done
	}
}

// cloudReportLeaseStaleThreshold outlasts the longest pause between the
// reporter's lease heartbeats: an event check followed by a report request.
const cloudReportLeaseStaleThreshold = time.Minute

// cloudReportEvents configures the DAG runs that reports to Dagu Console carry
// at the runs level: finished runs from the event store, and runs in progress
// followed through it. A reporter below runs uses them only to discard where
// they stopped being reported. It configures none while the event store is
// disabled.
func cloudReportEvents(ctx *Context) cloudreport.Events {
	if ctx.event == nil {
		return cloudreport.Events{}
	}
	stateFile := filepath.Join(ctx.Config.Paths.DataDir, "cloud", "report-state.json")
	return cloudreport.Events{
		Reader:        ctx.event,
		RetentionDays: ctx.Config.EventStore.RetentionDays,
		State:         filemonitor.NewStateStore(stateFile),
		Stale:         filemonitor.NewStateStore(stateFile + ".stale"),
		Lease:         filemonitor.NewLease(stateFile, &dirlock.LockOptions{StaleThreshold: cloudReportLeaseStaleThreshold}),
		Statuses:      ctx.Persistence.DAGRunRepository,
	}
}

// startupBanner tells the person who started Dagu where to open it.
type startupBanner struct {
	version       string
	url           string
	dagsDir       string
	setupRequired bool
	// license is nil when no license manager runs.
	license *license.Status
}

// printStartupBanner returns a frontend.WithOnReady hook that prints the
// startup banner to stdout. When stdout is not a terminal, as in a container,
// the hook logs the free monitoring offer instead. It returns nil in quiet
// mode or when the server has no web UI.
func printStartupBanner(ctx *Context, setupRequired bool) func(url string) {
	if ctx.Quiet || ctx.Config.Server.Headless {
		return nil
	}
	if !isTerminal(os.Stdout) {
		return func(url string) {
			if ctx.LicenseManager == nil {
				return
			}
			status := ctx.LicenseManager.Status()
			if link, ok := monitoringOffer(url, &status); ok {
				logger.Info(ctx, "Free monitoring: Dagu Console emails you when a workflow fails or this server stops", tag.URL(link))
			}
		}
	}
	return func(url string) {
		banner := startupBanner{
			version:       config.Version,
			url:           url,
			dagsDir:       ctx.Config.Paths.DAGsDir,
			setupRequired: setupRequired,
		}
		if ctx.LicenseManager != nil {
			status := ctx.LicenseManager.Status()
			banner.license = &status
		}
		banner.write(os.Stdout)
	}
}

// monitoringOffer returns the page that connects the server at url to Dagu
// Console for free monitoring, and whether to offer it: only on a Community
// server whose license is not managed by the environment. A nil status
// offers nothing.
func monitoringOffer(url string, status *license.Status) (string, bool) {
	if status == nil || !status.Community || status.Source.IsEnv() {
		return "", false
	}
	return url + "/license#monitoring", true
}

// write prints the banner in plain ASCII. It offers free monitoring only on
// Community servers whose license is not managed by the environment.
func (b startupBanner) write(w io.Writer) {
	var out strings.Builder
	fmt.Fprintf(&out, "\nDagu %s is ready\n", b.version)
	fmt.Fprintf(&out, "  Web UI:  %s\n", b.url)
	if b.setupRequired {
		fmt.Fprintf(&out, "  Setup:   create the admin account at %s/setup\n", b.url)
	}
	fmt.Fprintf(&out, "  DAGs:    %s\n", b.dagsDir)
	if link, ok := monitoringOffer(b.url, b.license); ok {
		fmt.Fprintf(&out, "  Alerts:  get an email if a workflow fails or this server stops, free: %s\n", link)
	}
	out.WriteString("\n")
	_, _ = io.WriteString(w, out.String())
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
