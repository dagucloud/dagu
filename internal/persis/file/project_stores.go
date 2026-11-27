// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/notification"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileartifact "github.com/dagucloud/dagu/v2/internal/persis/file/artifact"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
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
	monitorStateFileName    = "monitor-state.json"
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
	// library records the runs of the library DAGs that run in the
	// project, with their logs, artifacts, processes, queues, and DAG
	// state, apart from the project's DAGs of the same names. Its Paths are
	// the project's library paths; its other stores and its settings are
	// the project's. It is nil while projects are off, and in library
	// itself.
	library *ProjectStores
	// projectState and libraryState hold the project's DAG state and its
	// library's. libraryState is nil while projects are off.
	projectState dagrun.StateStore
	libraryState dagrun.StateStore
	// project is the project's stores in Library, and nil in the project's.
	project *ProjectStores
}

// ProjectStoreOptions configures NewProjectStores. The caches are keyed by
// file path, so the projects of one process can share them.
type ProjectStoreOptions struct {
	DAGCache            *fileutil.Cache[*ir.DAG]
	DAGRunStatusCache   *fileutil.Cache[*ir.DAGRunStatus]
	ArtifactRecordCache *fileutil.Cache[*fileartifact.Record]
}

// ProjectSettings opens the stores of one project's settings and the other
// project stores that need no run store. A store opens on each call, since
// opening one may create the server's encryption key or a directory.
type ProjectSettings struct {
	// Paths are the directories that the project's stores open on.
	Paths config.ProjectPaths

	cfg     *config.Config
	server  *ServerStores
	backend *Backend
	// v2Dirs reports that Paths are the directories v2 uses.
	v2Dirs bool
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
	if cfg.ProjectsEnabled {
		dags.project, dags.owners = p.Name, server.owners
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
	if !settings.v2Dirs {
		ephemeralDir = filepath.Join(paths.OtherStateDir, ephemeralRunsDirName)
	}
	schedulerState := backend.Collection(persis.CollectionSchedulerState)
	s := &ProjectStores{
		ProjectSettings:     settings,
		SchedulerStateStore: store.NewSchedulerStateStore(schedulerState),
		PeriodStore: store.NewPeriodStore(
			schedulerState,
			server.backend.Collection(persis.CollectionSchedulerProcess),
			server.pauses,
		),
		WatchStateStore:          store.NewWatchStateStore(backend.Collection(persis.CollectionWatchState)),
		AgentSessionCleanupQueue: agentsession.NewCleanupQueue(backend.Collection(persis.CollectionAgentSessionCleanups)),
		BackfillStore:            filebackfill.New(filebackfill.Dir(paths.OtherStateDir)),
		MaterializationStore:     filematerialization.New(filepath.Join(paths.OtherStateDir, materializationsDirName)),
		dags:                     dags,
	}
	s.openRuns(cfg, paths, backend, runPaths, ephemeralDir, opts)
	s.projectState = s.StateStore
	dagOpts := []DAGRepositoryOption{WithDAGFileCache(opts.DAGCache)}
	if !isDefault {
		dagOpts = append(dagOpts, WithDAGSkipExamples(true))
	}
	if s.DAGRepository, err = s.OpenDAGRepository(dagOpts...); err != nil {
		return nil, fmt.Errorf("open the DAG store of project %s: %w", p.Name, err)
	}
	if cfg.ProjectsEnabled {
		if err := s.openLibrary(cfg, p.Name, opts); err != nil {
			return nil, fmt.Errorf("open the library stores of project %s: %w", p.Name, err)
		}
	}
	logger.Debug(ctx, "Opened project stores", tag.Name(p.Name), tag.Dir(paths.StateDir))
	return s, nil
}

// openRuns opens on paths the stores that record runs: their records and
// logs, artifacts, processes, queues, and DAG state.
func (s *ProjectStores) openRuns(
	cfg *config.Config,
	paths config.ProjectPaths,
	backend *Backend,
	runPaths runpath.Mapper,
	ephemeralDir string,
	opts ProjectStoreOptions,
) {
	dagRunOpts := []DAGRunRepositoryOption{WithDAGRunRemovalEnqueuer(s.AgentSessionCleanupQueue)}
	if opts.DAGRunStatusCache != nil {
		dagRunOpts = append(dagRunOpts, WithDAGRunHistoryFileCache(opts.DAGRunStatusCache))
	}
	var artifactOpts []ArtifactRepositoryOption
	if opts.ArtifactRecordCache != nil {
		artifactOpts = append(artifactOpts, WithArtifactRecordCache(opts.ArtifactRecordCache))
	}
	s.DAGRunRepository = newDAGRunRepository(cfg, paths, ephemeralDir, runPaths, dagRunOpts...)
	s.ArtifactRepository = newArtifactRepository(paths.ArtifactDir, runPaths, artifactOpts...)
	s.ProcRepository = newProcRepository(cfg, paths.ProcDir)
	s.QueueStore = store.NewQueueStore(backend.Collection(persis.CollectionQueue))
	s.StateStore = store.NewDAGStateStore(backend.Collection(persis.CollectionDAGState))
}

// openLibrary opens s.library on the library paths of the project called
// name. A library DAG runs with the project's settings, and its run events
// are the project's.
func (s *ProjectStores) openLibrary(cfg *config.Config, name string, opts ProjectStoreOptions) error {
	paths, err := cfg.LibraryPaths(name)
	if err != nil {
		return err
	}
	paths.DAGsDir = s.Paths.DAGsDir
	paths.SettingsDir = s.Paths.SettingsDir
	paths.EventStoreDir = s.Paths.EventStoreDir

	library := *s
	library.ProjectSettings = &ProjectSettings{Paths: paths, cfg: cfg, server: s.server, backend: s.backend}
	library.openRuns(cfg, paths, newProjectBackend(paths), runPaths(cfg, paths, false),
		filepath.Join(paths.OtherStateDir, ephemeralRunsDirName), opts)
	library.libraryState = library.StateStore
	library.StateStore = scopedStateStore{dag: library.libraryState, rootDAG: library.libraryState, other: s.projectState}
	library.project = s
	s.libraryState = library.libraryState
	s.library = &library
	return nil
}

// RunStores returns the stores that record a run of the DAG file at path
// that starts in the project: Library for a library DAG, else the
// project's. It returns s while projects are off.
func (s *ProjectStores) RunStores(ctx context.Context, path string) (*ProjectStores, error) {
	project := s
	if s.project != nil {
		project = s.project
	}
	if project.library == nil {
		return s, nil
	}
	inLibrary, err := s.isLibraryDAG(ctx, path)
	switch {
	case err != nil:
		return nil, err
	case inLibrary:
		return project.library, nil
	default:
		return project, nil
	}
}

// LibraryRuns returns the run and process stores of the library DAGs that
// run in the project. Both are nil while projects are off.
func (s *ProjectStores) LibraryRuns() (*persis.DAGRunRepository, *persis.ProcRepository) {
	if s.library == nil {
		return nil, nil
	}
	return s.library.DAGRunRepository, s.library.ProcRepository
}

// InLibrary reports whether s records the runs of a project's library DAGs.
func (s *ProjectStores) InLibrary() bool {
	return s.project != nil
}

// StateStoreFor returns the DAG state store that a step of the DAG file at
// path uses in a run whose root s records. State that a DAG's name
// namespaces, scope dag by the step's DAG and scope root_dag by the root's,
// is the library's when that DAG is a library DAG. Any other state is the
// project's. It returns StateStore while projects are off.
func (s *ProjectStores) StateStoreFor(ctx context.Context, path string) (dagrun.StateStore, error) {
	if s.libraryState == nil {
		return s.StateStore, nil
	}
	inLibrary, err := s.isLibraryDAG(ctx, path)
	if err != nil {
		return nil, err
	}
	dag, rootDAG := s.projectState, s.projectState
	if inLibrary {
		dag = s.libraryState
	}
	if s.InLibrary() {
		rootDAG = s.libraryState
	}
	if !inLibrary && !s.InLibrary() {
		return s.projectState, nil
	}
	return scopedStateStore{dag: dag, rootDAG: rootDAG, other: s.projectState}, nil
}

// isLibraryDAG reports whether the DAG file at path is in the library.
func (s *ProjectStores) isLibraryDAG(ctx context.Context, path string) (bool, error) {
	if path == "" || s.dags.owners == nil {
		return false, nil
	}
	owner, err := s.dags.owners.Of(ctx, path)
	if err != nil {
		return false, fmt.Errorf("find the owner of DAG %s: %w", path, err)
	}
	return owner.Library, nil
}

// BaseConfig returns the project's base config file and the directory of
// the base configs of its v2 workspaces. Each is empty when none applies.
func (s *ProjectStores) BaseConfig() (file, workspaceDir string) {
	return s.dags.baseConfig, s.dags.workspaceBaseConfigDir
}

// OpenDAGRepository opens another repository of the project's DAG files,
// which also finds DAGs in the library. Options apply as they do to
// NewDAGRepository.
func (s *ProjectStores) OpenDAGRepository(opts ...DAGRepositoryOption) (*persis.DAGRepository, error) {
	return newDAGRepository(s.cfg, s.dags, opts...)
}

// CheckStart refuses an explicit start of the library DAG file at path in
// the project when the project has a DAG of the same name, since the name
// there addresses the project's own DAG. Any other file passes. It passes
// everything while projects are off.
func (s *ProjectStores) CheckStart(ctx context.Context, path string) error {
	if s.dags.owners == nil {
		return nil
	}
	owner, err := s.dags.owners.Of(ctx, path)
	if err != nil || !owner.Library {
		return err
	}
	name := fileutil.TrimYAMLFileExtension(filepath.Base(path))
	own, err := s.DAGRepository.GetMetadata(ctx, name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("library DAG %s cannot start in project %q: read its DAG named %s in %s: %w",
			path, s.dags.project, name, s.Paths.DAGsDir, err)
	}
	shadow, err := s.dags.owners.Of(ctx, own.Location)
	if err != nil || shadow.Library {
		return err
	}
	return fmt.Errorf("library DAG %s cannot start in project %q, which has a DAG named %s", path, s.dags.project, name)
}

// NewProjectSettings returns the settings of the project called name, for a
// process that opens no run store of the project. It does not access the
// filesystem. The default project keeps the directories v2 uses, also while
// projects are on, until migration moves its state.
func NewProjectSettings(cfg *config.Config, server *ServerStores, name string) (*ProjectSettings, error) {
	paths, v2Dirs, err := projectPaths(cfg, name)
	if err != nil {
		return nil, err
	}
	return &ProjectSettings{
		Paths:   paths,
		cfg:     cfg,
		server:  server,
		backend: newProjectBackend(paths),
		v2Dirs:  v2Dirs,
	}, nil
}

// defaultProjectInV2Dirs keeps the default project's stores in the
// directories v2 uses while projects are on. Runs still write the default
// project's logs and artifacts there, and its state stays there until
// migration moves it, so turning this off goes with that move.
const defaultProjectInV2Dirs = true

// ProjectStorePaths returns the directories that the stores of the project
// called name open on. A process that watches a project's state watches
// these directories.
func ProjectStorePaths(cfg *config.Config, name string) (config.ProjectPaths, error) {
	paths, _, err := projectPaths(cfg, name)
	return paths, err
}

// ProjectSchedulerStateDir returns the directory of the project's scheduler
// state, or "" when the project has no state directory.
func ProjectSchedulerStateDir(p config.ProjectPaths) string {
	if p.OtherStateDir == "" {
		return ""
	}
	return filepath.Join(p.OtherStateDir, schedulerStateDirName)
}

// projectPaths returns the directories that the stores of the project
// called name open on, and whether they are the ones v2 uses. Every store
// of a project, in every process, opens on these directories.
func projectPaths(cfg *config.Config, name string) (config.ProjectPaths, bool, error) {
	if defaultProjectInV2Dirs && strings.EqualFold(name, project.DefaultName) {
		return v2ProjectPaths(cfg.Paths), true, nil
	}
	paths, err := cfg.ProjectPaths(name)
	return paths, !cfg.ProjectsEnabled, err
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

// CachedInventoryStore opens the project's node inventories for a
// long-running process, which keeps them in memory.
func (s *ProjectSettings) CachedInventoryStore() (*store.InventoryStore, error) {
	hostKeys, err := s.HostKeyStore()
	if err != nil {
		return nil, err
	}
	return store.NewCachedInventoryStore(
		s.backend.Collection(persis.CollectionInventories),
		s.backend.Lock(persis.CollectionInventories),
		hostKeys,
	)
}

// DAGSettingsStore opens the settings of the project's DAGs, creating their
// directory.
func (s *ProjectSettings) DAGSettingsStore() (dagsettings.Store, error) {
	return NewDAGSettingsStore(s.cfg, s.backend.Collection(persis.CollectionDAGSettings))
}

// DAGPinStore opens the project's pinned DAGs.
func (s *ProjectSettings) DAGPinStore() (*store.DAGPinStore, error) {
	return store.NewDAGPinStore(s.backend.Collection(persis.CollectionDAGPins))
}

// ViewStore opens the project's saved views.
func (s *ProjectSettings) ViewStore() *store.ViewStore {
	return store.NewViewStore(s.backend.Collection(persis.CollectionViews))
}

// NotificationStore opens the project's notification routes, which send to
// the server's channels, creating their directories.
func (s *ProjectSettings) NotificationStore() (notification.Store, error) {
	enc, err := s.server.Encryptor()
	if err != nil {
		return nil, err
	}
	return NewNotificationStore(
		s.backend.Collection(persis.CollectionNotifications),
		s.server.backend.Collection(persis.CollectionNotificationChannels),
		enc,
	)
}

// IncidentStore opens the project's incident policies and incidents, which
// name the server's providers, creating their directories.
func (s *ProjectSettings) IncidentStore() (incident.Store, error) {
	enc, err := s.server.Encryptor()
	if err != nil {
		return nil, err
	}
	return NewIncidentStore(
		s.backend.Collection(persis.CollectionIncidents),
		s.server.backend.Collection(persis.CollectionIncidentProviders),
		enc,
	)
}

// NotificationStateFile returns the file where the project's notification
// monitor records its progress through the project's run events.
func (s *ProjectSettings) NotificationStateFile() string {
	return filepath.Join(s.Paths.OtherStateDir, notificationsDirName, monitorStateFileName)
}

// IncidentStateFile returns the file where the project's incident monitor
// records its progress through the project's run events.
func (s *ProjectSettings) IncidentStateFile() string {
	return filepath.Join(s.Paths.OtherStateDir, incidentsDirName, monitorStateFileName)
}

// DAGChangeMarkerPath returns this host's file that signals changes to the
// project's DAG definitions to readers on other hosts, or "" when the
// project has no state directory to keep it in.
func (s *ProjectSettings) DAGChangeMarkerPath() string {
	return filedag.ChangeMarkerPath(dagIndexDir(s.Paths.OtherStateDir), s.Paths.DAGsDir)
}
