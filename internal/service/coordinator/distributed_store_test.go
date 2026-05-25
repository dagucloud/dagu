// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"path/filepath"

	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
)

func newTestDispatchTaskStore(baseDir string) *store.DispatchTaskStore {
	return store.NewDispatchTaskStore(file.NewCollection(baseDir))
}

func newTestWorkerHeartbeatStore(baseDir string) *store.WorkerHeartbeatStore {
	return store.NewWorkerHeartbeatStore(file.NewCollection(filepath.Join(baseDir, "workers")))
}

func newTestDAGRunLeaseStore(baseDir string) *store.DAGRunLeaseStore {
	return store.NewDAGRunLeaseStore(file.NewCollectionWithLockRoot(filepath.Join(baseDir, "leases"), baseDir))
}

func newTestActiveDistributedRunStore(baseDir string) *store.ActiveDistributedRunStore {
	return store.NewActiveDistributedRunStore(file.NewCollectionWithLockRoot(filepath.Join(baseDir, "active-runs"), baseDir))
}
