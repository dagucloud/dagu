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

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	"github.com/spf13/cobra"
)

func CmdWorker() *cobra.Command {
	command := NewCommand(
		&cobra.Command{
			Use:   "worker [flags]",
			Short: "Start a worker that polls the coordinator for tasks",
			Long: `Launch a worker process that connects to the coordinator and polls for tasks.

The worker creates multiple concurrent pollers (goroutines) that continuously
poll the coordinator for tasks to execute. Each poller generates a unique
poller_id for every poll request.

By default, the worker ID is set to hostname@PID, but can be overridden.

Flags:
  --worker.id string                       Worker instance ID (default: hostname@PID)
  --worker.max-active-runs int             Maximum number of active runs (default: 100)
  --worker.health-port int                 Port number for the HTTP health check server (default: 8092, 0 disables)
  --worker.labels -l string                Worker labels for capability matching (format: key1=value1,key2=value2);
                                           keys and values are lowercased, and an invalid label stops the worker
  --worker.coordinators string             Coordinator addresses (format: host1:port1,host2:port2)
  --worker.invite string                   Invite that lets the worker join its coordinators over
                                           built-in TLS; dagu worker invite creates one
  --worker.shutdown-timeout string         How long to drain running DAG runs on SIGINT/SIGTERM before
                                           canceling them (default: 60s, 0 waits without a bound)
  --check                                  Check the connection to each coordinator and exit, without
                                           taking tasks

TLS Configuration (uses global peer settings):
  --peer.insecure                          Calls to a coordinator: true h2c; false TLS only; unset built-in TLS when its CA is known, else h2c
  --peer.cert-file string                  Path to TLS certificate file for mutual TLS
  --peer.key-file string                   Path to TLS key file for mutual TLS
  --peer.client-ca-file string             Path to CA certificate file for server verification
  --peer.skip-tls-verify                   Skip TLS certificate verification (insecure)
  --peer.ca-cert-file string               Path to the peer CA certificate

Example:
  dagu worker --worker.coordinators=coordinator-1:50055
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.max-active-runs=50
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.id=worker-1 --worker.max-active-runs=200
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.health-port=0

  # Check that the worker can reach and authenticate to its coordinators:
  dagu worker --worker.coordinators=coordinator-1:50055 --check

  # Worker with labels for capability matching:
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.labels gpu=true,memory=64G,region=us-east-1
  dagu worker --worker.coordinators=coordinator-1:50055 --worker.labels cpu-arch=amd64,instance-type=m5.xlarge

  # For TLS connections (when coordinator has TLS enabled):
  dagu worker --worker.coordinators=coordinator-1:50055 --peer.insecure=false --peer.cert-file=client.crt --peer.key-file=client.key
  dagu worker --worker.coordinators=coordinator-1:50055 --peer.insecure=false --peer.client-ca-file=ca.crt
  dagu worker --worker.coordinators=coordinator-1:50055 --peer.insecure=false --peer.skip-tls-verify  # For self-signed certificates

  # For built-in TLS, verified against the peer CA certificate, with peer.insecure unset:
  dagu worker --worker.coordinators=coordinator-1:50055 --peer.ca-cert-file=<ca.crt>

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
	command.AddCommand(workerInviteCommand())
	return command
}

var workerFlags = []commandLineFlag{
	workerIDFlag,
	workerMaxActiveRunsFlag,
	workerHealthPortFlag,
	workerLabelsFlag,
	workerCoordinatorsFlag,
	workerInviteFlag,
	workerShutdownTimeoutFlag,
	workerCheckFlag,
	// Peer configuration flags for TLS
	peerInsecureFlag,
	peerCertFileFlag,
	peerKeyFileFlag,
	peerClientCAFileFlag,
	peerSkipTLSVerifyFlag,
	peerCACertFileFlag,
}

func runWorker(ctx *Context, _ []string) error {
	if isWorkerCheck(ctx.Command) {
		return runWorkerCheck(ctx)
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
	} else if !inventory.IsNodeName(workerID) {
		logger.Warn(ctx, "worker.id is not a valid node name, so the worker is not a node", tag.WorkerID(workerID))
	}

	maxActiveRuns := ctx.Config.Worker.MaxActiveRuns
	labels := ctx.Config.Worker.Labels

	// Nothing the worker starts, such as a workflow's subprocess, needs
	// the invite, so it leaves the environment once loaded.
	config.ClearWorkerInviteEnv(ctx.Config)

	// The health check serves from before the worker joins, so a busy port
	// uses up no invite and a liveness probe sees the worker alive while
	// it waits for a coordinator to enroll it. It is not ready until the
	// worker it hands the server to reaches a coordinator.
	health := healthcheck.NewServer("worker", ctx.Config.Worker.HealthPort)
	health.SetReadyFunc(func() bool { return false })
	if err := health.Start(ctx); err != nil {
		return fmt.Errorf("failed to start worker health check server: %w", err)
	}
	defer func() { _ = health.Stop(ctx.Context) }()

	membership, err := worker.OpenMembership(signalCtx, ctx.Config, ctx.serverStores.ServiceRegistry)
	if err != nil {
		return err
	}
	defer func() { _ = membership.Close() }()
	logClientTransport(ctx, "worker", ctx.Config, membership.StartTransport)
	coordinatorCli, err := membership.Connect(signalCtx, workerID)
	if err != nil {
		return err
	}

	// Heartbeats carry the machine's figures, which the server reports to
	// Dagu Cloud on paid licenses.
	machine := resource.NewService(ctx.Config)
	if err := machine.Start(ctx); err != nil {
		return fmt.Errorf("failed to start resource monitoring: %w", err)
	}
	defer func() { _ = machine.Stop(ctx.Context) }()

	w := worker.NewWorker(
		workerID,
		maxActiveRuns,
		coordinatorCli,
		labels,
		ctx.Config,
	)

	handlerCfg := worker.RemoteTaskHandlerConfig{
		WorkerID:          workerID,
		CoordinatorClient: coordinatorCli,
		PeerConfig:        ctx.Config.Core.Peer,
		Config:            ctx.Config,
		SecretStore:       ctx.settings.SecretStore(ctx),
		ProfileStore:      ctx.settings.ProfileStore(ctx),
	}
	w.SetHandler(worker.NewRemoteTaskHandler(handlerCfg))
	logger.Info(ctx, "Using remote task handler")

	w.SetMachine(machine)
	w.SetHealthServer(health)

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

	// Wait for either context cancellation or an error
	select {
	case <-signalCtx.Done():
		// Let a second SIGINT end shutdown; SIGTERM stays absorbed.
		stop()
		shutdownTimeout := ctx.Config.Worker.ShutdownTimeout
		if shutdownTimeout > 0 {
			// Tasks may first run to their end for the drain timeout.
			shutdownTimeout += ctx.Config.SignalHandling.DrainTimeout
		}
		logger.Info(ctx, "Worker shutting down", slog.Duration("timeout", shutdownTimeout))
		stopCtx, cancel := workerStopContext(ctx.Context, shutdownTimeout)
		defer cancel()
		if err := w.Stop(stopCtx); err != nil {
			return fmt.Errorf("failed to stop worker: %w", err)
		}
	case err := <-errCh:
		return fmt.Errorf("worker failed: %w", err)
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
