// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// slotPaths are the run context values that differ from one slot's run to
// the next.
var slotPaths = []string{
	"context.run.scheduled_at",
	"context.run.interval_start",
	"context.run.interval_end",
	"context.run.next_scheduled_at",
}

// ReadsSlot reports whether the runs of dag can tell their slots apart: it
// declares dates, refers to its run's slot, window, or dates in its
// definition or base config, or calls a sub-DAG, which inherits the slot.
// Every run of any other DAG does the same work, whatever its slot.
func ReadsSlot(dag *ir.DAG) bool {
	if dag == nil {
		return false
	}
	if len(dag.Dates) > 0 || callsSubDAG(dag) {
		return true
	}
	for _, def := range dag.ParamDefs {
		if refersToSlot(def.Eval) {
			return true
		}
	}
	return refersToSlot(string(dag.YamlData)) || refersToSlot(string(dag.BaseConfigData))
}

func refersToSlot(text string) bool {
	return cmnvalue.HasReferenceToNamespace(text, "dates") || cmnvalue.HasReferenceToPath(text, slotPaths...)
}

func callsSubDAG(dag *ir.DAG) bool {
	for i := range dag.Steps {
		if dag.Steps[i].SubDAG != nil {
			return true
		}
	}
	for _, step := range []*ir.Step{dag.HandlerOn.Init, dag.HandlerOn.Success, dag.HandlerOn.Failure, dag.HandlerOn.Abort, dag.HandlerOn.Exit, dag.HandlerOn.Wait} {
		if step != nil && step.SubDAG != nil {
			return true
		}
	}
	return false
}
