// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

// noDispatchFlag marks a run that a Dagu process launched after it chose to
// run it on this host, so the run does not choose again. Every start and
// retry that the launcher builds sets it.
var noDispatchFlag = commandLineFlag{
	name:   "no-dispatch",
	usage:  "Run on this host without the coordinator (set by Dagu for a run it already placed)",
	isBool: true,
	hidden: true,
}

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
// run launched with --no-dispatch runs on this host.
func placeRun(ctx *Context, dag *ir.DAG, targetNodes []string, admitted string) (runPlacement, error) {
	if launchedPlaced(ctx) {
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
	return runPlacement{client: client, worker: worker}, nil
}

// launchedPlaced reports whether a Dagu process launched the command for a
// run it already placed on this host.
func launchedPlaced(ctx *Context) bool {
	placed, err := ctx.Command.Flags().GetBool(noDispatchFlag.name)
	return err == nil && placed
}
