// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"cmp"
	"context"
	"fmt"
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

const schedulerStateDirName = "scheduler"

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
// It does not access the filesystem; collections create directories lazily.
func NewBackend(paths config.PathsConfig) *Backend {
	distributedDir := filepath.Join(paths.DataDir, "distributed")
	incidentsDir := filepath.Join(paths.DataDir, "incidents")
	notificationsDir := filepath.Join(paths.DataDir, "notifications")
	return &Backend{
		dataDir: paths.DataDir,
		specs: map[string]collectionSpec{
			persis.CollectionAPIKeys:               {dir: paths.APIKeysDir, indented: true},
			persis.CollectionActiveDistributedRuns: {dir: filepath.Join(distributedDir, "active-runs")},
			persis.CollectionAgentSessionCleanups:  {dir: filepath.Join(paths.DataDir, "agent-session-cleanups")},
			persis.CollectionDAGPins:               {dir: filepath.Join(paths.DataDir, "dag-pins"), indented: true},
			persis.CollectionDAGRunLeases:          {dir: filepath.Join(distributedDir, "leases")},
			persis.CollectionDAGSettings:           {dir: filepath.Join(paths.DataDir, "dag-settings"), indented: true},
			persis.CollectionDAGState:              {dir: paths.DAGStateDir},
			persis.CollectionDispatchTasks: {
				dir:        distributedDir,
				idPrefixes: []string{"pending/", "claims/", "admissions/"},
			},
			persis.CollectionHostKeys:          {dir: filepath.Join(paths.DataDir, "host-keys"), indented: true},
			persis.CollectionIncidentProviders: {dir: incidentsDir, idPrefixes: incidentProviderIDPrefixes, indented: true},
			persis.CollectionIncidents:         {dir: incidentsDir, idPrefixes: incidentIDPrefixes, indented: true},
			persis.CollectionInventories:       {dir: filepath.Join(paths.DataDir, "inventories"), indented: true},
			persis.CollectionLicense:           {dir: filepath.Join(paths.DataDir, "license"), indented: true},
			persis.CollectionNotificationChannels: {
				dir:        notificationsDir,
				idPrefixes: notificationChannelIDPrefixes,
				indented:   true,
			},
			persis.CollectionNotifications:    {dir: notificationsDir, idPrefixes: notificationIDPrefixes, indented: true},
			persis.CollectionProfiles:         {dir: filepath.Join(paths.DataDir, "profiles"), indented: true},
			persis.CollectionPublicWebhooks:   {dir: filepath.Join(paths.DataDir, "public-webhooks"), indented: true},
			persis.CollectionQueue:            {dir: paths.QueueDir},
			persis.CollectionRemoteNodes:      {dir: paths.RemoteNodesDir, indented: true},
			persis.CollectionSchedulerProcess: {dir: SchedulerProcessDir(paths), idPrefixes: schedulerProcessIDPrefixes, indented: true},
			persis.CollectionSchedulerState:   {dir: SchedulerStateDir(paths), idPrefixes: schedulerStateIDPrefixes, indented: true},
			persis.CollectionSecrets:          {dir: filepath.Join(paths.DataDir, "secrets"), indented: true},
			persis.CollectionUpgradeCheck:     {dir: filepath.Join(paths.DataDir, "upgrade"), indented: true},
			persis.CollectionUsers:            {dir: paths.UsersDir, indented: true},
			persis.CollectionViews:            {dir: paths.ViewsDir, indented: true},
			persis.CollectionWatchState:       {dir: filepath.Join(paths.DataDir, "watch_state")},
			persis.CollectionWebhooks:         {dir: paths.WebhooksDir, indented: true},
			persis.CollectionWebhookDAGClaims: {dir: filepath.Join(filepath.Dir(paths.WebhooksDir), "webhook-dag-claims"), indented: true},
			persis.CollectionWorkerHeartbeats: {dir: filepath.Join(distributedDir, "workers")},
			persis.CollectionWorkerNodes:      {dir: filepath.Join(paths.DataDir, "worker-nodes"), indented: true},
			persis.CollectionWorkspaces:       {dir: paths.WorkspacesDir, indented: true},
		},
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
	defer func() { _ = lock.Unlock() }()

	// The holder renews the lock while fn runs, so a slow write does not let
	// another client take it over. held refuses a write once the last
	// renewal is three quarters of the stale threshold old, before any
	// contender can judge the lock stale, so a failed renewal or a paused
	// process stops the next write.
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
		ticker := time.NewTicker(cmp.Or(l.renewEvery, l.staleThreshold/4))
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
	defer func() {
		stopRenewing()
		<-renewed
	}()

	maxAge := l.staleThreshold * 3 / 4
	return fn(func() error {
		if time.Since(*renewedAt.Load()) > maxAge || !lock.IsHeldByMe() {
			return fmt.Errorf("file backend: lock lost before the write: %w", dirlock.ErrLockNotHeld)
		}
		return nil
	})
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
