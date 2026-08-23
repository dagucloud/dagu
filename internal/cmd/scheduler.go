// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	schedulerfile "github.com/dagucloud/dagu/v2/internal/service/scheduler/file"
	"github.com/spf13/cobra"
)

func Scheduler() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "scheduler [flags]",
			Short: "Start the scheduler for automated DAG-run execution",
			Long: `Launch the scheduler process that monitors DAG definitions and automatically triggers DAG based on their defined schedules.

The scheduler continuously monitors the specified directory for DAG definitions,
evaluates their schedule expressions (cron format), and initiates DAG-run executions
when their scheduled time arrives. It also consumes DAG-runs from the queue and executes them.

Flags:
  --dags string   Path to the directory containing DAG definition files

Example:
  dagu scheduler --dags=/path/to/dags

This process runs continuously in the foreground until terminated.
`,
		}, schedulerFlags, runScheduler,
	)
}

var schedulerFlags = []commandLineFlag{dagsFlag}

func newScheduler(ctx *Context, deps scheduler.Dependencies) (*scheduler.Scheduler, error) {
	if deps.DAGSettingsStore == nil {
		return nil, errors.New("DAG settings store is not configured")
	}
	coordinatorClient, err := ctx.NewCoordinatorClient()
	if err != nil {
		return nil, err
	}
	deps.DAGRunManager = runtime.NewManager(
		ctx.Persistence.DAGRunRepository,
		ctx.Persistence.ProcRepository,
		ctx.Config,
		runtime.WithLatestStatusAllHistory(),
	)
	deps.DAGRepository = ctx.Persistence.DAGRepository
	deps.DAGRunRepository = ctx.Persistence.DAGRunRepository
	deps.QueueStore = ctx.Persistence.QueueStore
	deps.ProcRepository = ctx.Persistence.ProcRepository
	deps.ServiceRegistry = ctx.Persistence.ServiceRegistry
	deps.CoordinatorClient = coordinatorClient
	deps.SchedulerStateStore = ctx.Persistence.SchedulerStateStore
	deps.DAGRunLeaseStore = ctx.Persistence.DAGRunLeaseStore
	deps.DispatchTaskStore = ctx.Persistence.DispatchTaskStore
	deps.WorkerHeartbeatStore = ctx.Persistence.WorkerHeartbeatStore
	deps.LicenseManager = ctx.LicenseManager
	return scheduler.New(ctx.Config, deps)
}

// runScheduler reads the "dags" flag (if present) to override the configured DAGs directory, initializes a scheduler, and starts it in the foreground.
//
// The context `ctx` supplies command flags, configuration, and creation of the scheduler. If scheduler initialization or startup fails, an error wrapping the underlying cause is returned.
func runScheduler(ctx *Context, _ []string) error {
	if dagsDir, _ := ctx.Command.Flags().GetString("dags"); dagsDir != "" {
		ctx.Config.Paths.DAGsDir = dagsDir
	}
	deps, err := schedulerfile.NewDependencies(ctx, ctx.Config, ctx.backend)
	if err != nil {
		return err
	}
	ctx = ctx.withEvent(deps.EventService)

	logger.Info(ctx, "Scheduler initialization",
		tag.Dir(ctx.Config.Paths.DAGsDir),
		slog.String("log-format", ctx.Config.Core.LogFormat),
	)

	schedulerCtx := ctx.WithEventSource(eventstore.SourceServiceScheduler)
	scheduler, err := newScheduler(schedulerCtx, deps)
	if err != nil {
		return fmt.Errorf("failed to initialize scheduler: %w", err)
	}

	defer scheduler.Stop(schedulerCtx)

	if err := scheduler.Start(schedulerCtx); err != nil {
		return fmt.Errorf("failed to start scheduler in directory %s: %w",
			ctx.Config.Paths.DAGsDir, err)
	}

	return nil
}
