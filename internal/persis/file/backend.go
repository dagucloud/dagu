// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// Backend maps logical control-plane collections to the released file layout.
type Backend struct {
	dataDir string
	specs   map[string]collectionSpec
	cols    sync.Map
}

type collectionSpec struct {
	dir        string
	idPrefixes []string
	indented   bool
}

var _ persis.Backend = (*Backend)(nil)

const (
	schedulerStateDirName = "scheduler"
	incidentsDirName      = "incidents"
	notificationsDirName  = "notifications"
)

// SchedulerStateDir returns the file-backend directory for scheduler state:
// watermarks, checkpoints, catch-up slots, and DAG records.
func SchedulerStateDir(paths config.PathsConfig) string {
	return filepath.Join(paths.DataDir, schedulerStateDirName)
}

// SchedulerProcessDir returns the file-backend directory for the scheduler
// process's state: its HA lock, pause flag, restart backoff, and outages. It
// is the scheduler state directory, whose records it does not share.
func SchedulerProcessDir(paths config.PathsConfig) string {
	return SchedulerStateDir(paths)
}

// The record IDs each collection accepts where a server-wide collection and
// a project collection share a directory, so neither reads the other's.
var (
	incidentProviderIDPrefixes    = []string{"providers/"}
	incidentIDPrefixes            = []string{"policies/", "states/"}
	notificationChannelIDPrefixes = []string{"channels/", "workspace"}
	notificationIDPrefixes        = []string{"dags/", "routes/"}
	schedulerProcessIDPrefixes    = []string{"down", "paused", "restart_backoff"}
	schedulerStateIDPrefixes      = []string{"catchup", "checkpoint", "dags/", "state"}
)

// NewBackend creates a file backend from the configured persistence paths.
// It keeps every collection where v2 keeps it. It does not access the
// filesystem; collections create directories lazily.
func NewBackend(paths config.PathsConfig) *Backend {
	specs := serverCollectionSpecs(paths)
	maps.Copy(specs, projectCollectionSpecs(config.ProjectPaths{
		SettingsDir:   paths.DataDir,
		OtherStateDir: paths.DataDir,
		DAGStateDir:   paths.DAGStateDir,
		QueueDir:      paths.QueueDir,
		ViewsDir:      paths.ViewsDir,
	}))
	return &Backend{dataDir: paths.DataDir, specs: specs}
}

// newServerBackend creates a file backend for the collections that every
// project shares.
func newServerBackend(paths config.PathsConfig) *Backend {
	return &Backend{dataDir: paths.DataDir, specs: serverCollectionSpecs(paths)}
}

// newProjectBackend creates a file backend for the collections of the
// project whose directories are p.
func newProjectBackend(p config.ProjectPaths) *Backend {
	return &Backend{dataDir: p.OtherStateDir, specs: projectCollectionSpecs(p)}
}

func serverCollectionSpecs(paths config.PathsConfig) map[string]collectionSpec {
	distributedDir := filepath.Join(paths.DataDir, "distributed")
	return map[string]collectionSpec{
		persis.CollectionAPIKeys:               {dir: paths.APIKeysDir, indented: true},
		persis.CollectionActiveDistributedRuns: {dir: filepath.Join(distributedDir, "active-runs")},
		persis.CollectionConnectionCatalogs:    {dir: filepath.Join(paths.DataDir, "connection-catalogs")},
		persis.CollectionConnectionTokens:      {dir: filepath.Join(paths.DataDir, "connection-tokens")},
		persis.CollectionConnections:           {dir: filepath.Join(paths.DataDir, "connections"), indented: true},
		persis.CollectionDAGRunLeases:          {dir: filepath.Join(distributedDir, "leases")},
		persis.CollectionDispatchTasks: {
			dir:        distributedDir,
			idPrefixes: []string{"pending/", "claims/", "admissions/"},
		},
		persis.CollectionIncidentProviders: {
			dir:        filepath.Join(paths.DataDir, incidentsDirName),
			idPrefixes: incidentProviderIDPrefixes,
			indented:   true,
		},
		persis.CollectionLicense: {dir: filepath.Join(paths.DataDir, "license"), indented: true},
		persis.CollectionNotificationChannels: {
			dir:        filepath.Join(paths.DataDir, notificationsDirName),
			idPrefixes: notificationChannelIDPrefixes,
			indented:   true,
		},
		persis.CollectionOAuthApps:        {dir: filepath.Join(paths.DataDir, "oauth-apps"), indented: true},
		persis.CollectionPublicWebhooks:   {dir: filepath.Join(paths.DataDir, "public-webhooks"), indented: true},
		persis.CollectionRemoteNodes:      {dir: paths.RemoteNodesDir, indented: true},
		persis.CollectionSchedulerProcess: {dir: SchedulerProcessDir(paths), idPrefixes: schedulerProcessIDPrefixes, indented: true},
		persis.CollectionUpgradeCheck:     {dir: filepath.Join(paths.DataDir, "upgrade"), indented: true},
		persis.CollectionUsers:            {dir: paths.UsersDir, indented: true},
		persis.CollectionWebhooks:         {dir: paths.WebhooksDir, indented: true},
		persis.CollectionWebhookDAGClaims: {dir: filepath.Join(filepath.Dir(paths.WebhooksDir), "webhook-dag-claims"), indented: true},
		persis.CollectionWorkerHeartbeats: {dir: filepath.Join(distributedDir, "workers")},
		persis.CollectionWorkerNodes:      {dir: filepath.Join(paths.DataDir, "worker-nodes"), indented: true},
		persis.CollectionWorkspaces:       {dir: paths.WorkspacesDir, indented: true},
	}
}

