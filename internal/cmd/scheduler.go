// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/project"
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

var schedulerFlags = []commandLineFlag{dagsFlag, projectsDirFlag}

// newScheduler builds the scheduler of every project: the default one from
// the command's stores and deps, and, while projects are on, each other
// project that projects holds from its own stores.
func newScheduler(ctx *Context, deps scheduler.Dependencies, projects *persisfile.ProjectStoreCache) (*scheduler.Scheduler, error) {
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
		deps.EntryReader = newEntryReader(ctx, ctx.Config.Paths.DAGsDir, ctx.Persistence.DAGRepository,
			ctx.Config.Paths.BaseConfig, ctx.Config.WorkspaceBaseConfigDir(),
			ctx.settings.DAGChangeMarkerPath())
	}
	deps.DAGRepository = ctx.Persistence.DAGRepository
	deps.DAGRunRepository = ctx.Persistence.DAGRunRepository
	deps.QueueStore = ctx.Persistence.QueueStore
	deps.ProcRepository = ctx.Persistence.ProcRepository
	deps.ServiceRegistry = ctx.Persistence.ServiceRegistry
	deps.CoordinatorClient = coordinatorClient
	deps.SchedulerStateStore = ctx.Persistence.SchedulerStateStore
	deps.SchedulerPauseStore = ctx.Persistence.SchedulerPauseStore
	deps.RestartBackoffStore = ctx.Persistence.RestartBackoffStore
	deps.PeriodStore = ctx.Persistence.PeriodStore
	deps.BackfillStore = ctx.Persistence.BackfillStore
	deps.WatchStore = ctx.Persistence.WatchStateStore
	deps.DAGRunLeaseStore = ctx.Persistence.DAGRunLeaseStore
	deps.DispatchTaskStore = ctx.Persistence.DispatchTaskStore
	deps.WorkerHeartbeatStore = ctx.Persistence.WorkerHeartbeatStore
	deps.LicenseManager = ctx.LicenseManager
	deps.LibraryDAGRunRepository, deps.LibraryProcRepository = ctx.projectStores.LibraryRuns()
	addPublicWebhooks(ctx, &deps)
	if projects != nil {
		deps.Projects = projects
		deps.OpenProject = func(c context.Context, p project.Project, stores *persisfile.ProjectStores) (scheduler.Dependencies, error) {
			return unitDependencies(ctx.WithContext(c), p, stores)
		}
	}
	return scheduler.New(ctx.Config, deps)
}

// unitDependencies returns the dependencies of the scheduler unit of
// project p, from its stores and the stores every project shares. A unit
// holds no public webhook URL: the webhooks are the default project's. It
// gets no store of distributed runs either: a unit sends no run to a
// worker, and those stores do not record a run's project, so a run of the
// default project would count against the unit's queue of the same name.
func unitDependencies(ctx *Context, p project.Project, stores *persisfile.ProjectStores) (scheduler.Dependencies, error) {
	deps, err := schedulerfile.NewDependencies(ctx, ctx.Config, ctx.serverStores, stores.ProjectSettings,
		unitEventDedupeBytes(ctx))
	if err != nil {
		return scheduler.Dependencies{}, err
	}
	// Each unit cleans up its own coordinator client when it stops.
	coordinatorClient, err := ctx.NewCoordinatorClient()
	if err != nil {
		return scheduler.Dependencies{}, err
	}
	baseConfig, workspaceDir := stores.BaseConfig()
	server := ctx.serverStores
	deps.EntryReader = newEntryReader(ctx, stores.Paths.DAGsDir, stores.DAGRepository,
		baseConfig, workspaceDir, stores.DAGChangeMarkerPath())
	deps.DAGRunManager = runtime.NewManager(stores.DAGRunRepository, stores.ProcRepository, ctx.Config,
		append(projectManagerOptions(ctx.Config, p), runtime.WithLatestStatusAllHistory())...)
	deps.DAGRepository = stores.DAGRepository
	deps.DAGRunRepository = stores.DAGRunRepository
	deps.QueueStore = stores.QueueStore
	deps.ProcRepository = stores.ProcRepository
	deps.CoordinatorClient = coordinatorClient
	deps.SchedulerStateStore = stores.SchedulerStateStore
	deps.SchedulerPauseStore = server.SchedulerPauseStore
	deps.RestartBackoffStore = server.RestartBackoffStore.ForProject(p.ID)
	deps.PeriodStore = stores.PeriodStore
	deps.BackfillStore = stores.BackfillStore
	deps.WatchStore = stores.WatchStateStore
	deps.LicenseManager = ctx.LicenseManager
	return deps, nil
}

