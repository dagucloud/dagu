// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
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

// SchedulerStateDir returns the file-backend directory for scheduler state.
func SchedulerStateDir(paths config.PathsConfig) string {
	return filepath.Join(paths.DataDir, schedulerStateDirName)
}

// NewBackend creates a file backend from the configured persistence paths.
// It does not access the filesystem; collections create directories lazily.
func NewBackend(paths config.PathsConfig) *Backend {
	distributedDir := filepath.Join(paths.DataDir, "distributed")
	return &Backend{
		dataDir: paths.DataDir,
		specs: map[string]collectionSpec{
			persis.CollectionAPIKeys:               {dir: paths.APIKeysDir, indented: true},
			persis.CollectionActiveDistributedRuns: {dir: filepath.Join(distributedDir, "active-runs")},
			persis.CollectionAgentSessionCleanups:  {dir: filepath.Join(paths.DataDir, "agent-session-cleanups")},
			persis.CollectionDAGRunLeases:          {dir: filepath.Join(distributedDir, "leases")},
			persis.CollectionDAGSettings:           {dir: filepath.Join(paths.DataDir, "dag-settings"), indented: true},
			persis.CollectionDAGState:              {dir: paths.DAGStateDir},
			persis.CollectionDispatchTasks: {
				dir:        distributedDir,
				idPrefixes: []string{"pending/", "claims/", "admissions/"},
			},
			persis.CollectionIncidents:        {dir: filepath.Join(paths.DataDir, "incidents"), indented: true},
			persis.CollectionLicense:          {dir: filepath.Join(paths.DataDir, "license"), indented: true},
			persis.CollectionNotifications:    {dir: filepath.Join(paths.DataDir, "notifications"), indented: true},
			persis.CollectionProfiles:         {dir: filepath.Join(paths.DataDir, "profiles"), indented: true},
			persis.CollectionQueue:            {dir: paths.QueueDir},
			persis.CollectionRemoteNodes:      {dir: paths.RemoteNodesDir, indented: true},
			persis.CollectionSchedulerState:   {dir: SchedulerStateDir(paths), indented: true},
			persis.CollectionSecrets:          {dir: filepath.Join(paths.DataDir, "secrets"), indented: true},
			persis.CollectionUpgradeCheck:     {dir: filepath.Join(paths.DataDir, "upgrade"), indented: true},
			persis.CollectionUsers:            {dir: paths.UsersDir, indented: true},
			persis.CollectionViews:            {dir: paths.ViewsDir, indented: true},
			persis.CollectionWebhooks:         {dir: paths.WebhooksDir, indented: true},
			persis.CollectionWorkerHeartbeats: {dir: filepath.Join(distributedDir, "workers")},
			persis.CollectionWorkspaces:       {dir: paths.WorkspacesDir, indented: true},
		},
	}
}

// Collection returns the collection identified by name.
func (b *Backend) Collection(name string) persis.Collection {
	if !validCollectionName(name) {
		panic(fmt.Sprintf("file backend: invalid collection name %q", name))
	}
	if col, ok := b.cols.Load(name); ok {
		return col.(*Collection)
	}

	spec, ok := b.specs[name]
	if !ok {
		spec.dir = filepath.Join(b.dataDir, name)
	}
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