// projectCollectionSpecs places a project's settings in p.SettingsDir and
// its other collections in p.OtherStateDir, unless p names their directory.
func projectCollectionSpecs(p config.ProjectPaths) map[string]collectionSpec {
	settings := func(name string) string { return filepath.Join(p.SettingsDir, name) }
	other := func(name string) string { return filepath.Join(p.OtherStateDir, name) }
	return map[string]collectionSpec{
		persis.CollectionAgentSessionCleanups: {dir: other("agent-session-cleanups")},
		persis.CollectionDAGPins:              {dir: other("dag-pins"), indented: true},
		persis.CollectionDAGSettings:          {dir: other("dag-settings"), indented: true},
		persis.CollectionDAGState:             {dir: p.DAGStateDir},
		persis.CollectionHostKeys:             {dir: settings("host-keys"), indented: true},
		persis.CollectionIncidents:            {dir: settings(incidentsDirName), idPrefixes: incidentIDPrefixes, indented: true},
		persis.CollectionInventories:          {dir: settings("inventories"), indented: true},
		persis.CollectionNotifications:        {dir: settings(notificationsDirName), idPrefixes: notificationIDPrefixes, indented: true},
		persis.CollectionProfiles:             {dir: settings("profiles"), indented: true},
		persis.CollectionQueue:                {dir: p.QueueDir},
		persis.CollectionSchedulerState:       {dir: other(schedulerStateDirName), idPrefixes: schedulerStateIDPrefixes, indented: true},
		persis.CollectionSecrets:              {dir: settings("secrets"), indented: true},
		persis.CollectionViews:                {dir: p.ViewsDir, indented: true},
		persis.CollectionWatchState:           {dir: other("watch_state")},
	}
}

// Collection returns the collection identified by name.
func (b *Backend) Collection(name string) persis.Collection {
	if col, ok := b.cols.Load(name); ok {
		return col.(*Collection)
	}

	spec := b.spec(name)
	var opts []CollectionOption
	if spec.indented {
		opts = append(opts, WithIndentedJSON())
	}
	if len(spec.idPrefixes) > 0 {
		opts = append(opts, withIDPrefixes(spec.idPrefixes...))
	}
	col, _ := b.cols.LoadOrStore(name, NewCollection(spec.dir, opts...))
	return col.(*Collection)
}

// Lock returns a lock on the named collection's directory, which every
// process that shares the directory honors.
func (b *Backend) Lock(name string) persis.Lock {
	return dirLockOn{dir: b.spec(name).dir, staleThreshold: lockStaleThreshold}
}

func (b *Backend) spec(name string) collectionSpec {
	if !validCollectionName(name) {
		panic(fmt.Sprintf("file backend: invalid collection name %q", name))
	}
	spec, ok := b.specs[name]
	if !ok {
		spec.dir = filepath.Join(b.dataDir, name)
	}
	return spec
}

// lockStaleThreshold is how long a holder may go without renewing its lock
// before another client takes the lock over.
const lockStaleThreshold = 30 * time.Second

// dirLockOn locks a collection directory. Listing skips the lock's own
// directory, so the lock lives beside the records it guards.
type dirLockOn struct {
	dir            string
	staleThreshold time.Duration
	// renewEvery is how often the holder renews the lock. Zero means a
	// quarter of staleThreshold.
	renewEvery time.Duration
}

func (l dirLockOn) Do(ctx context.Context, fn func(held func() error) error) error {
	lock := dirlock.New(l.dir, &dirlock.LockOptions{StaleThreshold: l.staleThreshold})
	if err := lock.Lock(ctx); err != nil {
		return err
	}
	held, release := renewHeld(ctx, lock, l.staleThreshold, l.renewEvery)
	defer release()
	return fn(held)
}

// renewHeld renews lock, which the caller holds, until release, which also
// unlocks it. A zero renewEvery means a quarter of staleThreshold.
//
// The holder renews the lock while it works, so a slow write does not let
// another client take it over. held refuses a write once the last renewal
// is three quarters of the stale threshold old, before any contender can
// judge the lock stale, so a failed renewal or a paused process stops the
// next write.
func renewHeld(
	ctx context.Context,
	lock dirlock.DirLock,
	staleThreshold, renewEvery time.Duration,
) (held func() error, release func()) {
	var renewedAt atomic.Pointer[time.Time]
	markRenewed := func() {
		now := time.Now()
		renewedAt.Store(&now)
	}
	markRenewed()
	renewCtx, stopRenewing := context.WithCancel(ctx)
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		ticker := time.NewTicker(cmp.Or(renewEvery, staleThreshold/4))
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				if lock.Heartbeat(renewCtx) == nil {
					markRenewed()
				}
			}
		}
	}()

	maxAge := staleThreshold * 3 / 4
	held = func() error {
		if time.Since(*renewedAt.Load()) > maxAge || !lock.IsHeldByMe() {
			return fmt.Errorf("file backend: lock lost before the write: %w", dirlock.ErrLockNotHeld)
		}
		return nil
	}
	release = func() {
		stopRenewing()
		<-renewed
		_ = lock.Unlock()
	}
	return held, release
}

func validCollectionName(name string) bool {
	if name == "" {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool {
		return r != '-' && r != '_' &&
			(r < '0' || r > '9') &&
			(r < 'A' || r > 'Z') &&
			(r < 'a' || r > 'z')
	}) == -1
}
