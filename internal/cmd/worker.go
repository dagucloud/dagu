// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	"github.com/spf13/cobra"
)

func CmdWorker() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "worker [flags]",
			Short: "Start a worker that polls the coordinator for tasks",
			Long: `Launch a worker process that connects to the coordinator and polls for tasks.

The worker creates multiple concurrent pollers (goroutines) that continuously
poll the coordinator for tasks to execute. Each poller generates a unique
poller_id for every poll request.

By default, the worker ID is set to hostname@PID, but can be overridden.

The option --agent <provider>=<path> makes an agent worker of a CLI provider
(Spec 092). An agent worker has one poller and takes one run. It announces the
label agent.provider.<provider> and takes only the runs that need its provider.
After the final status of the run, it exits with the code 0. It needs mutual TLS
to the coordinator, and it refuses an explicit worker.max_active_runs.

Flags:
  --agent string                           Agent worker: <provider>=<absolute path of its binary>
  --worker.id string                       Worker instance ID (default: hostname@PID)
  --worker.max-active-runs int             Maximum number of active runs (default: 100)
  --worker.health-port int                 Port number for the HTTP health check server (default: 8092, 0 disables)
  --worker.labels -l string                Worker labels for capability matching (format: key1=value1,key2=value2)
  --worker.coordinators string             Coordinator addresses (format: host1:port1,host2:port2)
  --worker.shutdown-timeout string         How long to drain running DAG runs on SIGINT/SIGTERM before
                                           canceling them (default: 60s, 0 waits without a bound)

TLS Configuration (uses global peer settings):
  --peer.insecure                          Use insecure connection (h2c) instead of TLS (default: true)
  --peer.cert-file string                  Path to TLS certificate file for mutual TLS
  --peer.key-file string                   Path to TLS key file for mutual TLS
  --peer.client-ca-file string             Path to CA certificate file for server verification
  --peer.skip-tls-verify                   Skip TLS certificate verification (insecure)

Example:
  dagu worker --worker.coordinators=coordinator-1:50055
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.max-active-runs=50
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.id=worker-1 --worker.max-active-runs=200
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.health-port=0

  # Worker with labels for capability matching:
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.labels gpu=true,memory=64G,region=us-east-1
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.labels cpu-arch=amd64,instance-type=m5.xlarge

  # For TLS connections (when coordinator has TLS enabled):
  dagu worker --worker.coordinators=coordinator-1:50055 --peer.insecure=false --peer.cert-file=client.crt --peer.key-file=client.key
  dagu worker --worker.coordinators=coordinator-1:50055 --peer.insecure=false --peer.client-ca-file=ca.crt
  dagu worker --worker.coordinators=coordinator-1:50055 --peer.insecure=false --peer.skip-tls-verify  # For self-signed certificates

This process runs continuously in the foreground until terminated. On SIGINT or
SIGTERM it stops polling and lets running DAG runs finish their cleanup and
lifecycle handlers. Once --worker.shutdown-timeout has passed, it cancels the
runs that are still going. Set the supervisor's stop timeout at least 15s above
this value (systemd TimeoutStopSec defaults to 90s, docker stop to 10s,
Kubernetes terminationGracePeriodSeconds to 30s), or the process is killed
before the canceled runs report their final status.
`,
		}, workerFlags, runWorker,
	)
}

var workerFlags = []commandLineFlag{
	workerAgentFlag,
	workerIDFlag,
	workerMaxActiveRunsFlag,
	workerHealthPortFlag,
	workerLabelsFlag,
	workerCoordinatorsFlag,
	workerShutdownTimeoutFlag,
	// Peer configuration flags for TLS
	peerInsecureFlag,
	peerCertFileFlag,
	peerKeyFileFlag,
	peerClientCAFileFlag,
	peerSkipTLSVerifyFlag,
}

