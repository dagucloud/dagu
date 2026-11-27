// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"os"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

// runPlacement is where a run started by a command runs.
type runPlacement struct {
	// client reaches the coordinator that a dispatched run goes through. It
	// is nil for a run on this host.
	client coordinator.Client
	// worker is the worker that runs a run bound to a worker node, or empty.
	worker string
}

// dispatched reports whether the run goes through the coordinator.
func (p runPlacement) dispatched() bool {
	return p.client != nil
}

// placeRun decides where a top-level run of dag runs, by the rules the API
// and the scheduler apply: on the worker of the worker node it is bound to,
// through the coordinator for a worker selector or the distributed execution
// mode, or on this host. targetNodes and admitted are as for boundWorker. A
// run that a Dagu process launched after placing it runs on this host. A
// run the rules dispatch fails when it cannot be dispatched.
func placeRun(ctx *Context, dag *ir.DAG, targetNodes []string, admitted string) (runPlacement, error) {
	if ctx.runPlaced {
		return runPlacement{}, nil
	}
	worker, client, err := boundWorker(ctx, dag, targetNodes, admitted)
	if err != nil {
		return runPlacement{}, err
	}
	if worker == "" {
		if !dispatch.ShouldDispatchToCoordinator(dag, ctx.coordinatorEnabled(), ctx.Config.DefaultExecMode) {
			return runPlacement{}, nil
		}
		if client, err = ctx.coordinatorClient(); err != nil {
			return runPlacement{}, err
		}
	}
	if dag.Type == ir.TypeBuild {
		return runPlacement{}, dispatch.ErrBuildRequiresLocal
	}
	// A run that cannot be dispatched is refused before the command changes
	// anything, such as the run a restart stops.
	if err := ctx.checkProjectDispatch(); err != nil {
		return runPlacement{}, err
	}
	return runPlacement{client: client, worker: worker}, nil
}

// claimRunPlaced reports whether a Dagu process launched this command for a
// run it placed on this host, and removes the marker from the environment so
// the run's steps, and the commands they start, place their own runs.
func claimRunPlaced() bool {
	placed := os.Getenv(runenv.EnvKeyRunPlaced) != ""
	_ = os.Unsetenv(runenv.EnvKeyRunPlaced)
	return placed
}
