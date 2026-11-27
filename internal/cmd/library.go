// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// errLibraryEnqueue refuses to queue a library DAG's run: no scheduler
// reads a project's library queues, so the run would never start.
var errLibraryEnqueue = errors.New("library DAGs cannot be queued; no scheduler reads a project's library queues")

// inLibraryOf returns c, or a copy of c that records runs in its project's
// library stores when the DAG file that args name is a library DAG. A
// sub-DAG run stays in the stores of its root's run.
func (c *Context) inLibraryOf(args []string) (*Context, error) {
	if !c.Config.ProjectsEnabled || c.projectStores == nil || len(args) == 0 ||
		!takesDAGFile(commandFamilyName(c.Command)) || c.isSubDAGRunCommand() {
		return c, nil
	}
	path, err := spec.ResolveDAGFile(args[0], c.projectPaths().DAGsDir)
	if err != nil || !fileutil.FileExists(path) {
		return c, nil
	}
	stores, err := c.projectStores.RunStores(c, path)
	if err != nil || stores == c.projectStores {
		return c, err
	}
	persistence := newPersistence(c.serverStores, stores)
	clone := *c
	clone.projectStores = stores
	clone.Persistence = persistence
	clone.DAGRunMgr = runtime.NewManager(persistence.DAGRunRepository, persistence.ProcRepository, c.Config,
		projectManagerOptions(c.Config, c.project)...)
	return &clone, nil
}

// isSubDAGRunCommand reports whether the command runs a sub-DAG run, which
// its caller resolved.
func (c *Context) isSubDAGRunCommand() bool {
	parent, err := c.Command.Flags().GetString("parent")
	return err == nil && parent != ""
}

// inLibrary reports whether the command records runs in a library's stores.
func (c *Context) inLibrary() bool {
	return c.projectStores != nil && c.projectStores.InLibrary()
}

// dispatchProject returns the scope in which the coordinator records the
// runs the command sends to workers: those of the addressed project's own
// DAGs, or of its library DAGs. It is the zero scope while projects are off.
func (c *Context) dispatchProject() dispatch.ProjectScope {
	if !c.Config.ProjectsEnabled {
		return dispatch.ProjectScope{}
	}
	scope := dispatch.ProjectScope{Library: c.inLibrary()}
	if !strings.EqualFold(c.project.Name, project.DefaultName) {
		scope.ID = c.project.ID
	}
	return scope
}

// subDAGStateStore returns how a child run finds its state store, or nil
// while projects are off.
func (c *Context) subDAGStateStore() func(ctx context.Context, path string) (dagrun.StateStore, error) {
	if c.projectStores == nil || !c.Config.ProjectsEnabled {
		return nil
	}
	return c.projectStores.StateStoreFor
}

// enqueueStores returns how dag.enqueue finds the stores that record the run
// it admits, or nil while projects are off. A library DAG's run is refused.
func (c *Context) enqueueStores() func(ctx context.Context, path string) (coordinator.EnqueueStores, error) {
	if c.projectStores == nil || !c.Config.ProjectsEnabled {
		return nil
	}
	stores := c.projectStores
	return func(ctx context.Context, path string) (coordinator.EnqueueStores, error) {
		runs, err := stores.RunStores(ctx, path)
		if err != nil {
			return coordinator.EnqueueStores{}, err
		}
		if runs.InLibrary() {
			return coordinator.EnqueueStores{}, errLibraryEnqueue
		}
		return coordinator.EnqueueStores{
			DAGRunRepository: runs.DAGRunRepository,
			QueueStore:       runs.QueueStore,
			LogDir:           runs.Paths.LogDir,
			ArtifactDir:      runs.Paths.ArtifactDir,
		}, nil
	}
}
