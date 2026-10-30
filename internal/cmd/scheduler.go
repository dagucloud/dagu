// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	schedulerfile "github.com/dagucloud/dagu/v2/internal/service/scheduler/file"
	"github.com/dagucloud/dagu/v2/internal/workspace"
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
	if deps.EntryReader == nil {
		deps.EntryReader = filedag.NewFileEntryReader(
			ctx.Config.Paths.DAGsDir, ctx.Persistence.DAGRepository,
			ctx.Config.DAGDiscovery.Recursive, ctx.Config.Paths.BaseConfig,
			workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir),
			filedag.WithChangeMarker(file.DAGChangeMarkerPath(ctx.Config), 0),
			filedag.WatchCalendars(ctx.Config.Paths.CalendarsDir),
		)
	}
	deps.DAGRepository = ctx.Persistence.DAGRepository
	deps.DAGRunRepository = ctx.Persistence.DAGRunRepository
	deps.QueueStore = ctx.Persistence.QueueStore
	deps.ProcRepository = ctx.Persistence.ProcRepository
	deps.ServiceRegistry = ctx.Persistence.ServiceRegistry
	deps.CoordinatorClient = coordinatorClient
	deps.SchedulerStateStore = ctx.Persistence.SchedulerStateStore
	deps.SchedulerPauseStore = ctx.Persistence.SchedulerPauseStore
	deps.WatchStore = ctx.Persistence.WatchStateStore
	deps.DAGRunLeaseStore = ctx.Persistence.DAGRunLeaseStore
	deps.DispatchTaskStore = ctx.Persistence.DispatchTaskStore
	deps.WorkerHeartbeatStore = ctx.Persistence.WorkerHeartbeatStore
	deps.LicenseManager = ctx.LicenseManager
	addPublicWebhooks(ctx, &deps)
	return scheduler.New(ctx.Config, deps)
}

// addPublicWebhooks gives the scheduler what holding public webhook URLs
// needs. Webhooks exist only under builtin auth.
func addPublicWebhooks(ctx *Context, deps *scheduler.Dependencies) {
	if ctx.Config.Server.Auth.Mode != config.AuthModeBuiltin {
		return
	}
	webhooks, status, err := schedulerfile.NewPublicWebhookStores(ctx.Config, ctx.backend)
	if err != nil {
		logger.Warn(ctx, "Public webhook URLs are disabled because the webhook store is unavailable", tag.Error(err))
		return
	}
	credentials := cloudCredentials(ctx)
	if credentials == nil {
		return
	}
	deps.Webhooks = webhooks
	deps.PublicWebhookStore = status
	deps.CloudCredentials = credentials
}

// cloudCredentials returns the server's Dagu Cloud credentials from the
// license manager, or from the stored license in a scheduler started on its
// own, which runs no license manager.
func cloudCredentials(ctx *Context) func() (license.CloudCredentials, bool) {
	if ctx.LicenseManager != nil {
		return ctx.LicenseManager.CloudCredentials
	}
	pubKey, err := license.PublicKey()
	if err != nil {
		logger.Warn(ctx, "Public webhook URLs are disabled because the license public key is unavailable", tag.Error(err))
		return nil
	}
	activations := file.NewLicenseStore(ctx, ctx.backend.Collection(persis.CollectionLicense))
	return license.StoredCloudCredentials(activations, pubKey, ctx.Config.License.CloudURL)
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

	ctx.Context = ctx.withSignalPropagation(ctx.Context)
	schedulerCtx := ctx.WithEventSource(eventstore.SourceServiceScheduler)
	startBrowserReaper(schedulerCtx, ctx.Config.Paths.DataDir, ctx.Persistence.DAGRunRepository)
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
