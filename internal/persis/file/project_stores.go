// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	materializationsDirName = "materializations"
	// ephemeralRunsDirName holds a project's ephemeral runs while projects
	// are on. Beside a runs directory set apart, as v2 keeps them, they
	// could take the name of another project's runs directory.
	ephemeralRunsDirName = "dag-runs-ephemeral"
)

// ProjectStores holds the stores of one project, each opened on the
// project's paths.
type ProjectStores struct {
	*ProjectSettings

	DAGRepository            *persis.DAGRepository
	DAGRunRepository         *persis.DAGRunRepository
	ArtifactRepository       *persis.ArtifactRepository
	ProcRepository           *persis.ProcRepository
	QueueStore               queue.QueueStore
	StateStore               dagrun.StateStore
	SchedulerStateStore      schedulerstate.Store
	PeriodStore              schedulerstate.PeriodStore
	WatchStateStore          schedulerstate.WatchStore
	AgentSessionCleanupQueue *agentsession.CleanupQueue
	BackfillStore            backfill.Store
	MaterializationStore     build.MaterializationStore

	dags dagSource
}

// ProjectStoreOptions configures NewProjectStores. The caches are keyed by
// file path, so the projects of one process can share them.
type ProjectStoreOptions struct {
	DAGCache            *fileutil.Cache[*ir.DAG]
	DAGRunStatusCache   *fileutil.Cache[*ir.DAGRunStatus]
	ArtifactRecordCache *fileutil.Cache[*fileartifact.Record]
}

// ProjectSettings opens the stores of one project's settings. A store opens
// on each call, since opening one may create the server's encryption key.
type ProjectSettings struct {
	// Paths are the project's directories.
	Paths config.ProjectPaths

	cfg     *config.Config
	server  *ServerStores
	backend *Backend
}

// NewProjectStores opens the stores of p on its paths. Only the default
// project gets example DAGs. The run records of the default project and of
// each migrated one map paths under the v2 log and artifact directories to
// the project's own. The default project keeps the directories v2 uses; see
// NewProjectSettings.
func NewProjectStores(
	ctx context.Context,
	cfg *config.Config,
	server *ServerStores,
	p project.Project,
	opts ProjectStoreOptions,
) (*ProjectStores, error) {
	settings, err := NewProjectSettings(cfg, server, p.Name)
	if err != nil {
		return nil, err
	}
	paths, backend := settings.Paths, settings.backend
	isDefault := strings.EqualFold(p.Name, project.DefaultName)

	dags := dagSource{
		paths:     paths,
		calendars: server.Calendars,
		records:   backend.Collection(persis.CollectionSchedulerState),
		library:   cfg.Paths.AltDAGsDir,
	}
	// Another project's base config comes in a later change; until then
	// none applies to it.
	if isDefault {
		dags.baseConfig = cfg.Paths.BaseConfig
		dags.workspaceBaseConfigDir = workspace.BaseConfigDir(paths.DAGsDir)
	}

	// A new project has no runs from v2.
	runPaths := runPaths(cfg, paths, isDefault || p.MigratedFrom != "")
	ephemeralDir := EphemeralDAGRunsDir(paths.DAGRunsDir)
	if cfg.ProjectsEnabled && !isDefault {
		ephemeralDir = filepath.Join(paths.OtherStateDir, ephemeralRunsDirName)
	}
	cleanups := agentsession.NewCleanupQueue(backend.Collection(persis.CollectionAgentSessionCleanups))
	dagRunOpts := []DAGRunRepositoryOption{WithDAGRunRemovalEnqueuer(cleanups)}
	if opts.DAGRunStatusCache != nil {
		dagRunOpts = append(dagRunOpts, WithDAGRunHistoryFileCache(opts.DAGRunStatusCache))
	}
	var artifactOpts []ArtifactRepositoryOption
	if opts.ArtifactRecordCache != nil {
		artifactOpts = append(artifactOpts, WithArtifactRecordCache(opts.ArtifactRecordCache))
	}
	schedulerState := backend.Collection(persis.CollectionSchedulerState)
	s := &ProjectStores{
		ProjectSettings:     settings,
		DAGRunRepository:    newDAGRunRepository(cfg, paths, ephemeralDir, runPaths, dagRunOpts...),
		ArtifactRepository:  newArtifactRepository(paths.ArtifactDir, runPaths, artifactOpts...),
		ProcRepository:      newProcRepository(cfg, paths.ProcDir),
		QueueStore:          store.NewQueueStore(backend.Collection(persis.CollectionQueue)),
		StateStore:          store.NewDAGStateStore(backend.Collection(persis.CollectionDAGState)),
		SchedulerStateStore: store.NewSchedulerStateStore(schedulerState),
		PeriodStore: store.NewPeriodStore(
			schedulerState,
			server.backend.Collection(persis.CollectionSchedulerProcess),
			server.pauses,
		),
		WatchStateStore:          store.NewWatchStateStore(backend.Collection(persis.CollectionWatchState)),
		AgentSessionCleanupQueue: cleanups,
		BackfillStore:            filebackfill.New(filebackfill.Dir(paths.OtherStateDir)),
		MaterializationStore:     filematerialization.New(filepath.Join(paths.OtherStateDir, materializationsDirName)),
		dags:                     dags,
	}
	dagOpts := []DAGRepositoryOption{WithDAGFileCache(opts.DAGCache)}
	if !isDefault {
		dagOpts = append(dagOpts, WithDAGSkipExamples(true))
	}
	if s.DAGRepository, err = s.OpenDAGRepository(dagOpts...); err != nil {
		return nil, fmt.Errorf("open the DAG store of project %s: %w", p.Name, err)
	}
	logger.Debug(ctx, "Opened project stores", tag.Name(p.Name), tag.Dir(paths.StateDir))
	return s, nil
}

