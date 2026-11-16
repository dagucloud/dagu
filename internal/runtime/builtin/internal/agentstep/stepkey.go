// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// StepKey returns the key that a step's stored records go by: its ID, or its
// name when it has none.
func StepKey(step ir.Step) string {
	if step.ID != "" {
		return step.ID
	}
	return step.Name
}

// StepKeys returns the keys of every step of dag that stores records with it:
// its steps, its handlers, and the bodies of its foreach steps.
func StepKeys(dag *ir.DAG) []string {
	if dag == nil {
		return nil
	}
	var keys []string
	var add func(steps []ir.Step)
	add = func(steps []ir.Step) {
		for _, step := range steps {
			keys = append(keys, StepKey(step))
			if step.Foreach != nil {
				add(step.Foreach.Steps)
			}
		}
	}
	add(dag.Steps)
	for _, handler := range []*ir.Step{
		dag.HandlerOn.Init, dag.HandlerOn.Failure, dag.HandlerOn.Success,
		dag.HandlerOn.Abort, dag.HandlerOn.Exit, dag.HandlerOn.Wait,
	} {
		if handler != nil {
			add([]ir.Step{*handler})
		}
	}
	return keys
}

// ClaimRecords moves the replay records that released versions stored for a
// step of dag under an older file name to the step's own file, when no other
// step of dag had that name. A failure leaves them in place and is logged.
func ClaimRecords(ctx context.Context, cache *replaycache.Store, dag *ir.DAG, stepKey string) {
	if dag == nil {
		return
	}
	if err := cache.Claim(dag.Name, stepKey, StepKeys(dag)); err != nil {
		logger.Warn(ctx, "Failed to move replay records stored under an older name", tag.Error(err))
	}
}