// unitEventDedupeShare is the part of a project's event dedupe budget that
// the event collector of each unit keeps: a unit's project holds a share of
// the server's events, and the size does not depend on how many projects
// there are or the order they arrive in.
const unitEventDedupeShare = 64

// unitEventDedupeBytes returns what a unit's event collector keeps for the
// IDs of the events it stored.
func unitEventDedupeBytes(ctx *Context) int {
	return ctx.Config.Cache.Limits().EventStoreBytes / unitEventDedupeShare
}

// newEntryReader returns the reader of the DAG files in dagsDir that the
// scheduler schedules.
func newEntryReader(ctx *Context, dagsDir string, dags *persis.DAGRepository, baseConfig, workspaceDir, changeMarker string) scheduler.EntryReader {
	return filedag.NewFileEntryReader(
		dagsDir, dags,
		ctx.Config.DAGDiscovery.Recursive, baseConfig, workspaceDir,
		filedag.WithChangeMarker(changeMarker, 0),
		filedag.WatchCalendars(ctx.Config.Paths.CalendarsDir),
		filedag.SkipDirectories(ctx.Config.DAGDiscovery.SkipDirs),
		filedag.SkipRoots(ctx.Config.DiscoverySkipRoots()),
	)
}

// addPublicWebhooks gives the scheduler what holding public webhook URLs
// needs. Webhooks exist only under builtin auth.
func addPublicWebhooks(ctx *Context, deps *scheduler.Dependencies) {
	if ctx.Config.Server.Auth.Mode != config.AuthModeBuiltin {
		return
	}
	webhooks, status, err := schedulerfile.NewPublicWebhookStores(ctx.serverStores, ctx.settings)
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
	activations := ctx.serverStores.LicenseStore(ctx)
	return license.StoredCloudCredentials(activations, pubKey, ctx.Config.License.CloudURL)
}

// runScheduler reads the "dags" flag (if present) to override the configured DAGs directory, initializes a scheduler, and starts it in the foreground.
//
// The context `ctx` supplies command flags, configuration, and creation of the scheduler. If scheduler initialization or startup fails, an error wrapping the underlying cause is returned.
func runScheduler(ctx *Context, _ []string) error {
	if dagsDir, _ := ctx.Command.Flags().GetString("dags"); dagsDir != "" {
		ctx.Config.Paths.DAGsDir = dagsDir
	}
	deps, err := schedulerfile.NewDependencies(ctx, ctx.Config, ctx.serverStores, ctx.settings, 0)
	if err != nil {
		return err
	}
	ctx = ctx.withEvent(deps.EventService)

	logger.Info(ctx, "Scheduler initialization",
		tag.Dir(ctx.Config.Paths.DAGsDir),
		slog.String("log-format", ctx.Config.Core.LogFormat),
	)
	warnTestNamedDAGs(ctx)

	ctx.Context = ctx.withRunRegistry(ctx.Context)
	schedulerCtx := ctx.WithEventSource(eventstore.SourceServiceScheduler)
	startBrowserReaper(schedulerCtx, ctx.Config.Paths.DataDir, ctx.Persistence.DAGRunRepository)
	projects := ctx.newProjectStoreCache()
	scheduler, err := newScheduler(schedulerCtx, deps, projects)
	if err != nil {
		return fmt.Errorf("failed to initialize scheduler: %w", err)
	}

	defer scheduler.Stop(schedulerCtx)

	followCtx, stopFollowing := context.WithCancel(schedulerCtx)
	defer stopFollowing()
	runProjectStores(followCtx, projects)

	if err := scheduler.Start(schedulerCtx); err != nil {
		return fmt.Errorf("failed to start scheduler in directory %s: %w",
			ctx.Config.Paths.DAGsDir, err)
	}

	return nil
}