// OpenDAGRepository opens another repository of the project's DAG files,
// which also finds DAGs in the library. Options apply as they do to
// NewDAGRepository.
func (s *ProjectStores) OpenDAGRepository(opts ...DAGRepositoryOption) (*persis.DAGRepository, error) {
	return newDAGRepository(s.cfg, s.dags, opts...)
}

// NewProjectSettings returns the settings of the project called name, for a
// process that opens no other store of the project. It does not access the
// filesystem. The default project keeps the directories v2 uses, also while
// projects are on.
func NewProjectSettings(cfg *config.Config, server *ServerStores, name string) (*ProjectSettings, error) {
	paths, err := cfg.ProjectPaths(name)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(name, project.DefaultName) {
		// The server and the scheduler still read and write the default
		// project's logs, settings, and state there, and runs write their
		// logs there; they move into the project together.
		paths = v2ProjectPaths(cfg.Paths)
	}
	return &ProjectSettings{Paths: paths, cfg: cfg, server: server, backend: newProjectBackend(paths)}, nil
}

// SecretStore opens the project's secrets, encrypted with the server's key.
// It returns nil, with a warning, when the store cannot open.
func (s *ProjectSettings) SecretStore(ctx context.Context) secret.Store {
	if s.cfg.Paths.DataDir == "" {
		return nil
	}
	enc, err := s.server.Encryptor()
	if err != nil {
		logger.Warn(ctx, "Failed to prepare encryption for secret store", tag.Error(err))
		return nil
	}
	secrets, err := store.NewSecretStore(s.backend.Collection(persis.CollectionSecrets), enc)
	if err != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(err))
		return nil
	}
	return secrets
}

// ProfileStore opens the project's runtime profiles. It returns nil, with a
// warning, when the store cannot open.
func (s *ProjectSettings) ProfileStore(ctx context.Context) profile.Store {
	return NewProfileStore(ctx, s.cfg, s.backend.Collection(persis.CollectionProfiles))
}

// HostKeyStore opens the host keys the project trusts for its nodes.
func (s *ProjectSettings) HostKeyStore() (*store.HostKeyStore, error) {
	return store.NewHostKeyStore(s.backend.Collection(persis.CollectionHostKeys))
}

// InventoryStore opens the project's node inventories.
func (s *ProjectSettings) InventoryStore() (inventory.Store, error) {
	hostKeys, err := s.HostKeyStore()
	if err != nil {
		return nil, err
	}
	return store.NewInventoryStore(
		s.backend.Collection(persis.CollectionInventories),
		s.backend.Lock(persis.CollectionInventories),
		hostKeys,
	)
}
