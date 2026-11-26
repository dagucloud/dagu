// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	fileserviceregistry "github.com/dagucloud/dagu/v2/internal/persis/file/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// ServerStores holds the stores that every project shares, kept in
// paths.data_dir whether or not projects are on. A store that writes when
// it opens, such as one that needs the encryption key, opens on each call.
type ServerStores struct {
	ServiceRegistry serviceregistry.ServiceRegistry
	// ProjectReleases records in this process's service registry entries
	// the projects whose stores it has released.
	ProjectReleases           serviceregistry.ProjectReleaseRecorder
	DispatchTaskStore         dispatch.DispatchTaskStore
	WorkerHeartbeatStore      dispatch.WorkerHeartbeatStore
	DAGRunLeaseStore          dispatch.DAGRunLeaseStore
	ActiveDistributedRunStore dispatch.ActiveDistributedRunStore
	SchedulerPauseStore       schedulerstate.PauseStore
	RestartBackoffStore       schedulerstate.RestartBackoffStore
	// Calendars is the one source of business calendars for every
	// project's DAGs.
	Calendars *spec.CalendarSource

	cfg     *config.Config
	backend *Backend
	pauses  *store.SchedulerPauseStore
	owners  *filedag.Owners

	encryptorMu sync.Mutex
	encryptor   *crypto.Encryptor
}

// NewServerStores opens the stores every project shares. It does not
// access the filesystem.
func NewServerStores(cfg *config.Config) *ServerStores {
	backend := newServerBackend(cfg.Paths)
	registry := fileserviceregistry.New(cfg.Paths.ServiceRegistryDir)
	leases := store.NewDAGRunLeaseStore(backend.Collection(persis.CollectionDAGRunLeases))
	activeRuns := store.NewActiveDistributedRunStore(backend.Collection(persis.CollectionActiveDistributedRuns))
	bundles := workspacebundle.NewStore(
		workspacebundle.StoreDir(cfg.Paths.DataDir),
		workspacebundle.DefaultLimits(),
	)
	pauses := store.NewSchedulerPauseStore(backend.Collection(persis.CollectionSchedulerProcess))
	return &ServerStores{
		ServiceRegistry: registry,
		ProjectReleases: registry,
		DispatchTaskStore: store.NewDispatchTaskStore(
			backend.Collection(persis.CollectionDispatchTasks),
			store.WithDispatchAdmissionLiveness(leases, activeRuns),
			store.WithDispatchTransitionLock(bundles.WithLock),
		),
		WorkerHeartbeatStore:      store.NewWorkerHeartbeatStore(backend.Collection(persis.CollectionWorkerHeartbeats)),
		DAGRunLeaseStore:          leases,
		ActiveDistributedRunStore: activeRuns,
		SchedulerPauseStore:       pauses,
		RestartBackoffStore:       store.NewRestartBackoffStore(backend.Collection(persis.CollectionSchedulerProcess)),
		Calendars:                 spec.NewCalendarSource(cfg.Paths.CalendarsDir),
		cfg:                       cfg,
		backend:                   backend,
		pauses:                    pauses,
		owners: filedag.NewOwners(filedag.OwnerDirs{
			DAGsDir:     cfg.Paths.DAGsDir,
			ProjectsDir: cfg.Paths.ProjectsDir,
			Library:     cfg.Paths.AltDAGsDir,
		}, projectregistry.New(cfg.Paths.DataDir), nil),
	}
}

// DAGOwners tells which project or library a DAG file belongs to, by the
// paths and the project registry in effect.
func (s *ServerStores) DAGOwners() *filedag.Owners {
	return s.owners
}

// Encryptor returns the encryptor of the server's key, which every project
// encrypts its secrets with. It creates the key in paths.data_dir when
// neither DAGU_ENCRYPTION_KEY nor that file holds one.
func (s *ServerStores) Encryptor() (*crypto.Encryptor, error) {
	s.encryptorMu.Lock()
	defer s.encryptorMu.Unlock()
	if s.encryptor != nil {
		return s.encryptor, nil
	}
	key, err := crypto.ResolveKey(s.cfg.Paths.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve encryption key: %w", err)
	}
	enc, err := crypto.NewEncryptor(key)
	if err != nil {
		return nil, fmt.Errorf("create encryptor: %w", err)
	}
	s.encryptor = enc
	return enc, nil
}

// LicenseStore opens the store of license activations, creating its
// directory.
func (s *ServerStores) LicenseStore(ctx context.Context) license.ActivationStore {
	return NewLicenseStore(ctx, s.backend.Collection(persis.CollectionLicense))
}
