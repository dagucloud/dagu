// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// causeWorkspace is the workspace of the run that a sub-DAG step belongs
// to. The run's failure skip reads this workspace's routes, so the step
// takes only the cause of a child run in the same workspace, whose
// connection messages go through those routes too. A child in another
// workspace sends its connection messages elsewhere, and the parent's
// failure message is the only one a channel of the parent's routes gets.
type causeWorkspace struct {
	name string
	// valid is false when the run's workspace labels name no single
	// workspace, so the run's failure is never skipped.
	valid bool
}

// runCauseWorkspace returns the workspace of the run that ctx belongs to.
func runCauseWorkspace(ctx context.Context) causeWorkspace {
	rCtx, ok := runctx.LookupContext(ctx)
	if !ok || rCtx.DAG == nil {
		return causeWorkspace{}
	}
	name, state := workspace.WorkspaceLabelFromLabels(rCtx.DAG.Labels)
	return causeWorkspace{name: name, valid: state != workspace.WorkspaceLabelInvalid}
}

// of returns the cause of result that the step may take, or nil.
func (w causeWorkspace) of(result *ir.RunStatus) []string {
	if result == nil || !w.valid || result.CauseWorkspace != w.name {
		return nil
	}
	return result.ConnectionCause
}