func runWorker(ctx *Context, _ []string) error {
	// The checks of an agent worker come before the worker connects to the
	// coordinator (Spec 092, "Start"). A failed check stops the worker with the
	// exit code 1.
	agent, err := agentWorkerFromFlags(ctx)
	if err != nil {
		return err
	}
	labels := ctx.Config.Worker.Labels
	if err := worker.CheckConfiguredLabels(labels); err != nil {
		return err
	}
	if agent != nil {
		// Checks 6 to 8 of Spec 092 "Start": the restricted user, the six
		// capabilities with the effective user ID 0, and no-new-privileges.
		if err := untrusted.CheckAgentWorker(ctx.Config.Untrusted); err != nil {
			return err
		}
		logger.Info(ctx, "Starting an agent worker", slog.String("provider", agent.provider), slog.String("binary", agent.path))
	}

	signalCtx, stop := notifyShutdownContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	workerID := ctx.Config.Worker.ID
	// Default to hostname@PID if not configured
	if workerID == "" {
		hostname, err := os.Hostname()
		if err != nil || hostname == "" {
			hostname = "unknown"
		}
		workerID = fmt.Sprintf("%s@%d", hostname, os.Getpid())
	}

	maxActiveRuns := ctx.Config.Worker.MaxActiveRuns
	if agent != nil {
		maxActiveRuns = 1
	}

	coordinatorCli, err := createCoordinatorClient(ctx)
	if err != nil {
		return err
	}

	w := worker.NewWorker(
		workerID,
		maxActiveRuns,
		coordinatorCli,
		labels,
		ctx.Config,
	)
	if agent != nil {
		if err := w.EnableAgentMode(agent.provider); err != nil {
			return fmt.Errorf("--agent: %w", err)
		}
	}

	stores := ctx.runtimeStores()
	handlerCfg := worker.RemoteTaskHandlerConfig{
		WorkerID:          workerID,
		CoordinatorClient: coordinatorCli,
		PeerConfig:        ctx.Config.Core.Peer,
		Config:            ctx.Config,
		SecretStore:       stores.SecretStore,
		ProfileStore:      stores.ProfileStore,
	}
	if agent != nil {
		handlerCfg.AgentProvider = agent.provider
		handlerCfg.AgentBinary = agent.path
	}
	w.SetHandler(worker.NewRemoteTaskHandler(handlerCfg))
	logger.Info(ctx, "Using remote task handler")

	// Heartbeats carry the machine's figures, which the server reports to
	// Dagu Cloud on paid licenses.
	machine := resource.NewService(ctx.Config)
	if err := machine.Start(ctx); err != nil {
		return fmt.Errorf("failed to start resource monitoring: %w", err)
	}
	defer func() { _ = machine.Stop(ctx.Context) }()
	w.SetMachine(machine)

	logger.Info(ctx, "Starting worker", tag.WorkerID(workerID), tag.MaxConcurrency(maxActiveRuns), slog.Any("labels", labels))
	// Run status lives with the coordinator, so the worker closes waiting
	// browsers by deadline and owner liveness only.
	startBrowserReaper(ctx, ctx.Config.Paths.DataDir, nil)

	// Start the worker in a goroutine to allow for graceful shutdown. It gets
	// the parent context because canceling it would cancel running tasks
	// instead of letting Stop drain them.
	errCh := make(chan error, 1)
	go func() {
		if err := w.Start(ctx); err != nil {
			errCh <- err
		}
	}()

	// Wait for either context cancellation, the end of the run of an agent
	// worker, or an error. Done is nil for a general worker.
	select {
	case <-signalCtx.Done():
		// Let a second SIGINT end shutdown; SIGTERM stays absorbed.
		stop()
		shutdownTimeout := ctx.Config.Worker.ShutdownTimeout
		logger.Info(ctx, "Worker shutting down", slog.Duration("timeout", shutdownTimeout))
		stopCtx, cancel := workerStopContext(ctx.Context, shutdownTimeout)
		defer cancel()
		if err := w.Stop(stopCtx); err != nil {
			return fmt.Errorf("failed to stop worker: %w", err)
		}
	case <-w.Done():
		// Spec 092, "End": the agent worker exits after its one run.
		logger.Info(ctx, "Agent worker stopping after its run")
		stopCtx, cancel := workerStopContext(ctx.Context, ctx.Config.Worker.ShutdownTimeout)
		defer cancel()
		if err := w.Stop(stopCtx); err != nil {
			return fmt.Errorf("failed to stop worker: %w", err)
		}
	case err := <-errCh:
		return fmt.Errorf("worker failed: %w", err)
	}

	// The status of the run does not change the exit code. A failed final
	// report gives the exit code 1.
	if err := w.AgentRunErr(); err != nil {
		return fmt.Errorf("agent worker: %w", err)
	}
	return nil
}

// workerStopContext returns the context for Worker.Stop. A positive timeout
// bounds the drain of running DAG runs; zero leaves it unbounded.
func workerStopContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

// createCoordinatorClient creates the worker coordinator client.
func createCoordinatorClient(ctx *Context) (coordinator.Client, error) {
	return worker.NewCoordinatorClient(ctx.Context, ctx.Config)
}
